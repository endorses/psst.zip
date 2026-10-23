package github

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func verifiedFixture(t *testing.T, b Binding, subjects []map[string]any) []byte {
	t.Helper()
	repository, ref := "https://github.com/"+b.Repository, "refs/tags/"+b.Version
	data, err := json.Marshal([]any{map[string]any{"verificationResult": map[string]any{
		"signature": map[string]any{"certificate": map[string]string{"identity": "verified by gh"}}, "verifiedTimestamps": []any{map[string]string{"type": "tlog"}},
		"statement": map[string]any{"_type": "https://in-toto.io/Statement/v1", "predicateType": Predicate, "subject": subjects,
			"predicate": map[string]any{
				"buildDefinition": map[string]any{"buildType": "https://actions.github.io/buildtypes/workflow/v1", "externalParameters": map[string]any{"workflow": map[string]string{"repository": repository, "ref": ref, "path": ReleaseWorkflow}}, "internalParameters": map[string]any{"github": map[string]string{"event_name": "push", "runner_environment": "github-hosted"}}, "resolvedDependencies": []any{map[string]any{"uri": "git+" + repository + "@" + ref, "digest": map[string]string{"gitCommit": b.Commit}}}},
				"runDetails":      map[string]any{"builder": map[string]string{"id": repository + "/" + ReleaseWorkflow + "@" + ref}, "metadata": map[string]string{"invocationId": fmtInvocation(repository, b.RunID, b.Attempt)}},
			},
		},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func TestVerifiedEvidenceRequiresExactHostedAttemptAndCompleteOneStatement(t *testing.T) {
	b := Binding{"endorses/psst.zip", "v1.2.3", strings.Repeat("a", 40), 12, 2}
	hash := strings.Repeat("b", 64)
	subjects := []map[string]any{{"name": "release-manifest.json", "digest": map[string]string{"sha256": hash}}, {"name": "ghcr.io/endorses/psst-zip-web", "digest": map[string]string{"sha256": strings.Repeat("c", 64)}}}
	expected := map[string]string{"release-manifest.json@sha256:" + hash: hash, "ghcr.io/endorses/psst-zip-web@sha256:" + strings.Repeat("c", 64): strings.Repeat("c", 64)}
	data := verifiedFixture(t, b, subjects)
	if err := VerifiedSubjects(data, "sha256:"+hash, b, expected); err != nil {
		t.Fatal(err)
	}
	wrong := b
	wrong.Attempt++
	if VerifiedSubjects(data, "sha256:"+hash, wrong, expected) == nil {
		t.Fatal("cross-attempt evidence accepted")
	}
	delete(expected, "ghcr.io/endorses/psst-zip-web@sha256:"+strings.Repeat("c", 64))
	if VerifiedSubjects(data, "sha256:"+hash, b, expected) == nil {
		t.Fatal("extra final subject accepted")
	}
	if VerifiedSubjects([]byte(`[]`), "sha256:"+hash, b, nil) == nil {
		t.Fatal("empty successful verifier output accepted")
	}
	duplicate := append(subjects, subjects[0])
	if VerifiedSubjects(verifiedFixture(t, b, duplicate), "sha256:"+hash, b, nil) == nil {
		t.Fatal("duplicate subject accepted")
	}
}
func TestVerifierPinsCertificateFlagsAndDetectsSnapshotReplacement(t *testing.T) {
	b := Binding{"endorses/psst.zip", "v1.2.3", strings.Repeat("a", 40), 12, 2}
	path := filepath.Join(t.TempDir(), "result.json")
	content := []byte(`{"private":"disposable report bytes"}`)
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	hash := hex.EncodeToString(sum[:])
	replace := false
	client := Client{executor(func(_ context.Context, _ time.Duration, args ...string) ([]byte, error) {
		for _, flag := range []string{"--signer-digest", "--source-digest", "--source-ref", "--cert-identity", "--cert-oidc-issuer", "--deny-self-hosted-runners", "--predicate-type"} {
			if !slices.Contains(args, flag) {
				t.Fatalf("missing cryptographic policy %s", flag)
			}
		}
		if args[3] == path {
			t.Fatal("verifier consumed mutable caller path")
		}
		if replace {
			_ = os.Chmod(args[3], 0600)
			_ = os.WriteFile(args[3], []byte("substituted"), 0600)
		}
		return verifiedFixture(t, b, []map[string]any{{"name": "result.json", "digest": map[string]string{"sha256": hash}}}), nil
	})}
	if err := client.VerifyFile(context.Background(), path, b, nil); err != nil {
		t.Fatal(err)
	}
	replace = true
	if client.VerifyFile(context.Background(), path, b, nil) == nil {
		t.Fatal("snapshot replacement accepted")
	}
}
func TestAnnotatedTagEventUsesExpandedHeadCommit(t *testing.T) {
	b := Binding{Repository: "endorses/psst.zip", Version: "v1.2.3", Commit: strings.Repeat("a", 40)}
	data := []byte(`{"repository":{"visibility":"public","full_name":"endorses/psst.zip"},"ref":"refs/tags/v1.2.3","after":"` + strings.Repeat("b", 40) + `","head_commit":{"id":"` + b.Commit + `"}}`)
	if err := ValidateTagPush(data, b); err != nil {
		t.Fatal(err)
	}
	b.Commit = strings.Repeat("c", 40)
	if ValidateTagPush(data, b) == nil {
		t.Fatal("foreign expanded tag commit accepted")
	}
}
