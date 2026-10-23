package github

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

const MaxAsset = 2 << 30

var assetName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,199}$`)

type Asset struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Path   string `json:"-"`
}

// SnapshotAssets freezes regular files once before any external mutation. The
// caller must remove the returned private directory after the transaction.
func SnapshotAssets(directory string, names []string) (string, []Asset, error) {
	return SnapshotAssetsAt(directory, names, "")
}
func SnapshotAssetsAt(directory string, names []string, parent string) (string, []Asset, error) {
	private, err := os.MkdirTemp(parent, "psst-release-assets-")
	if err != nil {
		return "", nil, errors.New("private publication snapshot unavailable")
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(private)
		}
	}()
	seen := map[string]bool{}
	assets := []Asset{}
	for _, name := range names {
		if !assetName.MatchString(name) || seen[name] {
			return "", nil, errors.New("invalid or duplicate publication asset name")
		}
		seen[name] = true
		path := filepath.Join(directory, name)
		before, err := os.Lstat(path)
		if err != nil || !before.Mode().IsRegular() || before.Size() < 1 || before.Size() > MaxAsset {
			return "", nil, errors.New("publication input is not a bounded regular file")
		}
		input, err := os.Open(path)
		if err != nil {
			return "", nil, errors.New("publication input could not be opened")
		}
		opened, err := input.Stat()
		if err != nil || !os.SameFile(before, opened) {
			_ = input.Close()
			return "", nil, errors.New("publication input changed while opening")
		}
		destination := filepath.Join(private, name)
		output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			_ = input.Close()
			return "", nil, errors.New("private publication snapshot could not be created")
		}
		hash := sha256.New()
		size, copyErr := io.Copy(io.MultiWriter(output, hash), io.LimitReader(input, MaxAsset+1))
		after, statErr := input.Stat()
		inputErr := input.Close()
		syncErr, closeErr := output.Sync(), output.Close()
		if copyErr != nil || statErr != nil || inputErr != nil || syncErr != nil || closeErr != nil || size != before.Size() || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
			return "", nil, errors.New("publication input changed while snapshotting")
		}
		assets = append(assets, Asset{Name: name, SHA256: hex.EncodeToString(hash.Sum(nil)), Size: size, Path: destination})
	}
	if len(assets) < 1 || len(assets) > 128 {
		return "", nil, errors.New("publication asset count exceeds bounds")
	}
	sort.Slice(assets, func(i, j int) bool { return assets[i].Name < assets[j].Name })
	ok = true
	return private, assets, nil
}

type ReleaseAsset struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
	State  string `json:"state"`
}
type Release struct {
	ID        int64          `json:"id"`
	Tag       string         `json:"tag_name"`
	Draft     bool           `json:"draft"`
	Immutable bool           `json:"immutable"`
	Assets    []ReleaseAsset `json:"assets"`
}
type Publication struct {
	Repository, Tag, Revision, Title, Notes, Receipt string
	Assets                                           []Asset
	// Optional least-privilege Administration:read adapter. Container publication
	// supplies its protected inspection token; ordinary CLI jobs have no such
	// token and retain their existing final immutable-flag requirement.
	Inspector *Client
	// Container publication supplies digest push/readback and anonymous final
	// checks here; CLI delivery needs only the shared exact-asset transaction.
	BeforePublish func(context.Context) error
	AfterPublish  func(context.Context, Release) error
	OnPhase       func(string, int64) error
	// The container job retains its existing bounded large-source transfer budget;
	// small CLI releases use the shorter defaults and their own workflow deadline.
	TransferTimeout time.Duration
}
type Receipt struct {
	Schema     int     `json:"schema_version"`
	Kind       string  `json:"kind"`
	Repository string  `json:"repository"`
	Tag        string  `json:"tag"`
	Revision   string  `json:"revision"`
	ReleaseID  int64   `json:"release_id"`
	Phase      string  `json:"phase"`
	Complete   bool    `json:"complete"`
	Assets     []Asset `json:"assets"`
}

func writeReceipt(path string, receipt Receipt, initial bool) error {
	data, err := contract.JSON(receipt)
	if err != nil || path == "" {
		return errors.New("publication requires an explicit partial receipt")
	}
	if initial {
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return errors.New("publication receipt already exists; reconcile instead of replaying")
		}
		_, writeErr := file.Write(data)
		syncErr, closeErr := file.Sync(), file.Close()
		if writeErr != nil || syncErr != nil || closeErr != nil {
			return errors.New("publication receipt could not be durably recorded")
		}
		return nil
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".psst-receipt-")
	if err != nil {
		return errors.New("publication receipt update unavailable")
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	_, writeErr := file.Write(data)
	syncErr, closeErr := file.Sync(), file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil || os.Rename(temporary, path) != nil {
		return errors.New("publication receipt could not be durably updated")
	}
	return nil
}

func (c Client) ReleaseByTag(ctx context.Context, repository, tag string) (Release, error) {
	var release Release
	if !RepositoryPattern.MatchString(repository) || !ReleaseTagPattern.MatchString(tag) {
		return release, errors.New("invalid release readback identity")
	}
	err := c.API(ctx, "GET", "repos/"+repository+"/releases/tags/"+url.PathEscape(tag), &release)
	return release, err
}
func (c Client) RequireUnused(ctx context.Context, repository, tag string) error {
	if !RepositoryPattern.MatchString(repository) || !ReleaseTagPattern.MatchString(tag) {
		return errors.New("invalid new release identity")
	}
	for page := 1; page <= 100; page++ {
		var releases []Release
		if err := c.API(ctx, "GET", fmt.Sprintf("repos/%s/releases?per_page=100&page=%d", repository, page), &releases); err != nil {
			return err
		}
		if len(releases) > 100 {
			return errors.New("release listing exceeded inspection bound")
		}
		for _, release := range releases {
			if release.Tag == tag {
				return errors.New("this tag already has a release; drafts and published releases are never replaced")
			}
		}
		if len(releases) < 100 {
			return nil
		}
	}
	return errors.New("release listing exceeded inspection bound")
}
func MatchAssets(release Release, expected []Asset) bool {
	if release.ID < 1 || len(release.Assets) != len(expected) {
		return false
	}
	seen := map[string]bool{}
	for _, asset := range release.Assets {
		if asset.ID < 1 || asset.State != "uploaded" || seen[asset.Name] {
			return false
		}
		seen[asset.Name] = true
		found := false
		for _, want := range expected {
			if asset.Name == want.Name && asset.Size == want.Size && asset.Digest == "sha256:"+want.SHA256 {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// PublishNew uses supported gh operations, one attempt only. The release remains
// draft until every uploaded asset is downloaded and compared. On uncertainty,
// the receipt records the attempted mutation; a future run may not reuse it.
func (c Client) PublishNew(ctx context.Context, plan Publication) (Receipt, error) {
	receipt := Receipt{1, "release-publication-receipt", plan.Repository, plan.Tag, plan.Revision, 0, "preflight", false, plan.Assets}
	if !RepositoryPattern.MatchString(plan.Repository) || !ReleaseTagPattern.MatchString(plan.Tag) || !CommitPattern.MatchString(plan.Revision) || len(plan.Assets) < 1 || len(plan.Assets) > 128 || len(plan.Notes) > 64<<10 || len(plan.Title) > 256 {
		return receipt, errors.New("invalid new release publication plan")
	}
	uploadTimeout, downloadTimeout := 90*time.Second, 60*time.Second
	if plan.TransferTimeout != 0 {
		if plan.TransferTimeout < time.Second || plan.TransferTimeout > 20*time.Minute {
			return receipt, errors.New("invalid bounded publication transfer budget")
		}
		uploadTimeout, downloadTimeout = plan.TransferTimeout, plan.TransferTimeout
	}
	if err := writeReceipt(plan.Receipt, receipt, true); err != nil {
		return receipt, err
	}
	advance := func(phase string) error {
		receipt.Phase = phase
		if err := writeReceipt(plan.Receipt, receipt, false); err != nil {
			return err
		}
		if plan.OnPhase != nil {
			return plan.OnPhase(phase, receipt.ReleaseID)
		}
		return nil
	}
	if err := c.RequireUnused(ctx, plan.Repository, plan.Tag); err != nil {
		return receipt, err
	}
	if plan.Inspector != nil {
		var immutable struct{ Enabled bool }
		if err := plan.Inspector.API(ctx, "GET", "repos/"+plan.Repository+"/immutable-releases", &immutable); err != nil {
			return receipt, err
		}
		if !immutable.Enabled {
			return receipt, errors.New("repository immutable releases must be enabled before publication")
		}
	}
	checkTag := func() error {
		resolved, err := c.ResolveTag(ctx, plan.Repository, plan.Tag)
		if err != nil || resolved != plan.Revision {
			return errors.New("selected tag differs from reviewed publication source")
		}
		return nil
	}
	if err := checkTag(); err != nil {
		return receipt, err
	}
	private, err := os.MkdirTemp("", "psst-release-readback-")
	if err != nil {
		return receipt, errors.New("private publication readback unavailable")
	}
	defer os.RemoveAll(private)
	notes := filepath.Join(private, "notes.md")
	if err = os.WriteFile(notes, []byte(plan.Notes), 0600); err != nil {
		return receipt, errors.New("publication notes unavailable")
	}
	args := []string{"gh", "release", "create", plan.Tag}
	for _, asset := range plan.Assets {
		args = append(args, asset.Path)
	}
	args = append(args, "--repo", "github.com/"+plan.Repository, "--verify-tag", "--draft", "--latest=false", "--title", plan.Title, "--notes-file", notes)
	if err = advance("reserve-draft-intent"); err != nil {
		return receipt, err
	}
	if _, err = c.Command(ctx, uploadTimeout, args...); err != nil {
		return receipt, errors.New("draft creation or upload was interrupted; reconcile the partial receipt, do not overwrite or replay")
	}
	release, err := c.ReleaseByTag(ctx, plan.Repository, plan.Tag)
	if err != nil || release.Tag != plan.Tag || !release.Draft || !MatchAssets(release, plan.Assets) {
		return receipt, errors.New("draft asset identity readback failed; release remains incomplete")
	}
	receipt.ReleaseID = release.ID
	if err = advance("draft-readback"); err != nil {
		return receipt, err
	}
	for _, asset := range plan.Assets {
		directory := filepath.Join(private, "asset-"+asset.Name)
		if err = os.Mkdir(directory, 0700); err != nil {
			return receipt, errors.New("private asset readback unavailable")
		}
		if _, err = c.Command(ctx, downloadTimeout, "gh", "release", "download", plan.Tag, "--repo", "github.com/"+plan.Repository, "--dir", directory, "--pattern", asset.Name); err != nil {
			return receipt, errors.New("draft asset download failed; release remains incomplete")
		}
		_, copied, err := SnapshotAssets(directory, []string{asset.Name})
		if err != nil {
			return receipt, err
		}
		copyDir := filepath.Dir(copied[0].Path)
		matches := copied[0].SHA256 == asset.SHA256 && copied[0].Size == asset.Size
		_ = os.RemoveAll(copyDir)
		if !matches {
			return receipt, errors.New("draft asset bytes differ; release remains incomplete")
		}
	}
	if plan.BeforePublish != nil {
		if err = advance("pre-publication-readbacks"); err != nil {
			return receipt, err
		}
		if err = plan.BeforePublish(ctx); err != nil {
			return receipt, err
		}
	}
	if err = checkTag(); err != nil {
		return receipt, err
	}
	if err = advance("publish-release-intent"); err != nil {
		return receipt, err
	}
	if _, err = c.Command(ctx, 45*time.Second, "gh", "release", "edit", plan.Tag, "--repo", "github.com/"+plan.Repository, "--draft=false", "--latest=false"); err != nil {
		return receipt, errors.New("publication result is uncertain; reconcile the partial receipt, do not replay")
	}
	final, err := c.ReleaseByTag(ctx, plan.Repository, plan.Tag)
	if err != nil || final.ID != release.ID || final.Tag != plan.Tag || final.Draft || !final.Immutable || !MatchAssets(final, plan.Assets) {
		return receipt, errors.New("published release identity, asset set or immutability readback failed; reconcile the partial receipt")
	}
	if err = checkTag(); err != nil {
		return receipt, err
	}
	if plan.AfterPublish != nil {
		if err = plan.AfterPublish(ctx, final); err != nil {
			return receipt, err
		}
	}
	receipt.Complete = true
	err = advance("public-readback-complete")
	return receipt, err
}

func boundedFile(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > limit {
		return nil, errors.New("release contract is not a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("release contract unavailable")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("release contract changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) != info.Size() || int64(len(data)) > limit {
		return nil, errors.New("release contract exceeds its read bound")
	}
	return data, nil
}

func CLIAssets(directory, version, revision string) (string, []Asset, error) {
	if !CLITagPattern.MatchString("client/v"+version) || !CommitPattern.MatchString(revision) {
		return "", nil, errors.New("invalid CLI artifact identity")
	}
	names := []string{"cli-release.json", "SHA256SUMS", "psst-" + version + "-source.tar.gz"}
	for _, system := range []string{"linux", "darwin", "windows"} {
		for _, arch := range []string{"amd64", "arm64"} {
			extension := ".tar.gz"
			if system == "windows" {
				extension = ".zip"
			}
			names = append(names, "psst-"+version+"-"+system+"-"+arch+extension)
		}
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != len(names) {
		return "", nil, errors.New("CLI candidate must contain exactly its release assets and checksums")
	}
	private, assets, err := SnapshotAssets(directory, names)
	if err != nil {
		return "", nil, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(private)
		}
	}()
	data, err := boundedFile(filepath.Join(private, "cli-release.json"), MaxJSON)
	if err != nil {
		return "", nil, err
	}
	var record struct {
		Schema      int    `json:"schema_version"`
		Version     string `json:"version"`
		Tag         string `json:"tag"`
		Revision    string `json:"revision"`
		Development *bool  `json:"development_only"`
		Dirty       *bool  `json:"source_dirty"`
	}
	if err = DecodeAPI(data, &record); err != nil || record.Schema != 1 || record.Version != version || record.Revision != revision || record.Tag != "client/v"+version || record.Development == nil || *record.Development || record.Dirty == nil || *record.Dirty {
		return "", nil, errors.New("CLI candidate identity or clean-source verification failed")
	}
	data, err = boundedFile(filepath.Join(private, "SHA256SUMS"), 64<<10)
	if err != nil {
		return "", nil, err
	}
	expected := map[string]string{}
	for _, asset := range assets {
		if asset.Name != "SHA256SUMS" {
			expected[asset.Name] = asset.SHA256
		}
	}
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		hash, name, valid := strings.Cut(line, "  ")
		if !valid || hash != expected[name] || expected[name] == "" {
			return "", nil, errors.New("CLI checksum set differs from exact candidate assets")
		}
		delete(expected, name)
	}
	if len(expected) != 0 {
		return "", nil, errors.New("CLI checksum set is incomplete")
	}
	ok = true
	return private, assets, nil
}
