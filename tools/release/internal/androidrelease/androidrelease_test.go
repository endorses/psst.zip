package androidrelease

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"encoding/json"
	"github.com/endorses/psst.zip/tools/release/internal/artifact"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

func write(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}
func gitFixture(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("fixture Git command failed: %s", output)
	}
	return strings.TrimSpace(string(output))
}
func newSource(t *testing.T) (Tool, Version, string) {
	t.Helper()
	root := t.TempDir()
	version := Version{"0.1.0", 2}
	write(t, filepath.Join(root, "android/release-version.properties"), []byte("versionName=0.1.0\nversionCode=2\n"))
	for _, name := range legalNames {
		write(t, filepath.Join(root, "android/app/src/main/assets/licenses", name), []byte("license fixture\n"))
	}
	write(t, filepath.Join(root, "android/gradle/libs.versions.toml"), []byte("[versions]\nandroid-minSdk = \"26\"\nandroid-targetSdk = \"37\"\n"))
	gitFixture(t, root, "init", "-q")
	gitFixture(t, root, "add", ".")
	gitFixture(t, root, "-c", "user.name=Release Test", "-c", "user.email=release@example.invalid", "-c", "commit.gpgsign=false", "commit", "-qm", "fixture")
	revision := gitFixture(t, root, "rev-parse", "HEAD")
	gitFixture(t, root, "tag", "android-v0.1.0")
	gitFixture(t, root, "update-ref", "refs/remotes/origin/main", revision)
	return New(root), version, revision
}
func apkFixture(t *testing.T, tool Tool, version Version, revision string, extra map[string][]byte, fixture bool) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "input.apk")
	output, err := os.Create(file)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(output)
	identity := map[string]any{"versionName": version.Name, "versionCode": version.Code, "sourceRevision": revision}
	if fixture {
		identity["deviceFixture"] = true
	}
	data, _ := json.Marshal(identity)
	items := map[string][]byte{"assets/psst-release.json": data}
	for _, name := range legalNames {
		data, err := os.ReadFile(filepath.Join(tool.Root, "android/app/src/main/assets/licenses", name))
		if err != nil {
			t.Fatal(err)
		}
		items["assets/licenses/"+name] = data
	}
	for name, data := range extra {
		items[name] = data
	}
	for name, data := range items {
		entry, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = entry.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err = archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err = output.Close(); err != nil {
		t.Fatal(err)
	}
	return file
}
func fakeTool(t *testing.T, directory, name, body string) string {
	t.Helper()
	path := filepath.Join(directory, name)
	write(t, path, []byte("#!/bin/sh\nset -eu\n"+body+"\n"))
	if err := os.Chmod(path, 0755); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestSourceAndVersionIdentity(t *testing.T) {
	tool, version, revision := newSource(t)
	path := filepath.Join(tool.Root, "android/release-version.properties")
	source, err := tool.ValidateSource(context.Background(), "android-v0.1.0", revision, "origin/main", path)
	if err != nil || source.Version != version || source.Revision != revision {
		t.Fatalf("source identity: %+v %v", source, err)
	}
	write(t, filepath.Join(tool.Root, "notes.txt"), []byte("unrelated"))
	if _, err = tool.ValidateSource(context.Background(), "android-v0.1.0", revision, "origin/main", path); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(tool.Root, "shared/untracked.kt"), []byte("untracked"))
	if _, err = tool.ValidateSource(context.Background(), "android-v0.1.0", revision, "origin/main", path); err == nil {
		t.Fatal("untracked mobile source accepted")
	}
	_ = os.Remove(filepath.Join(tool.Root, "shared/untracked.kt"))
	for _, text := range []string{"versionName=0.1.0\nversionCode=2\nversionCode=3\n", "versionName=0.1.0\nversionCode=02\n", "versionName=0.1.0\nversionCode=2100000001\n", "versionName=01.1.0\nversionCode=2\n"} {
		write(t, path, []byte(text))
		if _, err = VersionMetadata(path); err == nil {
			t.Fatal("ambiguous version accepted")
		}
	}
}
func TestSignerSDKLabelsAndRotationExclusion(t *testing.T) {
	signer := strings.Repeat("b", 64)
	for _, label := range []string{"Signer #1", "V2 Signer:", "V3.0 Signer:", "V3.2 Signer:"} {
		if err := VerifySignerOutput("Number of signers: 1\n"+label+" certificate SHA-256 digest: "+signer+"\n", signer); err != nil {
			t.Fatal(err)
		}
	}
	good := "Number of signers: 1\nV2 Signer: certificate SHA-256 digest: " + signer + "\nV3.0 Signer: certificate SHA-256 digest: " + signer + "\nSource Stamp Signer: certificate SHA-256 digest: " + strings.Repeat("c", 64) + "\n"
	if err := VerifySignerOutput(good, signer); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{strings.Replace(good, "Number of signers: 1", "Number of signers: 2", 1), good + "Signer #2 certificate SHA-256 digest: " + signer + "\n", strings.Replace(good, "V2 Signer:", "Unknown Signer:", 1), strings.Replace(good, signer, strings.Repeat("a", 64), 1), "Number of signers: 1\n"} {
		if err := VerifySignerOutput(value, signer); err == nil {
			t.Fatal("invalid/multiple/rotated signer accepted")
		}
	}
}
func TestAPKIdentityLegalAndArchiveBoundaries(t *testing.T) {
	tool, version, revision := newSource(t)
	apk := apkFixture(t, tool, version, revision, nil, false)
	assets, err := tool.InspectAPKAssets(apk, version, revision, false)
	if err != nil || len(assets.Legal) != 3 || len(assets.ABIs) != 0 {
		t.Fatalf("APK assets: %+v %v", assets, err)
	}
	if _, err = tool.InspectAPKAssets(apk, version, strings.Repeat("a", 40), false); err == nil {
		t.Fatal("wrong source accepted")
	}
	for _, extra := range []map[string][]byte{{"../credentials": []byte("unused")}, {"assets/licenses/THIRD_PARTY_NOTICES.txt": []byte("changed")}, {"lib/unknown/libexample.so": []byte("not ELF")}, {"assets/psst-release.json": []byte(`{"versionCode":2,"versionCode":2}`)}} {
		bad := apkFixture(t, tool, version, revision, extra, false)
		if _, err = tool.InspectAPKAssets(bad, version, revision, false); err == nil {
			t.Fatal("unsafe/mismatched APK accepted")
		}
	}
	private := apkFixture(t, tool, version, revision, nil, true)
	if _, err = tool.InspectAPKAssets(private, version, revision, false); err == nil {
		t.Fatal("private fixture accepted for publication")
	}
	if _, err = tool.InspectAPKAssets(private, version, revision, true); err != nil {
		t.Fatal(err)
	}
	if err = RequireUnsigned(apk); err != nil {
		t.Fatal(err)
	}
	signedJar := apkFixture(t, tool, version, revision, map[string][]byte{"META-INF/TEST.RSA": []byte("signature fixture")}, false)
	if err = RequireUnsigned(signedJar); err == nil {
		t.Fatal("JAR signature accepted as unsigned")
	}
}
func TestELFAlignmentAndArchitecture(t *testing.T) {
	data := make([]byte, 128)
	copy(data, []byte{0x7f, 'E', 'L', 'F', 2, 1})
	binary.LittleEndian.PutUint16(data[18:], 183)
	binary.LittleEndian.PutUint64(data[32:], 64)
	binary.LittleEndian.PutUint16(data[54:], 56)
	binary.LittleEndian.PutUint16(data[56:], 1)
	binary.LittleEndian.PutUint32(data[64:], 1)
	binary.LittleEndian.PutUint64(data[112:], 16384)
	if err := VerifyELF(data, "arm64-v8a"); err != nil {
		t.Fatal(err)
	}
	if err := VerifyELF(data, "x86_64"); err == nil {
		t.Fatal("wrong ABI accepted")
	}
	binary.LittleEndian.PutUint64(data[112:], 4096)
	if err := VerifyELF(data, ""); err == nil {
		t.Fatal("4 KB ELF accepted")
	}
	binary.LittleEndian.PutUint64(data[112:], 16384)
	binary.LittleEndian.PutUint64(data[72:], 4096)
	if err := VerifyELF(data, ""); err == nil {
		t.Fatal("unaligned load accepted")
	}
}
func lp(value []byte) []byte {
	result := make([]byte, 4)
	binary.LittleEndian.PutUint32(result, uint32(len(value)))
	return append(result, value...)
}
func signingFixture(t *testing.T, apk string, id uint32, value []byte) {
	t.Helper()
	central, err := centralDirectory(apk)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(apk)
	if err != nil {
		t.Fatal(err)
	}
	pair := make([]byte, 12)
	binary.LittleEndian.PutUint64(pair, uint64(len(value)+4))
	binary.LittleEndian.PutUint32(pair[8:], id)
	pair = append(pair, value...)
	size := uint64(len(pair) + 24)
	block := make([]byte, 8)
	binary.LittleEndian.PutUint64(block, size)
	block = append(block, pair...)
	footer := make([]byte, 24)
	binary.LittleEndian.PutUint64(footer, size)
	copy(footer[8:], "APK Sig Block 42")
	block = append(block, footer...)
	raw := append(append(append([]byte{}, original[:central]...), block...), original[central:]...)
	eocd := bytes.LastIndex(raw, []byte{'P', 'K', 5, 6})
	binary.LittleEndian.PutUint32(raw[eocd+16:], uint32(central)+uint32(len(block)))
	write(t, apk, raw)
}
func TestPublicSigningBlockRejectsLineage(t *testing.T) {
	tool, version, revision := newSource(t)
	v3 := func(attributes []byte) []byte {
		sdk := make([]byte, 8)
		binary.LittleEndian.PutUint32(sdk, 28)
		binary.LittleEndian.PutUint32(sdk[4:], 2147483647)
		signed := append(append(lp(nil), lp(nil)...), sdk...)
		signed = append(signed, lp(attributes)...)
		return lp(lp(lp(signed)))
	}
	good := apkFixture(t, tool, version, revision, nil, false)
	signingFixture(t, good, 0xf05368c0, v3(nil))
	if err := RejectPublicLineage(good); err != nil {
		t.Fatal(err)
	}
	if err := RequireUnsigned(good); err == nil {
		t.Fatal("signing block accepted as unsigned")
	}
	for _, id := range []uint32{0xf05368c0, 0x1b93ad61, 0x70e1c89f} {
		apk := apkFixture(t, tool, version, revision, nil, false)
		attribute := make([]byte, 4)
		binary.LittleEndian.PutUint32(attribute, 0x3ba06f8c)
		signingFixture(t, apk, id, v3(lp(append(attribute, []byte("lineage fixture")...))))
		if err := RejectPublicLineage(apk); err == nil {
			t.Fatal("rotation lineage accepted")
		}
	}
}
func TestTimingsAndPublicationVersionFloor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "timings.jsonl")
	good := `{"task":":app:minifyReleaseWithR8","duration_ms":25,"success":true}`
	write(t, path, []byte(good+"\n"))
	report, err := ReportTimings(path)
	if err != nil || report.Groups["R8 shrinking"].Duration != 25 {
		t.Fatalf("timing report %v", err)
	}
	for _, data := range []string{`{"task":":task\n| markup |","duration_ms":1,"success":true}`, `{"task":":app:test","duration_ms":-1,"success":true}`, `{"task":":app:test","duration_ms":1,"success":true,"extra":"private"}`, ""} {
		write(t, path, []byte(data))
		if _, err = ReportTimings(path); err == nil {
			t.Fatal("invalid/private timing accepted")
		}
	}
	for _, data := range []string{`[{"tag_name":"android-v0.1.0","body":"Android versionCode: 2"}]`, `[{"tag_name":"android-v0.0.9","body":"Android versionCode: 2"}]`, `[{"tag_name":"android-v0.0.9","body":"missing"}]`, `[{"tag_name":"android-v0.0.9","tag_name":"other"}]`} {
		if err = CheckPublication([]byte(data), "android-v0.1.0", Version{"0.1.0", 2}); err == nil {
			t.Fatal("existing/decreasing/ambiguous publication accepted")
		}
	}
	if err = CheckPublication([]byte(`[{"tag_name":"v0.1.6"},{"tag_name":"android-v0.0.9","body":"Android versionCode: 1"}]`), "android-v0.1.0", Version{"0.1.0", 2}); err != nil {
		t.Fatal(err)
	}
}
func TestSigningErrorsKeepSecretsOutAndRemovePartialOutput(t *testing.T) {
	tool, version, revision := newSource(t)
	apk := apkFixture(t, tool, version, revision, nil, false)
	directory := t.TempDir()
	key := filepath.Join(directory, "fixture.p12")
	write(t, key, []byte("disposable non-key fixture"))
	output := filepath.Join(directory, "signed.apk")
	sdk := t.TempDir()
	fakeTool(t, sdk, "zipalign", `cp "$5" "$6"`)
	fakeTool(t, sdk, "apksigner", `for arg in "$@"; do if [ "${next:-}" = yes ]; then output=$arg; break; fi; if [ "$arg" = --out ]; then next=yes; fi; done
printf partial > "$output"
printf 'keystore password was incorrect: %s' "$ANDROID_RELEASE_STORE_PASSWORD" >&2
exit 7`)
	secret := t.Name()
	tool.Runner = tool.Runner.With(map[string]string{"ANDROID_RELEASE_KEYSTORE": key, "ANDROID_RELEASE_KEY_ALIAS": "fixture", "ANDROID_RELEASE_STORE_PASSWORD": secret, "ANDROID_RELEASE_KEY_PASSWORD": secret, "ANDROID_RELEASE_KEYSTORE_BASE64": ""})
	err := tool.SignAPK(context.Background(), apk, output, sdk)
	if err == nil || !strings.Contains(err.Error(), "store-password-or-format") || strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), directory) {
		t.Fatalf("unsafe signing diagnostic: %v", err)
	}
	if exists(output) {
		t.Fatal("partial signing output retained")
	}
	if _, err = tool.command(context.Background(), time.Second, filepath.Join(sdk, "missing-private-tool-name"), secret); err == nil || strings.Contains(err.Error(), "private-tool") || strings.Contains(err.Error(), secret) {
		t.Fatalf("unsafe launch diagnostic: %v", err)
	}
	for _, name := range []string{"ANDROID_RELEASE_KEY_ALIAS", "ANDROID_RELEASE_STORE_PASSWORD", "ANDROID_RELEASE_KEY_PASSWORD"} {
		tool.Runner = tool.Runner.With(map[string]string{name: ""})
		if err = tool.SignAPK(context.Background(), apk, output, sdk); err == nil {
			t.Fatal("missing signing credential accepted")
		}
	}
}
func TestDisposableEmulatorAndInstrumentationBoundaries(t *testing.T) {
	tool := New(t.TempDir())
	if _, err := tool.Emulator("nonexistent-adb", "physical-device"); err == nil {
		t.Fatal("physical serial accepted")
	}
	adb := fakeTool(t, t.TempDir(), "adb", `shift 2
case "$*" in
'get-state') printf device;;
'shell getprop ro.kernel.qemu') printf 1;;
'shell getprop ro.hardware') printf ranchu;;
'emu avd name') printf '%s\nOK\n' "$PSST_FIXTURE_AVD";;
'shell getprop ro.build.version.sdk') printf 26;;
'shell pm list packages') printf '%s\n' "$PSST_FIXTURE_PACKAGES";;
*) exit 9;; esac`)
	tool.Runner = tool.Runner.With(map[string]string{"PSST_FIXTURE_AVD": "psst-release-fixture-test", "PSST_FIXTURE_PACKAGES": "package:android"})
	emulator, err := tool.Emulator(adb, "emulator-5554")
	if err != nil {
		t.Fatal(err)
	}
	if api, err := emulator.Validate(context.Background()); err != nil || api != 26 {
		t.Fatalf("owned empty emulator: %d %v", api, err)
	}
	for _, override := range []map[string]string{{"PSST_FIXTURE_AVD": "Everyday_Phone"}, {"PSST_FIXTURE_PACKAGES": "package:" + Package}, {"PSST_FIXTURE_PACKAGES": "package:" + testPackage}} {
		other := tool
		other.Runner = other.Runner.With(override)
		emulator, _ := other.Emulator(adb, "emulator-5554")
		if _, err = emulator.Validate(context.Background()); err == nil {
			t.Fatal("existing installation or unowned AVD accepted")
		}
	}
	for _, value := range []string{"FAILURES!!!", "OK (1 test)\nINSTRUMENTATION_FAILED: crash", "INSTRUMENTATION_CODE: 0", "OK (1 test)\nINSTRUMENTATION_CODE: 0", "OK (1 test)"} {
		if InstrumentationPassed(value) {
			t.Fatal("incomplete/failed instrumentation accepted")
		}
	}
	if !InstrumentationPassed("OK (1 test)\nINSTRUMENTATION_CODE: -1\n") {
		t.Fatal("valid instrumentation rejected")
	}
}
func TestPackageRejectsChangedArtifactBeforeWriting(t *testing.T) {
	tool, version, revision := newSource(t)
	apk := apkFixture(t, tool, version, revision, nil, false)
	signer := strings.Repeat("b", 64)
	report := Verification{Schema: 1, Revision: revision, Manifest: Manifest{Package, version, 26, 37}, Signed: true, Signer: &signer, Digest: strings.Repeat("a", 64), Size: 1}
	verification := filepath.Join(t.TempDir(), "verification.json")
	if err := writeJSON(verification, report); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "publication")
	if err := tool.PackageRelease(context.Background(), apk, verification, "android-v0.1.0", revision, output); err == nil {
		t.Fatal("changed APK packaged")
	}
	if exists(output) {
		t.Fatal("rejected publication directory created")
	}
}
func TestExecutedCIRequiresActualSuccessfulSteps(t *testing.T) {
	job := CIJob{Status: "completed", Conclusion: "success", Steps: []JobStep{{Name: "Build", Conclusion: "skipped"}}}
	if Executed(job, []string{"Build"}) {
		t.Fatal("skipped CI accepted")
	}
	job.Steps[0] = JobStep{"Build", "success", "start", "end"}
	if !Executed(job, []string{"Build"}) {
		t.Fatal("executed CI rejected")
	}
	job.Steps = append(job.Steps, job.Steps[0])
	if Executed(job, []string{"Build"}) {
		t.Fatal("ambiguous duplicate step accepted")
	}
}
func TestCommandRejectsMutuallyExclusiveSigningModes(t *testing.T) {
	err := Run(context.Background(), t.TempDir(), []string{"verify-apk", "--apk", "unused", "--sdk-tools", "unused", "--revision", strings.Repeat("a", 40), "--output", "unused", "--unsigned", "--signer-sha256", strings.Repeat("b", 64)}, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("two signing modes accepted")
	}
	var out bytes.Buffer
	if err = Run(context.Background(), t.TempDir(), []string{"help"}, &out, io.Discard); err != nil || !strings.Contains(out.String(), "same-signer") {
		t.Fatal("command help missing update behavior")
	}
}
func TestPackagedReleaseReadbackMatchesRetainedContract(t *testing.T) {
	tool, version, revision := newSource(t)
	apk := apkFixture(t, tool, version, revision, nil, false)
	assets, err := tool.InspectAPKAssets(apk, version, revision, false)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(apk)
	hash, err := artifact.HashFile(apk)
	if err != nil {
		t.Fatal(err)
	}
	signer := strings.Repeat("b", 64)
	report := Verification{Schema: 1, Revision: revision, Manifest: Manifest{Package, version, 26, 37}, Assets: assets, Signed: true, Signer: &signer, Digest: hash, Size: info.Size()}
	file := filepath.Join(t.TempDir(), "verified.json")
	if err = writeJSON(file, report); err != nil {
		t.Fatal(err)
	}
	// Real received data is intentionally untracked, and Git source archives
	// must never pick it up while packaging all tracked source inputs.
	write(t, filepath.Join(tool.Root, "incoming/private.bin"), []byte("disposable received-data fixture"))
	output := filepath.Join(t.TempDir(), "publication")
	if err = tool.PackageRelease(context.Background(), apk, file, "android-v0.1.0", revision, output); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(output)
	if err != nil || len(entries) != 6 {
		t.Fatalf("publication assets: %d %v", len(entries), err)
	}
	metadata, err := os.ReadFile(filepath.Join(output, "android-release.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err = contract.Decode(metadata, 1<<20, &fields); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"versionName", "versionCode", "applicationId", "sourceRevision", "signed", "signerSha256", "nativeAbis", "legalSha256", "apkSha256", "apkSize", "tag", "apk"} {
		if _, ok := fields[name]; !ok {
			t.Fatalf("missing retained public field %s", name)
		}
	}
	sums, err := os.ReadFile(filepath.Join(output, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(sums)), "\n") {
		want, name, ok := strings.Cut(line, "  ")
		if !ok {
			t.Fatal("invalid checksum record")
		}
		actual, err := artifact.HashFile(filepath.Join(output, name))
		if err != nil || actual != want {
			t.Fatal("packaged checksum mismatch")
		}
	}
	source, err := os.Open(filepath.Join(output, "source.tar.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	compressed, err := gzip.NewReader(source)
	if err != nil {
		t.Fatal(err)
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	found := false
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(header.Name, "incoming/") {
			t.Fatal("received files leaked into source archive")
		}
		if header.Name == "psst.zip/android/release-version.properties" {
			found = true
		}
	}
	if !found {
		t.Fatal("tracked source missing from archive")
	}
}

func TestProtectedPublicationRejectsMissingPoliciesAndBypass(t *testing.T) {
	tool := New(t.TempDir())
	directory := t.TempDir()
	fakeTool(t, directory, "gh", `case "$4" in
*/branches/main) printf '%s' "$PSST_FIXTURE_MAIN";;
*/immutable-releases) printf '%s' "$PSST_FIXTURE_IMMUTABLE";;
*/environments/android-release) printf '%s' "$PSST_FIXTURE_ENVIRONMENT";;
*/deployment-branch-policies*) printf '%s' "$PSST_FIXTURE_BRANCHES";;
*/rulesets\?*) printf '[{"id":4,"enforcement":"active","target":"tag"}]';;
*/rulesets/4) printf '%s' "$PSST_FIXTURE_RULESET";;
*) exit 9;; esac`)
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	defaults := map[string]string{"ANDROID_RELEASE_PUBLICATION_READY": "true", "ANDROID_RELEASE_DEVICE_EVIDENCE": t.Name(), "ANDROID_RELEASE_SIGNER_SHA256": strings.Repeat("a", 64), "PSST_FIXTURE_MAIN": `{"name":"main","protected":true}`, "PSST_FIXTURE_IMMUTABLE": `{"enabled":true}`, "PSST_FIXTURE_ENVIRONMENT": `{"protection_rules":[{"type":"required_reviewers","reviewers":[{"id":1}]}],"deployment_branch_policy":{"protected_branches":false,"custom_branch_policies":true}}`, "PSST_FIXTURE_BRANCHES": `{"total_count":1,"branch_policies":[{"name":"main","type":"branch"}]}`, "PSST_FIXTURE_RULESET": `{"conditions":{"ref_name":{"include":["refs/tags/android-v*"],"exclude":[]}},"bypass_actors":[],"rules":[{"type":"update"},{"type":"deletion"}]}`}
	tool.Runner = tool.Runner.With(defaults)
	if err := tool.ProtectedPublication(context.Background(), "endorses/psst.zip", "android-v0.1.0"); err != nil {
		t.Fatal(err)
	}
	for _, override := range []map[string]string{
		{"PSST_FIXTURE_MAIN": `{"name":"main","protected":false}`},
		{"PSST_FIXTURE_IMMUTABLE": `{}`},
		{"PSST_FIXTURE_IMMUTABLE": `{"enabled":false}`},
		{"PSST_FIXTURE_ENVIRONMENT": `{"protection_rules":[],"deployment_branch_policy":{"protected_branches":false,"custom_branch_policies":true}}`},
		{"PSST_FIXTURE_ENVIRONMENT": `{"protection_rules":[{"type":"required_reviewers","reviewers":[{"id":1}]}]}`},
		{"PSST_FIXTURE_BRANCHES": `{"total_count":1,"branch_policies":[{"name":"main","type":"tag"}]}`},
		{"PSST_FIXTURE_RULESET": `{"conditions":{"ref_name":{"include":["refs/tags/android-v*"],"exclude":[]}},"rules":[{"type":"update"},{"type":"deletion"}]}`},
		{"PSST_FIXTURE_RULESET": `{"conditions":{"ref_name":{"include":["refs/tags/android-v*"],"exclude":[]}},"bypass_actors":[{"actor_id":1}],"rules":[{"type":"update"},{"type":"deletion"}]}`},
		{"PSST_FIXTURE_RULESET": `{"conditions":{"ref_name":{"include":["refs/tags/v*"],"exclude":[]}},"bypass_actors":[],"rules":[{"type":"update"},{"type":"deletion"}]}`},
		{"ANDROID_RELEASE_DEVICE_EVIDENCE": ""},
	} {
		candidate := tool
		candidate.Runner = candidate.Runner.With(override)
		if err := candidate.ProtectedPublication(context.Background(), "endorses/psst.zip", "android-v0.1.0"); err == nil {
			t.Fatalf("incomplete policy accepted: %v", override)
		}
	}
}
func TestExactSourceCIReuseRequiresTrustedDefinitionAndExecutedJobs(t *testing.T) {
	tool, _, _ := newSource(t)
	write(t, filepath.Join(tool.Root, ".github/workflows/ci.yml"), []byte("trusted fixture workflow\n"))
	gitFixture(t, tool.Root, "add", ".")
	gitFixture(t, tool.Root, "-c", "user.name=Release Test", "-c", "user.email=release@example.invalid", "-c", "commit.gpgsign=false", "commit", "-qm", "CI fixture")
	revision := gitFixture(t, tool.Root, "rev-parse", "HEAD")
	gitFixture(t, tool.Root, "update-ref", "refs/remotes/origin/main", revision)
	directory := t.TempDir()
	fakeTool(t, directory, "gh", `case "$4" in
*/actions/workflows/ci.yml/runs\?*) printf '%s' "$PSST_FIXTURE_RUNS";;
*/attempts/1/jobs\?*) printf '%s' "$PSST_FIXTURE_JOBS";;
*) exit 9;; esac`)
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	runs := map[string]any{"workflow_runs": []any{map[string]any{"id": 1, "run_attempt": 1, "head_sha": revision, "head_branch": "main", "event": "push", "path": ".github/workflows/ci.yml", "repository": map[string]string{"full_name": "endorses/psst.zip"}, "head_repository": map[string]string{"full_name": "endorses/psst.zip"}}}}
	jobs := []CIJob{}
	for name, required := range requiredSteps {
		job := CIJob{ID: int64(len(jobs) + 1), Name: name, Status: "completed", Conclusion: "success"}
		for _, step := range required {
			job.Steps = append(job.Steps, JobStep{step, "success", "start", "end"})
		}
		jobs = append(jobs, job)
	}
	data, _ := json.Marshal(runs)
	jobData, _ := json.Marshal(map[string]any{"jobs": jobs})
	tool.Runner = tool.Runner.With(map[string]string{"PSST_FIXTURE_RUNS": string(data), "PSST_FIXTURE_JOBS": string(jobData)})
	evidence, err := tool.CIEvidence(context.Background(), "endorses/psst.zip", revision)
	if err != nil || evidence.Android == nil || evidence.Security == nil {
		t.Fatalf("CI reuse: %+v %v", evidence, err)
	}
	for index := range jobs {
		for step := range jobs[index].Steps {
			jobs[index].Steps[step].Conclusion = "skipped"
		}
	}
	jobData, _ = json.Marshal(map[string]any{"jobs": jobs})
	tool.Runner = tool.Runner.With(map[string]string{"PSST_FIXTURE_JOBS": string(jobData)})
	evidence, err = tool.CIEvidence(context.Background(), "endorses/psst.zip", revision)
	if err != nil || evidence.Android != nil || evidence.Security != nil {
		t.Fatal("skipped job steps reused")
	}
}
func TestSigningFailureClassificationIsBoundedAndFixed(t *testing.T) {
	for _, item := range []struct{ marker, want string }{
		{"Permission denied", "permission-or-disk"},
		{"Disk quota exceeded", "permission-or-disk"},
		{"java.security.NoSuchAlgorithmException", "java-provider-or-algorithm"},
		{"keystore password was incorrect: UnrecoverableKeyException", "store-password-or-format"},
		{"Keystore was tampered with", "store-password-or-format"},
		{"UnrecoverableKeyException: Cannot recover key", "key-password"},
		{"Keystore does not contain key with alias", "alias-or-private-key"},
		{"Refusing to sign debuggable APK", "apk-policy"},
		{"unknown fixture", "unclassified"},
	} {
		if got := failureCategory([]byte(item.marker), true); got != item.want {
			t.Fatalf("fixed failure category: got %s want %s", got, item.want)
		}
	}
	if got := failureCategory([]byte(strings.Repeat("x", 65536)+"Permission denied"), true); got != "unclassified" {
		t.Fatal("classification consumed an unbounded stderr suffix")
	}
}
