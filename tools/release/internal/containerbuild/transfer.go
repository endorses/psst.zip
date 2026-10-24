package containerbuild

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/scanning"
	"github.com/endorses/psst.zip/tools/release/internal/source"
)

const transferMetadata = "candidate-transfer.json"
const transferFileLimit int64 = 8 << 30
const transferTotalLimit int64 = 32 << 30
const transferRawLimit int64 = 32 << 20
const transferFileCount = 10000

// TransferFile binds one complete payload, including empty declared stderr files.
type TransferFile struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// NativeArtifact binds one explicitly selected native producer artifact.
type NativeArtifact struct {
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// NativeDescriptor is the current native producer's signed subject. It cannot
// establish authorization before the caller authenticates its exact raw bytes.
type NativeDescriptor struct {
	SchemaVersion                     int                       `json:"schema_version"`
	Kind                              string                    `json:"kind"`
	Source                            scanning.NativeIdentity   `json:"source"`
	OriginalTestedConfigs             map[string]string         `json:"original_tested_configs"`
	TestedConfigs                     map[string]string         `json:"tested_configs"`
	SourceHelperConfig                string                    `json:"source_helper_config"`
	OCIExporter                       string                    `json:"oci_exporter"`
	Artifacts                         map[string]NativeArtifact `json:"artifacts"`
	PublicationAuthorized             bool                      `json:"publication_authorized"`
	MeasurementAuthenticationRequired bool                      `json:"measurement_authentication_required"`
}

// Transfer preserves the existing cross-job inventory contract. Run/attempt
// provenance belongs to the caller's exact authenticated GitHub binding.
type Transfer struct {
	SchemaVersion                     int                     `json:"schema_version"`
	Kind                              string                  `json:"kind"`
	Source                            scanning.NativeIdentity `json:"source"`
	SourceKind                        string                  `json:"source_kind"`
	Files                             map[string]TransferFile `json:"files"`
	PublicationAuthorized             bool                    `json:"publication_authorized"`
	MeasurementAuthenticationRequired bool                    `json:"measurement_authentication_required"`
}

// TransferVerification exposes cached control bytes for authentication equality.
// VerifyTransfer checks hashes/selection only, never source/image/browser policy.
type TransferVerification struct {
	Transfer
	Descriptor    NativeDescriptor `json:"-"`
	MetadataRaw   []byte           `json:"-"`
	DescriptorRaw []byte           `json:"-"`
}

type transferPlan struct {
	root     *os.Root
	files    map[string]TransferFile
	stamps   map[string]os.FileInfo
	limits   map[string]int64
	selected map[string]bool
	total    int64
}

func transferIdentity(identity scanning.NativeIdentity, kind string) error {
	if err := (source.Context{Repository: identity.Repository, Version: identity.Version, Commit: identity.Commit}).Validate(); err != nil {
		return err
	}
	if identity.Platform != "linux/amd64" && identity.Platform != "linux/arm64" || kind != "version-tag" && kind != "planned-main-dispatch" {
		return fmt.Errorf("invalid native transfer platform/source kind")
	}
	return nil
}
func realTransferDirectory(name string) error {
	absolute, err := filepath.Abs(name)
	if err != nil {
		return err
	}
	for current := absolute; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("transfer requires real directory ancestors")
		}
		if current == filepath.Dir(current) {
			break
		}
	}
	return nil
}
func openTransferPlan(directory string) (*transferPlan, error) {
	if err := realTransferDirectory(directory); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	return &transferPlan{root: root, files: map[string]TransferFile{}, stamps: map[string]os.FileInfo{}, limits: map[string]int64{}, selected: map[string]bool{}}, nil
}
func safeTransferPath(name string) bool {
	return name != "" && len(name) <= 4096 && path.Clean(name) == name && name != "." && name != ".." && !path.IsAbs(name) && !strings.HasPrefix(name, "../") && !strings.ContainsAny(name, "\\\x00") && strings.IndexFunc(name, func(r rune) bool { return r < 32 || r == 127 }) < 0
}
func (p *transferPlan) regular(name string) (os.FileInfo, error) {
	if !safeTransferPath(name) {
		return nil, fmt.Errorf("unsafe transfer path")
	}
	for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
		info, err := p.root.Lstat(parent)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("transfer path has a linked/non-directory parent")
		}
	}
	info, err := p.root.Lstat(name)
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("transfer input is not a regular file")
	}
	return info, nil
}
func sameTransferStamp(a, b os.FileInfo) bool {
	return os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}
