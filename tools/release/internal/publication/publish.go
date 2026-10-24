package publication

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
	releasegithub "github.com/endorses/psst.zip/tools/release/internal/github"
	"github.com/endorses/psst.zip/tools/release/internal/process"
)

type imageStager func(context.Context, Inputs, string) (map[string]string, error)
type registryFactory func(Inputs, releasegithub.Client, *journal, string, map[string]string, io.Writer) (*registry, error)

func Prepare(ctx context.Context, o PublishOptions, environment map[string]string, client releasegithub.Client) error {
	if err := privateDirectory(o.State); err != nil {
		return err
	}
	inputs, err := LoadInputs(ctx, o, environment, client)
	if err != nil {
		return err
	}
	defer os.RemoveAll(inputs.Snapshot)
	if err = verifyWorkflow(ctx, client, inputs); err != nil {
		return err
	}
	if err = validateScans(ctx, o.Root, inputs, time.Now().UTC()); err != nil {
		return err
	}
	if !filepath.IsAbs(o.Checksums) {
		return fmt.Errorf("final attestation checksums require an absolute path")
	}
	keys := []string{}
	for pair := range inputs.Expected {
		keys = append(keys, pair)
	}
	sort.Strings(keys)
	var output strings.Builder
	for _, pair := range keys {
		name, hash, _ := subjectParts(pair)
		fmt.Fprintf(&output, "%s  %s\n", strings.TrimPrefix(hash, "sha256:"), name)
	}
	file, err := os.OpenFile(o.Checksums, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("final subject checksum input already exists or unavailable")
	}
	_, writeErr := io.WriteString(file, output.String())
	syncErr, closeErr := file.Sync(), file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return fmt.Errorf("final subject checksum input could not be durably written")
	}
	return nil
}

