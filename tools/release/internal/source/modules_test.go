package source

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

func moduleOriginal(t *testing.T) (moduleRecord, []byte, []byte, []byte, map[string]string) {
	t.Helper()
	module, version := "example.invalid/fixture", "v1.2.3"
	mod := []byte("module " + module + "\n\ngo 1.23.0\n")
	info := []byte(`{"Version":"v1.2.3"}`)
	files := map[string][]byte{module + "@" + version + "/LICENSE": []byte("Disposable source terms\n"), module + "@" + version + "/go.mod": mod, module + "@" + version + "/source.go": []byte("package fixture\n")}
	var raw bytes.Buffer
	writer := zip.NewWriter(&raw)
	sums := map[string]string{}
	for _, name := range []string{module + "@" + version + "/LICENSE", module + "@" + version + "/go.mod", module + "@" + version + "/source.go"} {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		entry.Write(files[name])
		sum := sha256.Sum256(files[name])
		sums[name] = hex.EncodeToString(sum[:])
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	zipSum := h1(sums)
	modSHA := sha256.Sum256(mod)
	modSum := h1(map[string]string{"go.mod": hex.EncodeToString(modSHA[:])})
	record := moduleRecord{Path: module, Version: version, Zip: "/reports/cache/download/fixture.zip", GoMod: "/reports/cache/download/fixture.mod", Info: "/reports/cache/download/fixture.info", Sum: zipSum, GoModSum: modSum}
	return record, raw.Bytes(), mod, info, map[string]string{module + "@" + version: zipSum, module + "@" + version + "/go.mod": modSum}
}
func TestPlatformModulesRetainPubliclyAuthenticatedOriginalsWithoutNpmDuplication(t *testing.T) {
	record, archive, mod, info, sums := moduleOriginal(t)
	sumFile := []byte(record.Path + " " + record.Version + " " + sums[record.Path+"@"+record.Version] + "\n" + record.Path + " " + record.Version + "/go.mod " + sums[record.Path+"@"+record.Version+"/go.mod"] + "\n")
	root, commit := gitFixture(t, map[string][]byte{"backend/go.mod": []byte("module example.invalid/application\n\ngo 1.23.0\n\nrequire " + record.Path + " " + record.Version + "\n"), "backend/go.sum": sumFile})
	config := "sha256:" + strings.Repeat("c", 64)
	o := ModuleOptions{Root: root, Source: Context{"endorses/psst.zip", "v1.2.3", commit}, Output: filepath.Join(t.TempDir(), "collected"), GoImage: "docker.io/library/golang@sha256:" + strings.Repeat("d", 64), Platform: "linux/amd64"}
	o.Execute = func(ctx context.Context, timeout time.Duration, args ...string) ([]byte, error) {
		if strings.Join(args[:3], " ") == "docker image inspect" {
			return []byte(fmt.Sprintf(`[{"Id":%q,"Os":"linux","Architecture":"amd64"}]`, config)), nil
		}
		if len(args) < 4 || args[0] != "docker" || args[1] != "run" {
			t.Fatal("unexpected collector command")
		}
		reports := ""
		for _, a := range args {
			if strings.HasPrefix(a, "type=bind,src=") && strings.HasSuffix(a, ",dst=/reports") {
				reports = strings.TrimSuffix(strings.TrimPrefix(a, "type=bind,src="), ",dst=/reports")
			}
		}
		if reports == "" {
			t.Fatal("private report mount missing")
		}
		if err := os.MkdirAll(filepath.Join(reports, "cache/download"), 0700); err != nil {
			t.Fatal(err)
		}
		downloads, _ := json.Marshal(record)
		graph := append([]byte(`{"Path":"example.invalid/application","Main":true}`+"\n"), downloads...)
		env, _ := contract.JSON(publicGoEnvironment)
		files := map[string][]byte{"go-downloads.json": downloads, "go-modules.json": graph, "go-version.txt": []byte("go version go1.27.3 linux/amd64\n"), "go-environment.json": env, "authenticated-go.sum": sumFile, "cache/download/fixture.zip": archive, "cache/download/fixture.mod": mod, "cache/download/fixture.info": info}
		for name, raw := range files {
			if err := os.WriteFile(filepath.Join(reports, name), raw, 0600); err != nil {
				t.Fatal(err)
			}
		}
		return nil, nil
	}
	inventory, err := CollectModules(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Sources) != 1 || inventory.Sources[0].Ecosystem != "go" || inventory.Collectors[0].Version != "go1.27.3" || len(inventory.Sources[0].LicensePaths) != 1 {
		t.Fatal("actual module source/legal/toolchain identity missing")
	}
	o.Collection = o.Output
	if _, err = VerifyModules(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	var changed bytes.Buffer
	changedWriter := zip.NewWriter(&changed)
	reader, _ := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	for _, member := range reader.File {
		stream, _ := member.Open()
		body, _ := io.ReadAll(stream)
		stream.Close()
		if strings.HasSuffix(member.Name, "/source.go") {
			body = append(body, []byte("// substituted source\n")...)
		}
		entry, _ := changedWriter.Create(member.Name)
		entry.Write(body)
	}
	changedWriter.Close()
	if _, _, err = inspectModule(changed.Bytes(), mod, info, record, sums); err == nil {
		t.Fatal("substituted module original accepted")
	}
	altered := map[string]string{}
	for k, v := range sums {
		altered[k] = v
	}
	altered[record.Path+"@"+record.Version] = "h1:" + strings.Repeat("A", 43) + "="
	if _, _, err = inspectModule(archive, mod, info, record, altered); err == nil {
		t.Fatal("module metadata substituted for authenticated sums")
	}
}
