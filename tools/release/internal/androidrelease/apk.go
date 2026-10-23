package androidrelease

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/artifact"
	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

var legalNames = []string{"AGPL-3.0-only.txt", "THIRD_PARTY_NOTICES.txt", "dependency-inventory.json"}
var abis = map[string]struct {
	wide    bool
	machine uint16
}{"armeabi-v7a": {false, 40}, "arm64-v8a": {true, 183}, "x86": {false, 3}, "x86_64": {true, 62}}

type Manifest struct {
	ApplicationID string `json:"applicationId"`
	Version
	MinSDK    int `json:"minSdk"`
	TargetSDK int `json:"targetSdk"`
}
type Assets struct {
	ABIs      []string          `json:"nativeAbis"`
	Libraries []string          `json:"nativeLibraries"`
	Legal     map[string]string `json:"legalSha256"`
}
type Verification struct {
	Schema   int    `json:"schema_version"`
	Revision string `json:"sourceRevision"`
	Manifest
	Assets
	Signed bool    `json:"signed"`
	Signer *string `json:"signerSha256"`
	Digest string  `json:"apkSha256"`
	Size   int64   `json:"apkSize"`
}

func CertificateDigest(value string) (string, error) {
	value = strings.ToLower(strings.ReplaceAll(value, ":", ""))
	if len(value) != 64 {
		return "", errors.New("invalid public signer fingerprint")
	}
	if _, err := hex.DecodeString(value); err != nil {
		return "", errors.New("invalid public signer fingerprint")
	}
	return value, nil
}
func VerifySignerOutput(value, expected string) error {
	expected, err := CertificateDigest(expected)
	if err != nil {
		return err
	}
	counts := regexp.MustCompile(`(?m)^Number of signers: ([0-9]+)\r?$`).FindAllStringSubmatch(value, -1)
	if len(counts) != 1 || counts[0][1] != "1" {
		return errors.New("APK must have exactly one verified signer")
	}
	recordPattern := regexp.MustCompile(`(?m)^(.+) certificate SHA-256 digest: ([^\r\n]+)\r?$`)
	labelPattern := regexp.MustCompile(`^(?:V(?:1|2|3\.[012]) )?Signer(?: #([1-9][0-9]{0,9}))?:?(?: \(minSdkVersion=([1-9][0-9]{0,9})(?: \(dev release=true\))?, maxSdkVersion=([1-9][0-9]{0,9})\))?$`)
	observed := 0
	for _, record := range recordPattern.FindAllStringSubmatch(value, -1) {
		label := record[1]
		if label == "Source Stamp Signer" || label == "Source Stamp Signer:" {
			continue
		}
		identity := labelPattern.FindStringSubmatch(label)
		if identity == nil || identity[1] != "" && identity[1] != "1" {
			return errors.New("unrecognized or multiple APK signer certificate records")
		}
		if identity[2] != "" {
			min, err1 := strconv.ParseInt(identity[2], 10, 64)
			max, err2 := strconv.ParseInt(identity[3], 10, 64)
			if err1 != nil || err2 != nil || min > max {
				return errors.New("invalid APK signer SDK range")
			}
		}
		digest, err := CertificateDigest(record[2])
		if err != nil || digest != expected {
			return errors.New("APK signer differs from approved signer")
		}
		observed++
	}
	if observed == 0 {
		return errors.New("APK signer is missing")
	}
	return nil
}
func ParseBadging(value string) (Manifest, error) {
	packageMatches := regexp.MustCompile(`(?m)^package: name='([^']+)' versionCode='([0-9]+)' versionName='([^']+)'`).FindAllStringSubmatch(value, -1)
	min := regexp.MustCompile(`(?m)^(?:minSdkVersion|sdkVersion):'([0-9]+)'\r?$`).FindAllStringSubmatch(value, -1)
	target := regexp.MustCompile(`(?m)^targetSdkVersion:'([0-9]+)'\r?$`).FindAllStringSubmatch(value, -1)
	if len(packageMatches) != 1 || len(min) != 1 || len(target) != 1 {
		return Manifest{}, errors.New("APK manifest metadata is incomplete or ambiguous")
	}
	if regexp.MustCompile(`(?m)^application-debuggable(?:\s|$)`).MatchString(value) {
		return Manifest{}, errors.New("APK is debuggable")
	}
	code, e1 := strconv.Atoi(packageMatches[0][2])
	minSDK, e2 := strconv.Atoi(min[0][1])
	targetSDK, e3 := strconv.Atoi(target[0][1])
	if e1 != nil || e2 != nil || e3 != nil {
		return Manifest{}, errors.New("invalid APK SDK/version values")
	}
	return Manifest{packageMatches[0][1], Version{packageMatches[0][3], code}, minSDK, targetSDK}, nil
}
func VerifyELF(data []byte, abi string) error {
	if len(data) < 64 || !bytes.Equal(data[:4], []byte{0x7f, 'E', 'L', 'F'}) || data[4] != 1 && data[4] != 2 || data[5] != 1 && data[5] != 2 {
		return errors.New("native library is not a supported ELF file")
	}
	var order binary.ByteOrder = binary.LittleEndian
	if data[5] == 2 {
		order = binary.BigEndian
	}
	wide := data[4] == 2
	if abi != "" {
		machine, ok := abis[abi]
		if !ok || machine.wide != wide || machine.machine != order.Uint16(data[18:20]) {
			return errors.New("native ELF architecture differs from ABI")
		}
	}
	var offset uint64
	var entrySize, count uint16
	if wide {
		offset = order.Uint64(data[32:40])
		entrySize = order.Uint16(data[54:56])
		count = order.Uint16(data[56:58])
	} else {
		offset = uint64(order.Uint32(data[28:32]))
		entrySize = order.Uint16(data[42:44])
		count = order.Uint16(data[44:46])
	}
	minimum := uint16(32)
	if wide {
		minimum = 56
	}
	if count == 0 || count >= 4096 || entrySize < minimum || offset > uint64(len(data)) || uint64(entrySize)*uint64(count) > uint64(len(data))-offset {
		return errors.New("invalid or truncated ELF program headers")
	}
	loads := 0
	for i := uint64(0); i < uint64(count); i++ {
		start := offset + i*uint64(entrySize)
		entry := data[start : start+uint64(entrySize)]
		if order.Uint32(entry[:4]) != 1 {
			continue
		}
		loads++
		var fileOffset, address, alignment uint64
		if wide {
			fileOffset = order.Uint64(entry[8:16])
			address = order.Uint64(entry[16:24])
			alignment = order.Uint64(entry[48:56])
		} else {
			fileOffset = uint64(order.Uint32(entry[4:8]))
			address = uint64(order.Uint32(entry[8:12]))
			alignment = uint64(order.Uint32(entry[28:32]))
		}
		if alignment < 16384 || alignment&(alignment-1) != 0 || fileOffset%16384 != address%16384 {
			return errors.New("native library lacks 16 KB ELF load alignment")
		}
	}
	if loads == 0 {
		return errors.New("native library has no load segments")
	}
	return nil
}
func zipPayload(file *zip.File, limit uint64) ([]byte, error) {
	if file.UncompressedSize64 > limit {
		return nil, errors.New("APK member exceeds bounds")
	}
	reader, err := file.Open()
	if err != nil {
		return nil, errors.New("APK member could not be opened")
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, int64(limit)+1))
	if err != nil || uint64(len(data)) > limit || uint64(len(data)) != file.UncompressedSize64 {
		return nil, errors.New("APK member content is invalid or exceeds bounds")
	}
	return data, nil
}
func (t Tool) InspectAPKAssets(apk string, version Version, revision string, fixture bool) (Assets, error) {
	result := Assets{ABIs: []string{}, Libraries: []string{}, Legal: map[string]string{}}
	info, err := os.Lstat(apk)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > maxAPK {
		return result, errors.New("invalid APK file")
	}
	archive, err := zip.OpenReader(apk)
	if err != nil {
		return result, errors.New("invalid APK ZIP archive")
	}
	defer archive.Close()
	if len(archive.File) > 50000 {
		return result, errors.New("APK contains too many entries")
	}
	entries := map[string]*zip.File{}
	nativeABIs := map[string]bool{}
	var total uint64
	for _, entry := range archive.File {
		name := entry.Name
		if !artifact.ValidPath(strings.TrimSuffix(name, "/")) || entries[name] != nil || entry.Flags&1 != 0 || entry.Mode()&os.ModeSymlink != 0 {
			return result, errors.New("APK contains unsafe, encrypted or duplicate entries")
		}
		if !entry.Mode().IsRegular() && !entry.FileInfo().IsDir() {
			return result, errors.New("APK contains unsupported entry type")
		}
		entries[name] = entry
		if entry.UncompressedSize64 > maxMember || entry.UncompressedSize64 > uint64(4*maxAPK)-total {
			return result, errors.New("APK expanded size exceeds limit")
		}
		total += entry.UncompressedSize64
		if strings.HasPrefix(name, "lib/") && strings.HasSuffix(name, ".so") {
			parts := strings.Split(name, "/")
			if len(parts) != 3 {
				return result, errors.New("unsupported packaged native ABI")
			}
			if _, ok := abis[parts[1]]; !ok {
				return result, errors.New("unsupported packaged native ABI")
			}
			data, err := zipPayload(entry, maxMember)
			if err != nil {
				return result, err
			}
			if err = VerifyELF(data, parts[1]); err != nil {
				return result, err
			}
			nativeABIs[parts[1]] = true
			result.Libraries = append(result.Libraries, name)
		}
	}
	identityFile := entries["assets/psst-release.json"]
	if identityFile == nil {
		return result, errors.New("APK lacks release identity")
	}
	data, err := zipPayload(identityFile, 65536)
	if err != nil {
		return result, err
	}
	var identity struct {
		Version
		Revision string `json:"sourceRevision"`
		Fixture  *bool  `json:"deviceFixture,omitempty"`
	}
	if err = contract.Decode(data, 65536, &identity); err != nil || identity.Version != version || identity.Revision != revision || fixture && (identity.Fixture == nil || !*identity.Fixture) || !fixture && identity.Fixture != nil {
		return result, errors.New("packaged source/version identity mismatch")
	}
	for _, name := range legalNames {
		file := entries["assets/licenses/"+name]
		if file == nil {
			return result, errors.New("APK lacks required legal resources")
		}
		data, err := zipPayload(file, 8<<20)
		if err != nil {
			return result, err
		}
		source, err := safeRead(filepath.Join(t.Root, "android/app/src/main/assets/licenses", name), 8<<20)
		if err != nil || !bytes.Equal(data, source) {
			return result, errors.New("packaged legal resources differ from selected source")
		}
		result.Legal[name] = digest(data)
	}
	for abi := range nativeABIs {
		result.ABIs = append(result.ABIs, abi)
	}
	sort.Strings(result.ABIs)
	sort.Strings(result.Libraries)
	return result, nil
}
func centralDirectory(apk string) (int64, error) {
	file, err := os.Open(apk)
	if err != nil {
		return 0, errors.New("APK could not be opened")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() < 22 || info.Size() > maxAPK {
		return 0, errors.New("invalid APK size")
	}
	size := min(info.Size(), 65557)
	tail := make([]byte, size)
	if _, err = file.ReadAt(tail, info.Size()-size); err != nil {
		return 0, errors.New("APK directory could not be read")
	}
	for index := len(tail) - 22; index >= 0; index-- {
		entry := tail[index:]
		if !bytes.HasPrefix(entry, []byte{'P', 'K', 5, 6}) || int(binary.LittleEndian.Uint16(entry[20:22])) != len(entry)-22 {
			continue
		}
		if binary.LittleEndian.Uint16(entry[4:6]) != 0 || binary.LittleEndian.Uint16(entry[6:8]) != 0 {
			return 0, errors.New("multi-disk APK is unsupported")
		}
		offset := int64(binary.LittleEndian.Uint32(entry[16:20]))
		length := int64(binary.LittleEndian.Uint32(entry[12:16]))
		if offset < 0 || offset+length != info.Size()-size+int64(index) {
			return 0, errors.New("APK central directory boundary is invalid")
		}
		return offset, nil
	}
	return 0, errors.New("APK central directory is missing")
}
func RequireUnsigned(apk string) error {
	archive, err := zip.OpenReader(apk)
	if err != nil {
		return errors.New("invalid unsigned APK")
	}
	defer archive.Close()
	pattern := regexp.MustCompile(`(?i)^META-INF/[^/]+\.(?:SF|RSA|DSA|EC)$`)
	for _, entry := range archive.File {
		if pattern.MatchString(entry.Name) {
			return errors.New("unsigned APK contains JAR signatures")
		}
	}
	central, err := centralDirectory(apk)
	if err != nil {
		return err
	}
	if central >= 24 {
		file, err := os.Open(apk)
		if err != nil {
			return errors.New("APK could not be opened")
		}
		defer file.Close()
		magic := make([]byte, 16)
		if _, err = file.ReadAt(magic, central-16); err != nil {
			return errors.New("APK signing footer could not be read")
		}
		if string(magic) == "APK Sig Block 42" {
			return errors.New("unsigned APK already contains a signing block")
		}
	}
	return nil
}
func lengthPrefixed(data []byte, offset uint64) ([]byte, uint64, error) {
	if offset > uint64(len(data)) || uint64(len(data))-offset < 4 {
		return nil, 0, errors.New("truncated signing attribute")
	}
	size := uint64(binary.LittleEndian.Uint32(data[offset : offset+4]))
	end := offset + 4 + size
	if end > uint64(len(data)) {
		return nil, 0, errors.New("truncated signing attribute")
	}
	return data[offset+4 : end], end, nil
}
func RejectPublicLineage(apk string) error {
	central, err := centralDirectory(apk)
	if err != nil {
		return err
	}
	if central < 24 {
		return errors.New("APK lacks a signing block")
	}
	file, err := os.Open(apk)
	if err != nil {
		return errors.New("APK could not be opened")
	}
	defer file.Close()
	footer := make([]byte, 24)
	if _, err = file.ReadAt(footer, central-24); err != nil || string(footer[8:]) != "APK Sig Block 42" {
		return errors.New("APK lacks a signing block")
	}
	size := binary.LittleEndian.Uint64(footer[:8])
	if size < 24 || size > 16<<20 || size+8 > uint64(central) {
		return errors.New("invalid signing block size")
	}
	block := make([]byte, size+8)
	if _, err = file.ReadAt(block, central-int64(size)-8); err != nil || binary.LittleEndian.Uint64(block[:8]) != size {
		return errors.New("signing block sizes disagree")
	}
	seen := map[uint32]bool{}
	boundary := uint64(len(block) - 24)
	offset := uint64(8)
	for offset < boundary {
		if boundary-offset < 8 {
			return errors.New("truncated signing block entry")
		}
		length := binary.LittleEndian.Uint64(block[offset : offset+8])
		if length < 4 || length > boundary-offset-8 {
			return errors.New("invalid signing block entry")
		}
		end := offset + 8 + length
		id := binary.LittleEndian.Uint32(block[offset+8 : offset+12])
		if seen[id] {
			return errors.New("duplicate signing block")
		}
		seen[id] = true
		if id == 0x1b93ad61 || id == 0x70e1c89f {
			return errors.New("rotated APK cannot be published")
		}
		if id == 0xf05368c0 {
			value := block[offset+12 : end]
			signers, consumed, err := lengthPrefixed(value, 0)
			if err != nil || consumed != uint64(len(value)) {
				return errors.New("invalid v3 signing block")
			}
			position := uint64(0)
			for position < uint64(len(signers)) {
				signer, next, err := lengthPrefixed(signers, position)
				if err != nil {
					return err
				}
				position = next
				signed, _, err := lengthPrefixed(signer, 0)
				if err != nil {
					return err
				}
				_, current, err := lengthPrefixed(signed, 0)
				if err != nil {
					return err
				}
				_, current, err = lengthPrefixed(signed, current)
				if err != nil || current+8 > uint64(len(signed)) {
					return errors.New("truncated v3 SDK range")
				}
				attributes, current, err := lengthPrefixed(signed, current+8)
				if err != nil || current != uint64(len(signed)) {
					return errors.New("invalid v3 signed data")
				}
				attributeOffset := uint64(0)
				for attributeOffset < uint64(len(attributes)) {
					attribute, next, err := lengthPrefixed(attributes, attributeOffset)
					if err != nil || len(attribute) < 4 {
						return errors.New("invalid v3 signing attribute")
					}
					attributeOffset = next
					if binary.LittleEndian.Uint32(attribute[:4]) == 0x3ba06f8c {
						return errors.New("private signing lineage cannot be published")
					}
				}
			}
		}
		offset = end
	}
	if offset != boundary {
		return errors.New("invalid signing block boundary")
	}
	return nil
}
func (t Tool) VerifyAPK(ctx context.Context, apk, sdk, signer, versionFile, revision string, fixture bool) (Verification, error) {
	result := Verification{Schema: 1, Revision: revision}
	if !revisionPattern.MatchString(revision) {
		return result, errors.New("invalid source revision")
	}
	version, err := VersionMetadata(versionFile)
	if err != nil {
		return result, err
	}
	assets, err := t.InspectAPKAssets(apk, version, revision, fixture)
	if err != nil {
		return result, err
	}
	output, err := t.command(ctx, 120*time.Second, filepath.Join(sdk, "aapt2"), "dump", "badging", apk)
	if err != nil {
		return result, err
	}
	manifest, err := ParseBadging(output)
	if err != nil {
		return result, err
	}
	catalog, err := safeRead(filepath.Join(t.Root, "android/gradle/libs.versions.toml"), 1<<20)
	if err != nil {
		return result, err
	}
	sdkVersion := func(name string) (int, error) {
		matches := regexp.MustCompile(`(?m)^`+regexp.QuoteMeta(name)+`\s*=\s*"([0-9]+)"\s*$`).FindAllStringSubmatch(string(catalog), -1)
		if len(matches) != 1 {
			return 0, errors.New("missing or ambiguous SDK version catalog entry")
		}
		return strconv.Atoi(matches[0][1])
	}
	minSDK, err := sdkVersion("android-minSdk")
	if err != nil {
		return result, err
	}
	targetSDK, err := sdkVersion("android-targetSdk")
	if err != nil {
		return result, err
	}
	if manifest != (Manifest{Package, version, minSDK, targetSDK}) {
		return result, errors.New("packaged manifest differs from release configuration")
	}
	if _, err = t.command(ctx, 120*time.Second, filepath.Join(sdk, "zipalign"), "-c", "-P", "16", "4", apk); err != nil {
		return result, err
	}
	if signer == "" {
		if err = RequireUnsigned(apk); err != nil {
			return result, err
		}
	} else {
		signer, err = CertificateDigest(signer)
		if err != nil {
			return result, err
		}
		output, err = t.command(ctx, 120*time.Second, filepath.Join(sdk, "apksigner"), "verify", "--verbose", "--print-certs", apk)
		if err != nil {
			return result, err
		}
		if err = VerifySignerOutput(output, signer); err != nil {
			return result, err
		}
		if !regexp.MustCompile(`(?m)^Verified using v2 scheme .*: true\r?$`).MatchString(output) {
			return result, errors.New("APK lacks verified v2 signature")
		}
		if err = RejectPublicLineage(apk); err != nil {
			return result, err
		}
		result.Signed = true
		result.Signer = &signer
	}
	result.Manifest, result.Assets = manifest, assets
	result.Digest, err = artifact.HashFile(apk)
	if err != nil {
		return result, errors.New("APK could not be hashed")
	}
	info, err := os.Stat(apk)
	if err != nil {
		return result, errors.New("APK size unavailable")
	}
	result.Size = info.Size()
	return result, nil
}
