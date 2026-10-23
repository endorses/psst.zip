package source

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestApplicationOfferingUsesExactGitObjectsAndIgnoresUntrackedFiles(t *testing.T) {
	root, commit := gitFixture(t, map[string][]byte{"README.md": []byte("committed source\n"), "backend/licenses/LICENSE": []byte("terms\n")})
	source := Context{"endorses/psst.zip", "v1.2.3", commit}
	name := filepath.Join(t.TempDir(), "psst.zip-source-v1.2.3.tar.gz")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("working change\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "untracked-private"), []byte("private fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := CreateApplication(context.Background(), root, source, name)
	if err != nil {
		t.Fatal(err)
	}
	if string(result.Files["README.md"]) != "committed source\n" || result.Files["untracked-private"] != nil {
		t.Fatal("worktree files entered committed source offering")
	}
	checked, err := ReadApplication(context.Background(), root, source, name)
	if err != nil {
		t.Fatal(err)
	}
	if checked.Asset != result.Asset || checked.PrefixedGitArchiveSHA256 != result.PrefixedGitArchiveSHA256 {
		t.Fatal("source verification changed exact artifact identity")
	}
	raw, _ := os.ReadFile(name)
	if err = os.WriteFile(name, append(raw, raw...), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = ReadApplication(context.Background(), root, source, name); err == nil {
		t.Fatal("concatenated application source accepted")
	}
	if _, err = CreateApplication(context.Background(), root, source, name); err == nil {
		t.Fatal("existing application source overwritten")
	}
}