func (p *transferPlan) add(name, digest string, size *int64, limit int64, nonempty bool) error {
	info, err := p.regular(name)
	if err != nil {
		return err
	}
	if info.Size() < 0 || info.Size() > limit || nonempty && info.Size() == 0 || size != nil && *size != info.Size() || digest != "" && !digestRE.MatchString(digest) {
		return fmt.Errorf("transfer input size/checksum reference exceeds bounds")
	}
	p.selected[name] = true
	if old, ok := p.stamps[name]; ok {
		if !sameTransferStamp(old, info) {
			return fmt.Errorf("transfer input changed during inventory")
		}
		fact := p.files[name]
		if digest != "" && fact.SHA256 != "" && fact.SHA256 != digest {
			return fmt.Errorf("conflicting transfer checksum references")
		}
		if fact.SHA256 == "" {
			fact.SHA256 = digest
		}
		p.files[name] = fact
		if limit < p.limits[name] {
			p.limits[name] = limit
		}
		return nil
	}
	if len(p.files) >= transferFileCount || info.Size() > transferTotalLimit-p.total {
		return fmt.Errorf("transfer inventory exceeds total bounds")
	}
	p.stamps[name] = info
	p.files[name] = TransferFile{digest, info.Size()}
	p.limits[name] = limit
	p.total += info.Size()
	return nil
}
func (p *transferPlan) read(name string) ([]byte, error) {
	if err := p.add(name, "", nil, maxJSON, true); err != nil {
		return nil, err
	}
	file, err := p.root.Open(name)
	if err != nil {
		return nil, err
	}
	opened, statErr := file.Stat()
	raw, readErr := io.ReadAll(io.LimitReader(file, maxJSON+1))
	after, afterErr := file.Stat()
	closeErr := file.Close()
	if statErr != nil || readErr != nil || afterErr != nil || closeErr != nil || len(raw) > maxJSON || !sameTransferStamp(p.stamps[name], opened) || !sameTransferStamp(opened, after) || int64(len(raw)) != opened.Size() {
		return nil, fmt.Errorf("transfer control JSON changed while reading")
	}
	fact := p.files[name]
	digest := assemblyHash(raw)
	if fact.SHA256 != "" && fact.SHA256 != digest {
		return nil, fmt.Errorf("transfer control bytes differ from referenced checksum")
	}
	fact.SHA256 = digest
	p.files[name] = fact
	var checked any
	if err := contract.Decode(raw, maxJSON, &checked); err != nil {
		return nil, err
	}
	return raw, nil
}
func (p *transferPlan) unchanged() error {
	for name, before := range p.stamps {
		after, err := p.regular(name)
		if err != nil || !sameTransferStamp(before, after) {
			return fmt.Errorf("transfer evidence changed during retention")
		}
	}
	return nil
}
func transferKeys(files map[string]TransferFile) []string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (p *transferPlan) stream(ctx context.Context, name string, destination io.Writer) (TransferFile, error) {
	var result TransferFile
	file, err := p.root.Open(name)
	if err != nil {
		return result, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !sameTransferStamp(opened, p.stamps[name]) {
		return result, fmt.Errorf("transfer file changed before streaming")
	}
	hash := sha256.New()
	writer := io.Writer(hash)
	if destination != nil {
		writer = io.MultiWriter(destination, hash)
	}
	count, err := io.Copy(writer, io.LimitReader(contextReader{ctx, file}, p.limits[name]+1))
	after, statErr := file.Stat()
	if err != nil || statErr != nil || count != opened.Size() || count > p.limits[name] || !sameTransferStamp(opened, after) {
		return result, fmt.Errorf("transfer file changed or exceeded streaming bound")
	}
	result = TransferFile{"sha256:" + hex.EncodeToString(hash.Sum(nil)), count}
	expected := p.files[name]
	if expected.SHA256 != "" && result.SHA256 != expected.SHA256 || result.Size != expected.Size {
		return result, fmt.Errorf("transfer payload differs from referenced checksum/size")
	}
	return result, nil
}

// CollectTransfer streams only selected producer bytes into a new private tree.
// A single terminal inventory is written after all copies/stamps agree; heavy
// image files retain their paths for the workflow's separate image upload.
func CollectTransfer(ctx context.Context, private, output string, identity scanning.NativeIdentity, sourceKind string) (TransferVerification, error) {
	var result TransferVerification
	if err := transferIdentity(identity, sourceKind); err != nil {
		return result, err
	}
	if err := realTransferDirectory(filepath.Dir(output)); err != nil {
		return result, err
	}
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		return result, fmt.Errorf("transfer output must be a new real directory")
	}
	plan, err := openTransferPlan(private)
	if err != nil {
		return result, err
	}
	defer plan.root.Close()
	descriptor, descriptorRaw, err := plan.selectNative(identity)
	if err != nil {
		return result, err
	}
	if err := os.Mkdir(output, 0700); err != nil {
		return result, err
	}
	actual := map[string]TransferFile{}
	for _, name := range transferKeys(plan.files) {
		target := filepath.Join(output, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return result, err
		}
		file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return result, err
		}
		fact, copyErr := plan.stream(ctx, name, file)
		syncErr, closeErr := file.Sync(), file.Close()
		if copyErr != nil || syncErr != nil || closeErr != nil {
			return result, fmt.Errorf("candidate input changed or failed during transfer")
		}
		actual[name] = fact
	}
	if err := plan.unchanged(); err != nil {
		return result, err
	}
	result = TransferVerification{Transfer: Transfer{SchemaVersion: 1, Kind: "native-candidate-transfer", Source: identity, SourceKind: sourceKind, Files: actual, MeasurementAuthenticationRequired: true}, Descriptor: descriptor, DescriptorRaw: descriptorRaw}
	result.MetadataRaw, err = contract.JSON(result.Transfer)
	if err != nil {
		return result, err
	}
	if err := writeRecord(filepath.Join(output, transferMetadata), result.MetadataRaw); err != nil {
		return result, err
	}
	return result, nil
}

