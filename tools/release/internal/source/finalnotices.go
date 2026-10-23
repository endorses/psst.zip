package source

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

type BackendNoticeFacts struct {
	Files             []File `json:"files"`
	Digest            string `json:"notice_inventory_sha256"`
	DependencyNotices int    `json:"dependency_notice_count"`
}

// VerifyFinalBackendNotices consumes the already verified final filesystem and
// application snapshot. It never opens OCI archives or runs Git again.
func VerifyFinalBackendNotices(source Context, application ApplicationVerification, runtime RuntimeVerification, upstream map[string]File) (BackendNoticeFacts, error) {
	var result BackendNoticeFacts
	if application.Source != source || runtime.Inventory.Source != source {
		return result, fmt.Errorf("backend final notices belong to another source context")
	}
	if len(upstream) == 0 {
		return result, fmt.Errorf("original backend dependency notices were not replayed")
	}
	git := application.Files
	for _, name := range []string{"LICENSE", "backend/go.mod", "backend/go.sum", "backend/licenses/AGPL-3.0-only.txt", "backend/licenses/THIRD_PARTY_NOTICES.txt", "backend/licenses/dependency-inventory.json"} {
		if _, ok := git[name]; !ok {
			return result, fmt.Errorf("committed backend notice input missing")
		}
	}
	if !bytes.Equal(git["LICENSE"], git["backend/licenses/AGPL-3.0-only.txt"]) {
		return result, fmt.Errorf("application and backend license originals differ")
	}
	expected := map[string]File{}
	for name, body := range git {
		if !strings.HasPrefix(name, "backend/licenses/") {
			continue
		}
		target := "app/licenses/" + strings.TrimPrefix(name, "backend/licenses/")
		if strings.HasPrefix(target, "app/licenses/runtime/") || target == "app/licenses/SOURCE.txt" || target == "app/licenses/go/LICENSE" {
			return result, fmt.Errorf("committed backend notice shadows generated source metadata")
		}
		expected[target] = fact(target, body)
	}
	for name, want := range upstream {
		target := "app/licenses/" + name
		actual, ok := expected[target]
		if !strings.HasPrefix(name, "dependencies/") || !ok || want.Path != name || want.SHA256 != actual.SHA256 || want.Size != actual.Size {
			return result, fmt.Errorf("final backend dependency notice differs from independently replayed original")
		}
	}
	generated := []byte(fmt.Sprintf("psst.zip %s\nRevision: %s\nSource: https://github.com/%s/archive/%s.tar.gz\nLicense: AGPL-3.0-only\n", source.Version, source.Commit, source.Repository, source.Commit))
	expected["app/licenses/SOURCE.txt"] = fact("app/licenses/SOURCE.txt", generated)
	goLicense, ok := runtime.GoLicenses["backend"]
	if !ok || goLicense.Path != "app/licenses/go/LICENSE" || !digestRE.MatchString(goLicense.SHA256) || goLicense.Size <= 0 {
		return result, fmt.Errorf("actual compiler original license was not verified")
	}
	expected[goLicense.Path] = goLicense
	for _, f := range runtime.NoticeFiles["backend"] {
		if !strings.HasPrefix(f.Path, "app/licenses/runtime/") || !digestRE.MatchString(f.SHA256) {
			return result, fmt.Errorf("invalid independently verified runtime notice path")
		}
		if _, exists := expected[f.Path]; exists {
			return result, fmt.Errorf("runtime notice shadows another source file")
		}
		expected[f.Path] = f
	}
	if len(runtime.NoticeFiles["backend"]) < 3 {
		return result, fmt.Errorf("runtime source/notice offering missing")
	}
	actual := map[string]File{}
	for name, body := range runtime.Files["backend"] {
		if strings.HasPrefix(name, "app/licenses/") {
			actual[name] = fact(name, body)
		}
	}
	if len(actual) != len(expected) {
		return result, fmt.Errorf("final backend notice tree has missing or unrelated files")
	}
	names := []string{}
	for name, want := range expected {
		if actual[name] != want {
			return result, fmt.Errorf("final backend notice bytes changed or missing")
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		result.Files = append(result.Files, actual[name])
	}
	raw, err := contract.JSON(result.Files)
	if err != nil {
		return result, err
	}
	result.Digest = hash(raw)
	result.DependencyNotices = len(upstream)
	return result, nil
}
