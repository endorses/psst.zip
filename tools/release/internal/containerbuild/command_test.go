package containerbuild

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

func TestSourceCommandRetainsPlannedIdentityAndFixedWorkflowOutputs(t *testing.T) {
	b, git := gitFixture(t)
	commit := git("rev-parse", "HEAD")
	outputs := filepath.Join(t.TempDir(), "github-output")
	if err := os.WriteFile(outputs, []byte("existing=value\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	args := []string{"validate-source", "--ref", "refs/heads/main", "--event-sha", commit, "--planned-version", "v1.2.4", "--github-output", outputs}
	if err := run(context.Background(), b, args, &stdout); err != nil {
		t.Fatal(err)
	}
	var identity SourceIdentity
	if err := contract.Decode(stdout.Bytes(), maxJSON, &identity); err != nil || identity.Commit != commit || !identity.PlannedCandidateOnly || identity.PublicationAuthorized || identity.TaggedSourceCIVerified {
		t.Fatal(identity, err)
	}
	raw, err := os.ReadFile(outputs)
	if err != nil || string(raw) != "existing=value\nversion=v1.2.4\ncommit="+commit+"\nsource_kind=planned-main-dispatch\n" {
		t.Fatal(string(raw), err)
	}
	if git("tag", "--list", "v1.2.4") != "" {
		t.Fatal("source command created a tag")
	}
	for _, invalid := range [][]string{{}, {"publish"}, {"defaults", "unexpected"}, {"defaults", "--approve"}, {"validate-tag", "--ref", "refs/tags/v1.2.3", "--event-sha", commit, "--planned-version", "v1.2.4"}} {
		stdout.Reset()
		if err := run(context.Background(), b, invalid, &stdout); err == nil || stdout.Len() != 0 {
			t.Fatal("invalid command emitted candidate evidence", invalid, err)
		}
	}
}

func TestCandidateCommandUsesBoundedRegularFilesAndExclusiveRecords(t *testing.T) {
	b, _ := resolver(t, baseFixture())
	folder := t.TempDir()
	output := filepath.Join(folder, "candidate.json")
	outputs := filepath.Join(folder, "github-output")
	var stdout bytes.Buffer
	args := []string{"resolve-bases", "--version", "v1.2.3", "--commit", strings.Repeat("a", 40), "--output", output, "--github-output", outputs}
	if err := run(context.Background(), b, args, &stdout); err != nil {
		t.Fatal(err)
	}
	retained, err := os.ReadFile(output)
	if err != nil || !bytes.Equal(retained, stdout.Bytes()) {
		t.Fatal("retained record differs from command output", err)
	}
	candidate, err := DecodeCandidate(retained)
	if err != nil {
		t.Fatal(err)
	}
	workflow, err := os.ReadFile(outputs)
	if err != nil || !bytes.Contains(workflow, []byte("golang_image="+candidate.BaseImages["golang"]+"\n")) {
		t.Fatal("missing exact immutable workflow output", err)
	}
	if err := run(context.Background(), b, args, &stdout); err == nil {
		t.Fatal("overwrote retained candidate")
	}
	stdout.Reset()
	if err := run(context.Background(), b, []string{"validate-candidate", "--candidate", output}, &stdout); err != nil || !bytes.Equal(retained, stdout.Bytes()) {
		t.Fatal("candidate reader changed v1 fields", err)
	}
	linked := filepath.Join(folder, "linked.json")
	if err := os.Symlink(output, linked); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{linked, filepath.Join(folder, "absent.json")} {
		stdout.Reset()
		if err := run(context.Background(), b, []string{"validate-candidate", "--candidate", file}, &stdout); err == nil || stdout.Len() != 0 {
			t.Fatal("accepted unsafe candidate file", file, err)
		}
	}
	if err := appendOutputs(filepath.Join(folder, "unsafe-output"), map[string]string{"version": "v1.2.3\ninvented_approval=true"}); err == nil {
		t.Fatal("accepted workflow command injection")
	}
}

func TestBundleCommandBindsExpectedSourceAndEmitsArtifactFacts(t *testing.T) {
	b, git := gitFixture(t)
	commit := git("rev-parse", "HEAD")
	output := filepath.Join(t.TempDir(), "artifacts")
	args := []string{"bundle", "--version", "v1.2.3", "--output", output, "--payload-profile", "deployment-ready", "--expected-commit", commit}
	var stdout bytes.Buffer
	if err := run(context.Background(), b, args, &stdout); err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := contract.Decode(stdout.Bytes(), maxJSON, &fields); err != nil || len(fields) != 4 {
		t.Fatal("changed artifact fact schema", err)
	}
	var bundle Bundle
	if err := contract.Decode(stdout.Bytes(), maxJSON, &bundle); err != nil || bundle.SourceCommit != commit || filepath.Dir(bundle.Path) != output || bundle.Size <= 0 || len(bundle.SHA256) != 64 {
		t.Fatal(bundle, err)
	}
	stdout.Reset()
	args[len(args)-1] = strings.Repeat("b", 40)
	if err := run(context.Background(), b, args, &stdout); err == nil || stdout.Len() != 0 {
		t.Fatal("created artifact facts for another source", err)
	}
}