// VerifyTransfer hashes the complete reconstructed tree before reading selected
// producer claims. The caller authenticates exact MetadataRaw/DescriptorRaw for
// its run/attempt and compares those cached bytes before using returned claims.
func VerifyTransfer(ctx context.Context, tree string, identity scanning.NativeIdentity, sourceKind string) (TransferVerification, error) {
	var result TransferVerification
	if err := transferIdentity(identity, sourceKind); err != nil {
		return result, err
	}
	plan, err := openTransferPlan(tree)
	if err != nil {
		return result, err
	}
	defer plan.root.Close()
	metadata, err := plan.read(transferMetadata)
	if err != nil {
		return result, err
	}
	var record Transfer
	if err := contract.Decode(metadata, maxJSON, &record); err != nil {
		return result, err
	}
	if err := checkTransferRecord(metadata, record, identity, sourceKind); err != nil {
		return result, err
	}
	metadataStamp := plan.stamps[transferMetadata]
	// Metadata is not itself listed in the payload table.
	plan.files = map[string]TransferFile{}
	plan.stamps = map[string]os.FileInfo{}
	plan.limits = map[string]int64{}
	plan.total = 0
	for _, name := range transferKeys(record.Files) {
		expected := record.Files[name]
		if name == transferMetadata || !digestRE.MatchString(expected.SHA256) || expected.Size < 0 {
			return result, fmt.Errorf("invalid transfer payload checksum/path")
		}
		if err := plan.add(name, expected.SHA256, &expected.Size, transferFileLimit, false); err != nil {
			return result, err
		}
		if _, err := plan.stream(ctx, name, nil); err != nil {
			return result, err
		}
	}
	if err := plan.completeTree(record.Files); err != nil {
		return result, err
	}
	plan.selected = map[string]bool{}
	descriptor, descriptorRaw, err := plan.selectNative(identity)
	if err != nil {
		return result, err
	}
	if !reflectTransferFiles(plan.files, record.Files) || len(plan.selected) != len(record.Files) {
		return result, fmt.Errorf("transfer inventory has extra or missing referenced payloads")
	}
	if err := plan.unchanged(); err != nil {
		return result, err
	}
	// Re-read through the original directory handle, retaining its initial file
	// identity as well as its bytes if a directory name was replaced meanwhile.
	plan.stamps[transferMetadata] = metadataStamp
	plan.files[transferMetadata] = TransferFile{SHA256: assemblyHash(metadata), Size: int64(len(metadata))}
	plan.limits[transferMetadata] = maxJSON
	current, err := plan.read(transferMetadata)
	if err != nil || !sameRawTransfer(current, metadata) {
		return result, fmt.Errorf("transfer metadata changed during verification")
	}
	return TransferVerification{Transfer: record, Descriptor: descriptor, MetadataRaw: metadata, DescriptorRaw: descriptorRaw}, nil
}
func reflectTransferFiles(a, b map[string]TransferFile) bool {
	if len(a) != len(b) {
		return false
	}
	for name, fact := range a {
		if b[name] != fact {
			return false
		}
	}
	return true
}
func sameRawTransfer(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func (p *transferPlan) completeTree(files map[string]TransferFile) error {
	expectedFiles := map[string]bool{transferMetadata: true}
	expectedDirs := map[string]bool{}
	for name := range files {
		expectedFiles[name] = true
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			expectedDirs[parent] = true
		}
	}
	err := fs.WalkDir(p.root.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("cannot inventory transferred tree")
		}
		if name == "." {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("linked candidate transfer payload")
		}
		if entry.IsDir() {
			if !expectedDirs[name] {
				return fmt.Errorf("extra candidate transfer directory")
			}
			delete(expectedDirs, name)
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || !expectedFiles[name] {
			return fmt.Errorf("extra/special candidate transfer payload")
		}
		delete(expectedFiles, name)
		return nil
	})
	if err != nil {
		return err
	}
	if len(expectedFiles) != 0 || len(expectedDirs) != 0 {
		return fmt.Errorf("missing candidate transfer tree entries")
	}
	return nil
}
