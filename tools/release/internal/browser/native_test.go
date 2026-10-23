package browser

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/oci"
	"github.com/endorses/psst.zip/tools/release/internal/source"
)

func TestNativeCaptureIsBoundedNativeAndCleansExactCIDAfterFailures(t *testing.T) {
	for _, failure := range []string{"", "create", "start-cancel", "status", "copy", "archive"} {
		t.Run(failure, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cid := strings.Repeat("c", 64)
			config := "sha256:" + strings.Repeat("b", 64)
			var calls [][]string
			var private string
			o := NativeOptions{Root: t.TempDir(), Source: source.Context{Repository: "endorses/psst.zip", Version: "v1.2.3", Commit: strings.Repeat("a", 40)}, Platform: "linux/amd64", BuilderConfig: config}
			o.Execute = func(ctx context.Context, timeout time.Duration, args ...string) ([]byte, error) {
				calls = append(calls, append([]string(nil), args...))
				if timeout <= 0 || timeout > 3*time.Minute {
					t.Fatal("unbounded command timeout", timeout)
				}
				switch args[1] {
				case "info":
					return []byte("x86_64\n"), nil
				case "image":
					return contract.JSON([]map[string]string{{"Id": config, "Os": "linux", "Architecture": "amd64"}})
				case "create":
					line := strings.Join(args, " ")
					for _, required := range []string{"--pull=never", "--platform linux/amd64", "--network none", "--read-only", "--tmpfs /tmp:rw,noexec,nosuid,nodev,size=640m,mode=1777", "--cap-drop ALL", "--security-opt no-new-privileges", "--user 65532:65532", "--pids-limit 64", "--memory 1g", "--cpus 1", "--ulimit nofile=1024:1024", "--entrypoint node " + config} {
						if !strings.Contains(line, required) {
							t.Error("missing capture bound", required)
						}
					}
					if strings.Contains(line, "--mount") || strings.Contains(line, "--volume") || strings.Contains(line, "--privileged") {
						t.Fatal("unsafe capture mounts/capabilities")
					}
					for i, arg := range args {
						if arg == "--cidfile" {
							private = filepath.Dir(args[i+1])
							if err := os.WriteFile(args[i+1], []byte(cid+"\n"), 0600); err != nil {
								t.Fatal(err)
							}
						}
					}
					if failure == "create" {
						return nil, errors.New("uncertain create fixture")
					}
					return []byte(cid + "\n"), nil
				case "start":
					if failure == "start-cancel" {
						cancel()
						return nil, context.Canceled
					}
					return nil, nil
				case "inspect":
					if failure == "status" {
						return []byte("1\n"), nil
					}
					return []byte("0\n"), nil
				case "cp":
					if args[2] != cid+":/tmp/psst-browser-builder.tar" {
						t.Fatal("copied from another container", args)
					}
					if failure == "copy" {
						return nil, errors.New("copy fixture")
					}
					if failure == "archive" {
						return nil, os.WriteFile(args[3], []byte("invalid"), 0600)
					}
					_, err := WriteCapture(args[3], map[string][]byte{"src/input.js": []byte("captured input")})
					return nil, err
				case "rm":
					if ctx.Err() != nil || len(args) != 4 || args[3] != cid {
						t.Fatal("cleanup ignored cancellation or removed another container", args, ctx.Err())
					}
					return nil, nil
				}
				return nil, fmt.Errorf("unexpected fixture command")
			}
			files, err := CaptureNative(ctx, o, map[string][]byte{"web/scripts/pack-browser-builder-inputs.mjs": []byte("committed selective script")})
			if failure == "" {
				if err != nil || string(files["src/input.js"]) != "captured input" {
					t.Fatal(files, err)
				}
			} else if err == nil {
				t.Fatal("failure emitted captured facts", failure)
			}
			if len(calls) < 4 || calls[len(calls)-1][1] != "rm" {
				t.Fatal("owned capture container was not cleaned", calls)
			}
			if _, err := os.Lstat(private); !os.IsNotExist(err) {
				t.Fatal("private capture files leaked", private, err)
			}
		})
	}
}

