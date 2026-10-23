package source

import (
	"fmt"
	"strings"
	"testing"
)

func TestFinalBackendNoticesReuseVerifiedSnapshotsAndRejectSubstitution(t *testing.T) {
	identity := Context{"endorses/psst.zip", "v1.2.3", strings.Repeat("a", 40)}
	source := []byte("application license\n")
	dependency := []byte("retained normalized upstream notice\n")
	application := ApplicationVerification{Source: identity, Files: map[string][]byte{
		"LICENSE": source, "backend/go.mod": []byte("module fixture\n"), "backend/go.sum": {},
		"backend/licenses/AGPL-3.0-only.txt":               source,
		"backend/licenses/THIRD_PARTY_NOTICES.txt":         []byte("original attribution\n"),
		"backend/licenses/dependency-inventory.json":       []byte("fixture inventory\n"),
		"backend/licenses/dependencies/fixture@v1/LICENSE": dependency,
	}}
	actual := map[string][]byte{}
	for name, body := range application.Files {
		if strings.HasPrefix(name, "backend/licenses/") {
			actual["app/licenses/"+strings.TrimPrefix(name, "backend/licenses/")] = body
		}
	}
	actual["app/licenses/SOURCE.txt"] = []byte(fmt.Sprintf("psst.zip %s\nRevision: %s\nSource: https://github.com/%s/archive/%s.tar.gz\nLicense: AGPL-3.0-only\n", identity.Version, identity.Commit, identity.Repository, identity.Commit))
	actual["app/licenses/go/LICENSE"] = []byte("actual compiler original license\n")
	runtime := RuntimeVerification{Inventory: newInventory(identity), Files: map[string]map[string][]byte{"backend": actual}, NoticeFiles: map[string][]File{}, GoLicenses: map[string]File{"backend": fact("app/licenses/go/LICENSE", actual["app/licenses/go/LICENSE"])}}
	for _, name := range []string{"THIRD_PARTY_NOTICES.txt", "runtime-inventory.json", "SOURCE.txt"} {
		name = "app/licenses/runtime/" + name
		actual[name] = []byte("independently checked source/runtime bytes for " + name)
		runtime.NoticeFiles["backend"] = append(runtime.NoticeFiles["backend"], fact(name, actual[name]))
	}
	upstream := map[string]File{"dependencies/fixture@v1/LICENSE": fact("dependencies/fixture@v1/LICENSE", dependency)}
	result, err := VerifyFinalBackendNotices(identity, application, runtime, upstream)
	if err != nil || len(result.Files) != len(actual) || result.DependencyNotices != 1 || !digestRE.MatchString(result.Digest) {
		t.Fatalf("exact source and image notices were not bound: %v", err)
	}
	name := "app/licenses/dependencies/fixture@v1/LICENSE"
	original := actual[name]
	actual[name] = []byte("changed legal terms")
	if _, err = VerifyFinalBackendNotices(identity, application, runtime, upstream); err == nil {
		t.Fatal("changed distributed legal bytes accepted")
	}
	actual[name] = original
	actual["app/licenses/unrelated.txt"] = []byte("unexpected")
	if _, err = VerifyFinalBackendNotices(identity, application, runtime, upstream); err == nil {
		t.Fatal("unrelated notice file accepted")
	}
	delete(actual, "app/licenses/unrelated.txt")
	delete(actual, "app/licenses/SOURCE.txt")
	if _, err = VerifyFinalBackendNotices(identity, application, runtime, upstream); err == nil {
		t.Fatal("missing source offering accepted")
	}
}
