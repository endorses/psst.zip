package source

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

func TestBackendProjectComparesAllProxyMembersAndExactOrigin(t *testing.T) {
	commit := strings.Repeat("b", 40)
	body := []byte("package sqlite\n")
	member := Member{Kind: "file", Path: "lib/sqlite.go", SHA256: hash(body), Size: int64(len(body))}
	project := preferredOriginal{source: Source{ID: "backend-sqlite-project", Name: "cznic/sqlite", Version: commit, Associations: map[string]string{"go:modernc.org/sqlite": "v1.37.0"}}, members: map[string]Member{member.Path: member}}
	module := preferredOriginal{source: Source{Ecosystem: "go", Name: "modernc.org/sqlite", Version: "v1.37.0", Original: File{Path: "go/sqlite.zip"}}, members: map[string]Member{member.Path: member}}
	origin := map[string]any{"Version": "v1.37.0", "Origin": map[string]string{"VCS": "git", "URL": "https://gitlab.com/cznic/sqlite", "Hash": commit, "Ref": "refs/tags/v1.37.0"}}
	raw, _ := json.Marshal(origin)
	files := map[string][]byte{"go/sqlite.info": raw}
	result, err := preferredProject(project, module, files)
	if err != nil || len(result.ProxyFiles) != 1 || result.ProxyFiles[0].SHA256 != hash(body) {
		t.Fatalf("exact proxy/project member binding failed: %v", err)
	}
	altered := member
	altered.SHA256 = hash([]byte("substituted executable source"))
	module.members[member.Path] = altered
	if _, err = preferredProject(project, module, files); err == nil {
		t.Fatal("changed proxy source member accepted")
	}
	module.members[member.Path] = member
	origin["Origin"].(map[string]string)["Ref"] = "refs/tags/v9.9.9"
	files["go/sqlite.info"], _ = json.Marshal(origin)
	if _, err = preferredProject(project, module, files); err == nil {
		t.Fatal("different proxy origin accepted")
	}
}

func TestPreferredModuleMemberRootIncludesFullModulePath(t *testing.T) {
	members := []Member{{Kind: "file", Path: "modernc.org/cc/v4@v4.26.0/lib/cc.go", SHA256: hash([]byte("source")), Size: 6}}
	raw, _ := contract.JSON(memberInventory{1, "retained-original-source-members", "modernc.org/cc/v4", "v4.26.0", members})
	f := fact("inventory/module.json", raw)
	item := Source{Ecosystem: "go", Name: "modernc.org/cc/v4", Version: "v4.26.0", MemberInventory: &f}
	result, err := preferredMembers(item, map[string][]byte{f.Path: raw})
	if err != nil || result.members["lib/cc.go"].Path != "lib/cc.go" {
		t.Fatalf("module source prefix was truncated: %v", err)
	}
}

func TestCommittedBackendRecipeIsFiniteAndHasNoReproductionClaim(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, backendRecipeCatalog))
	if err != nil {
		t.Fatal(err)
	}
	var r backendRecipe
	if err = contract.Decode(raw, MetadataLimit, &r); err != nil {
		t.Fatal(err)
	}
	if r.SchemaVersion != 1 || r.Kind != "reviewed-backend-generator-associations" || r.GeneratedOutputReproductionVerified || r.SQLite.TranslatorVersion != "v1.9.0" || len(r.SQLite.Platforms) != 2 || len(r.Libc.GeneratorTools) != 3 {
		t.Fatalf("reviewed source recipe scope changed: %+v", r)
	}
	constants, err := sqliteConstants([]byte("package sqlite3\nconst SQLITE_VERSION = \"3.49.1\"\nconst SQLITE_VERSION_NUMBER = 3049001\nconst SQLITE_SOURCE_ID = \"source\"\n"))
	if err != nil || constants["SQLITE_VERSION_NUMBER"] != "3049001" {
		t.Fatalf("SQLite constants parsed incorrectly: %v", err)
	}
	if _, err = sqliteConstants([]byte("package sqlite3\nconst SQLITE_VERSION = \"3.49.1\"\nconst SQLITE_VERSION = \"other\"\n")); err == nil {
		t.Fatal("duplicate generated version accepted")
	}
}