func TestNativeCaptureRejectsEmulationAndSubstitutedPreloadedImageBeforeCreate(t *testing.T) {
	for _, substitution := range []string{"daemon", "config", "platform"} {
		t.Run(substitution, func(t *testing.T) {
			config := "sha256:" + strings.Repeat("b", 64)
			o := NativeOptions{Source: source.Context{Repository: "endorses/psst.zip", Version: "v1.2.3", Commit: strings.Repeat("a", 40)}, Platform: "linux/arm64", BuilderConfig: config}
			o.Execute = func(_ context.Context, _ time.Duration, args ...string) ([]byte, error) {
				if args[1] == "info" {
					if substitution == "daemon" {
						return []byte("amd64"), nil
					}
					return []byte("aarch64"), nil
				}
				if args[1] != "image" {
					t.Fatal("failed preflight changed Docker state", args)
				}
				id, arch := config, "arm64"
				if substitution == "config" {
					id = "sha256:" + strings.Repeat("d", 64)
				} else {
					arch = "amd64"
				}
				return contract.JSON([]map[string]string{{"Id": id, "Os": "linux", "Architecture": arch}})
			}
			if _, err := CaptureNative(context.Background(), o, map[string][]byte{"web/scripts/pack-browser-builder-inputs.mjs": []byte("script")}); err == nil {
				t.Fatal("accepted substituted native builder")
			}
		})
	}
}

func nativeReplayFixture(t *testing.T) (BindingOptions, source.RuntimeVerification) {
	t.Helper()
	binding := bindingFixture(t)
	web := map[string][]byte{}
	for name, body := range binding.Captured {
		if strings.HasPrefix(name, "build/") {
			web["srv/web/"+strings.TrimPrefix(name, "build/")] = body
		}
	}
	for name, body := range binding.GitFiles {
		if strings.HasPrefix(name, "backend/licenses/") {
			web["srv/web/licenses/backend/"+strings.TrimPrefix(name, "backend/licenses/")] = body
		}
	}
	runtime := source.RuntimeVerification{Inventory: source.Inventory{SchemaVersion: 2, Source: binding.Source}, Images: map[string]oci.Image{}, Files: map[string]map[string][]byte{"web": web, "backend": {}}, NoticeFiles: map[string][]source.File{}}
	for _, component := range []string{"backend", "web"} {
		config := "sha256:" + strings.Repeat("b", 64)
		if component == "web" {
			config = "sha256:" + strings.Repeat("c", 64)
		}
		runtime.Images[component] = oci.Image{Platform: "linux/amd64", ConfigDigest: config, Labels: map[string]string{"org.opencontainers.image.version": binding.Source.Version, "org.opencontainers.image.revision": binding.Source.Commit}}
	}
	return binding, runtime
}

func TestNativeSharedReplayRequiresBothImagesAndRetainsFalseAuthority(t *testing.T) {
	binding, runtime := nativeReplayFixture(t)
	result, err := VerifyNativeFacts(context.Background(), binding, runtime, "linux/amd64", "sha256:"+strings.Repeat("d", 64))
	if err != nil {
		t.Fatal(err)
	}
	if result.SchemaVersion != 2 || !result.GitSourceBindingVerified || !result.NPMMemberIntegrityVerified || !result.OCIImageVerified || result.PublicationAuthorized || result.DistributionAuthorized || result.PreferredSourceComplete || result.SourceReproductionVerified || result.BrowserModuleClosureVerified || !result.MeasurementAuthenticationRequired {
		t.Fatal("native result changed approval boundary", result)
	}
	folder := t.TempDir()
	capture, err := WriteCapture(filepath.Join(folder, "browser-inputs.tar"), binding.Captured)
	if err != nil {
		t.Fatal(err)
	}
	result.BrowserInputsSHA256 = capture.SHA256
	report := filepath.Join(folder, "browser-verification.json")
	if err := writeNativeReport(report, result); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	var retained map[string]json.RawMessage
	if contract.Decode(raw, maximumFile, &retained) != nil || !bytes.Equal(retained["publication_authorized"], []byte("false")) || !bytes.Equal(retained["schema_version"], []byte("2")) || len(retained["verification"]) == 0 {
		t.Fatal("report lost v2 facts or explicit unapproved boundary")
	}
	if err := writeNativeReport(report, result); err == nil {
		t.Fatal("overwrote browser verification")
	}
	delete(runtime.Images, "backend")
	if _, err := VerifyNativeFacts(context.Background(), binding, runtime, "linux/amd64", "sha256:"+strings.Repeat("d", 64)); err == nil {
		t.Fatal("browser replay accepted web archive alone")
	}
}

func TestNativeCommandRejectsSuppliedSuccessAndMissingPair(t *testing.T) {
	for _, args := range [][]string{{}, {"publish"}, {"collect", "--capture", "supplied.tar"}, {"verify", "--capture", "supplied.tar"}, {"collect", "--approve"}} {
		var out, diagnostic bytes.Buffer
		if err := RunNative(context.Background(), t.TempDir(), args, &out, &diagnostic); err == nil || out.Len() != 0 {
			t.Fatal("invalid command emitted native evidence", args, err)
		}
	}
}