// executePublication has explicit fixture adapters so one bounded rehearsal can
// exercise failure boundaries without contacting a registry or publishing.
func executePublication(ctx context.Context, o PublishOptions, environment map[string]string, client, inspection releasegithub.Client, stage imageStager, makeRegistry registryFactory, public *publicReader, out io.Writer) error {
	progress := func(label string) { fmt.Fprintln(out, "Publication stage: "+label) }
	progress("workflow-inputs")
	if runtime.GOOS != "linux" {
		return fmt.Errorf("container publication requires its trusted hosted Linux runner")
	}
	if err := privateDirectory(o.State); err != nil {
		return err
	}
	for _, name := range []string{"GH_TOKEN", "PSST_IMMUTABLE_INSPECTION_TOKEN"} {
		if environment[name] == "" || strings.ContainsAny(environment[name], "\r\n") {
			return fmt.Errorf("explicit hosted publication and policy inspection credentials required")
		}
	}
	binding, bindingErr := workflowBinding(environment, o.ReviewedCommit)
	if bindingErr != nil {
		return bindingErr
	}
	for _, suffix := range []string{".jsonl", "-partial-publication.json", "-publication-receipt.json"} {
		_, err := os.Lstat(filepath.Join(o.State, binding.Version+suffix))
		if err == nil || !os.IsNotExist(err) {
			return fmt.Errorf("publication state already exists or cannot be inspected; reconcile instead of replaying")
		}
	}
	progress("snapshot-inputs")
	inputs, err := LoadInputs(ctx, o, environment, client)
	if err != nil {
		return err
	}
	retained := false
	defer func() {
		if !retained {
			_ = os.RemoveAll(inputs.Snapshot)
		}
	}()
	progress("workflow-api-preflight")
	if err = verifyWorkflow(ctx, client, inputs); err != nil {
		return err
	}
	progress("scanner-policy")
	if err = validateScans(ctx, o.Root, inputs, time.Now().UTC()); err != nil {
		return err
	}
	// Final standard provenance must cover every exact file/index/child pair in
	// one verified statement before the first external mutation.
	progress("provenance-publication")
	manifest := filepath.Join(inputs.Snapshot, "release-manifest.json")
	if err = client.VerifyFile(ctx, manifest, inputs.Binding, inputs.Expected); err != nil {
		return err
	}
	if err = client.RequireUnused(ctx, inputs.Binding.Repository, inputs.Binding.Version); err != nil {
		return err
	}
	var immutable struct{ Enabled bool }
	if err = inspection.API(ctx, "GET", "repos/"+inputs.Binding.Repository+"/immutable-releases", &immutable); err != nil {
		return err
	}
	if !immutable.Enabled {
		return fmt.Errorf("repository immutable releases are not enabled")
	}
	adapter, err := makeRegistry(inputs, client, nil, o.State, environment, out)
	if err != nil {
		return err
	}
	defer adapter.close()
	progress("package-preflight")
	if err = adapter.preflight(ctx); err != nil {
		return err
	}
	if stage == nil {
		return fmt.Errorf("validated OCI image staging is required")
	}
	progress("final-oci")
	layouts, err := stage(ctx, inputs, inputs.Snapshot)
	if err != nil {
		return err
	}
	if len(layouts) != 2 || layouts["backend"] == "" || layouts["web"] == "" {
		return fmt.Errorf("both verified image index layouts are required")
	}
	progress("snapshot-durability")
	if err = syncSnapshot(inputs.Snapshot); err != nil {
		return err
	}
	if err = syncDirectory(o.State); err != nil {
		return err
	}
	progress("publication-lease")
	j, err := newJournal(filepath.Join(o.State, inputs.Binding.Version+".jsonl"), inputs)
	if err != nil {
		return err
	}
	success := false
	defer func() {
		if !success {
			_ = j.append("stopped", "transaction", map[string]any{"automatic_resume": false})
		}
		_ = j.file.Close()
	}()
	adapter.journal = j
	retained = true
	plan := releasegithub.Publication{
		Repository: inputs.Binding.Repository, Tag: inputs.Binding.Version, Revision: inputs.Binding.Commit, Title: "psst.zip " + inputs.Binding.Version,
		Notes:   fmt.Sprintf("psst.zip %s\n\nReviewed source: %s\n\nBackend and web images are paired by release-manifest.json. Verify the immutable manifest and deployment bundle before updating. Corresponding source and notices are retained in the attached artifacts.\n", inputs.Binding.Version, inputs.Binding.Commit),
		Receipt: filepath.Join(o.State, inputs.Binding.Version+"-partial-publication.json"), Assets: inputs.Assets, Inspector: &inspection,
		TransferTimeout: 20 * time.Minute,
	}
	plan.OnPhase = func(phase string, releaseID int64) error {
		switch phase {
		case "reserve-draft-intent":
			return j.append("intent", "reserve-draft", map[string]any{})
		case "draft-readback":
			return j.append("complete", "reserve-draft", map[string]any{"release_id": releaseID})
		case "publish-release-intent":
			progress("immutable-publication")
			if err := validateScans(ctx, o.Root, inputs, time.Now().UTC()); err != nil {
				return err
			}
			if err := verifyWorkflow(ctx, client, inputs); err != nil {
				return err
			}
			return j.append("intent", "publish-release", map[string]any{"release_id": releaseID})
		case "public-readback-complete":
			return j.append("complete", "public-readback", map[string]any{"release_id": releaseID})
		}
		return nil
	}
	plan.BeforePublish = func(ctx context.Context) error {
		progress("registry-publication")
		if err := adapter.push(ctx, layouts); err != nil {
			return err
		}
		progress("anonymous-pull")
		if err := adapter.anonymousPull(ctx, o.State); err != nil {
			return err
		}
		if err := validateScans(ctx, o.Root, inputs, time.Now().UTC()); err != nil {
			return err
		}
		progress("version-tags")
		return adapter.tagVersions(ctx)
	}
	plan.AfterPublish = func(ctx context.Context, release releasegithub.Release) error {
		progress("public-readback")
		if _, err := adapter.packages(ctx, false, false); err != nil {
			return err
		}
		if err := adapter.inspectPair(ctx, true, true); err != nil {
			return err
		}
		return public.verify(ctx, inputs.Binding.Repository, inputs.Binding.Version, release.ID, inputs.Assets)
	}
	receipt, err := client.PublishNew(ctx, plan)
	if err != nil {
		return err
	}
	journalRaw, err := read(filepath.Join(o.State, inputs.Binding.Version+".jsonl"))
	if err != nil {
		return fmt.Errorf("publication journal could not be checked")
	}
	final := map[string]any{"schema_version": 1, "kind": "container-publication-receipt", "repository": inputs.Binding.Repository, "version": inputs.Binding.Version, "commit": inputs.Binding.Commit, "binding_digest": inputs.Result.Binding, "release_id": receipt.ReleaseID, "immutable": true, "journal_sha256": digest(journalRaw), "public_readback": map[string]any{"release": true, "assets": true, "registry": true, "complete_child_pulls": []string{"backend-amd64", "backend-arm64", "web-amd64", "web-arm64"}, "credentials_used": false}}
	data, err := contract.JSON(final)
	if err != nil {
		return err
	}
	name := filepath.Join(o.State, inputs.Binding.Version+"-publication-receipt.json")
	file, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("successful receipt already exists; reconcile publication")
	}
	_, writeErr := file.Write(data)
	syncErr, closeErr := file.Sync(), file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil || syncDirectory(o.State) != nil {
		return fmt.Errorf("successful publication receipt could not be durably retained")
	}
	success, retained = true, false
	fmt.Fprintln(out, "Published immutable release "+inputs.Binding.Version+"; verified anonymous readback receipt retained.")
	return nil
}

func syncSnapshot(root string) error {
	var directories []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("publication snapshot unavailable")
		}
		if entry.IsDir() {
			directories = append(directories, path)
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("publication snapshot contains unsafe filesystem objects")
		}
		f, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("publication snapshot could not be opened for sync")
		}
		syncErr, closeErr := f.Sync(), f.Close()
		if syncErr != nil || closeErr != nil {
			return fmt.Errorf("publication snapshot sync failed")
		}
		return nil
	})
	if err != nil {
		return err
	}
	for i := len(directories) - 1; i >= 0; i-- {
		if err = syncDirectory(directories[i]); err != nil {
			return err
		}
	}
	return nil
}

func inspectionClient(root, token string) releasegithub.Client {
	return releasegithub.Client{Runner: process.Runner{Dir: root, Env: map[string]string{"GH_TOKEN": token}}}
}
