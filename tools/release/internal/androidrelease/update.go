package androidrelease

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"crypto/rand"
	"encoding/base64"
)

const testPackage = Package + ".test"
const testComponent = testPackage + "/zip.psst.android.fixture.ReleaseUpdateInstrumentation"
const testMethod = "zip.psst.android.data.ReleaseUpdateFixture#run"

type UpdateOptions struct{ Seed, Next, SeedTest, NextTest, SDK, ADB, Keytool, Serial, Revision, Output string }
type DisposableEmulator struct {
	tool   Tool
	prefix []string
}

func (t Tool) Emulator(adb, serial string) (DisposableEmulator, error) {
	if !regexp.MustCompile(`^emulator-[0-9]+$`).MatchString(serial) {
		return DisposableEmulator{}, errors.New("only an explicitly selected disposable emulator is supported")
	}
	return DisposableEmulator{t, []string{adb, "-s", serial}}, nil
}
func (e DisposableEmulator) run(ctx context.Context, args ...string) (string, error) {
	return e.tool.command(ctx, 120*time.Second, append(append([]string{}, e.prefix...), args...)...)
}
func (e DisposableEmulator) Validate(ctx context.Context) (int, error) {
	for _, check := range []struct {
		args   []string
		values []string
	}{{[]string{"get-state"}, []string{"device"}}, {[]string{"shell", "getprop", "ro.kernel.qemu"}, []string{"1"}}, {[]string{"shell", "getprop", "ro.hardware"}, []string{"ranchu", "goldfish"}}} {
		output, err := e.run(ctx, check.args...)
		if err != nil {
			return 0, err
		}
		found := false
		for _, allowed := range check.values {
			if strings.TrimSpace(output) == allowed {
				found = true
			}
		}
		if !found {
			return 0, errors.New("target is not a connected supported disposable emulator")
		}
	}
	name, err := e.run(ctx, "emu", "avd", "name")
	if err != nil {
		return 0, err
	}
	if !strings.HasPrefix(strings.Split(strings.TrimSpace(name), "\n")[0], "psst-release-fixture-") {
		return 0, errors.New("AVD is not owned by the disposable release fixture")
	}
	output, err := e.run(ctx, "shell", "getprop", "ro.build.version.sdk")
	if err != nil {
		return 0, err
	}
	api, err := strconv.Atoi(strings.TrimSpace(output))
	if err != nil || api < 26 {
		return 0, errors.New("update fixture requires Android API 26+")
	}
	output, err = e.run(ctx, "shell", "pm", "list", "packages")
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(output, "\n") {
		name := strings.TrimSpace(line)
		if name == "package:"+Package || name == "package:"+testPackage {
			return 0, errors.New("fixture refuses existing target or instrumentation installation")
		}
	}
	return api, nil
}
func (e DisposableEmulator) install(ctx context.Context, apk string, update bool) error {
	args := []string{"install"}
	if update {
		args = append(args, "-r")
	}
	output, err := e.run(ctx, append(args, apk)...)
	if err != nil {
		return err
	}
	if !regexp.MustCompile(`(?m)^Success\r?$`).MatchString(output) {
		return errors.New("disposable APK install failed")
	}
	return nil
}
func InstrumentationPassed(value string) bool {
	if !regexp.MustCompile(`\bOK \(1 test\)`).MatchString(value) || !regexp.MustCompile(`(?m)^INSTRUMENTATION_CODE: -1\s*$`).MatchString(value) {
		return false
	}
	for _, marker := range []string{"FAILURES!!!", "INSTRUMENTATION_FAILED", "INSTRUMENTATION_ABORTED", "shortMsg="} {
		if strings.Contains(value, marker) {
			return false
		}
	}
	return true
}
func (e DisposableEmulator) fixture(ctx context.Context, phase string) error {
	output, err := e.run(ctx, "shell", "am", "instrument", "-w", "-r", "-e", "class", testMethod, "-e", "disposableUpdate", "true", "-e", "updatePhase", phase, testComponent)
	if err != nil {
		return err
	}
	if !InstrumentationPassed(output) {
		return errors.New("actual installed-storage update fixture failed")
	}
	return nil
}
func (t Tool) disposableKey(ctx context.Context, keytool, directory string) (string, string, error) {
	keystore, certificate := filepath.Join(directory, "fixture.p12"), filepath.Join(directory, "fixture.der")
	for _, args := range [][]string{{keytool, "-genkeypair", "-alias", "fixture", "-keystore", keystore, "-storetype", "PKCS12", "-keyalg", "RSA", "-keysize", "2048", "-validity", "3", "-dname", "CN=Disposable release update fixture", "-storepass:env", "PSST_FIXTURE_PASSWORD", "-keypass:env", "PSST_FIXTURE_PASSWORD", "-noprompt"}, {keytool, "-exportcert", "-alias", "fixture", "-keystore", keystore, "-storepass:env", "PSST_FIXTURE_PASSWORD", "-file", certificate}} {
		if _, err := t.command(ctx, 120*time.Second, args...); err != nil {
			return "", "", err
		}
	}
	if err := os.Chmod(keystore, 0600); err != nil {
		return "", "", errors.New("disposable key permissions could not be restricted")
	}
	data, err := safeRead(certificate, 1<<20)
	if err != nil {
		return "", "", err
	}
	return keystore, digest(data), nil
}
func (t Tool) signFixture(ctx context.Context, apk, output, sdk, keystore string) error {
	aligned := output + ".aligned.apk"
	defer os.Remove(aligned)
	if _, err := t.command(ctx, 120*time.Second, filepath.Join(sdk, "zipalign"), "-P", "16", "-f", "4", apk, aligned); err != nil {
		return err
	}
	_, err := t.command(ctx, 120*time.Second, filepath.Join(sdk, "apksigner"), "sign", "--ks", keystore, "--ks-key-alias", "fixture", "--ks-pass", "env:PSST_FIXTURE_PASSWORD", "--key-pass", "env:PSST_FIXTURE_PASSWORD", "--v4-signing-enabled", "false", "--debuggable-apk-permitted", "true", "--out", output, aligned)
	return err
}
func (t Tool) CheckUpdate(ctx context.Context, options UpdateOptions) error {
	if strings.EqualFold(t.env("GITHUB_ACTIONS"), "true") {
		return errors.New("installed-data update check is a local disposable fixture")
	}
	if !revisionPattern.MatchString(options.Revision) {
		return errors.New("invalid update source revision")
	}
	if exists(options.Output) {
		return errors.New("device evidence output already exists")
	}
	emulator, err := t.Emulator(options.ADB, options.Serial)
	if err != nil {
		return err
	}
	api, err := emulator.Validate(ctx)
	if err != nil {
		return err
	}
	inputs := []string{options.Seed, options.Next}
	manifests := []Manifest{}
	for _, apk := range inputs {
		output, err := t.command(ctx, 120*time.Second, filepath.Join(options.SDK, "aapt2"), "dump", "badging", apk)
		if err != nil {
			return err
		}
		manifest, err := ParseBadging(output)
		if err != nil {
			return err
		}
		if manifest.ApplicationID != Package {
			return errors.New("fixture target package differs")
		}
		if _, err = t.InspectAPKAssets(apk, manifest.Version, options.Revision, true); err != nil {
			return err
		}
		manifests = append(manifests, manifest)
	}
	if manifests[0].Code >= manifests[1].Code {
		return errors.New("fixture requires increasing optimized release version codes")
	}
	temporary, err := os.MkdirTemp("", "psst-release-update-")
	if err != nil {
		return errors.New("disposable update directory could not be created")
	}
	defer os.RemoveAll(temporary)
	password := make([]byte, 36)
	if _, err = rand.Read(password); err != nil {
		return errors.New("disposable key password could not be generated")
	}
	secret := base64.RawURLEncoding.EncodeToString(password)
	t.Runner = t.Runner.With(map[string]string{"PSST_FIXTURE_PASSWORD": secret, "ANDROID_RELEASE_KEYSTORE_BASE64": ""})
	keystore, fingerprint, err := t.disposableKey(ctx, options.Keytool, temporary)
	if err != nil {
		return err
	}
	t.Runner = t.Runner.With(map[string]string{"ANDROID_RELEASE_KEYSTORE": keystore, "ANDROID_RELEASE_KEY_ALIAS": "fixture", "ANDROID_RELEASE_STORE_PASSWORD": secret, "ANDROID_RELEASE_KEY_PASSWORD": secret})
	signed, tests := []string{}, []string{}
	for index, apk := range inputs {
		output := filepath.Join(temporary, fmt.Sprintf("release-%d.apk", index))
		if err = t.SignAPK(ctx, apk, output, options.SDK); err != nil {
			return err
		}
		versionFile := filepath.Join(temporary, fmt.Sprintf("version-%d.properties", index))
		version := manifests[index].Version
		if err = os.WriteFile(versionFile, fmt.Appendf(nil, "versionName=%s\nversionCode=%d\n", version.Name, version.Code), 0600); err != nil {
			return errors.New("fixture version file could not be written")
		}
		if _, err = t.VerifyAPK(ctx, output, options.SDK, fingerprint, versionFile, options.Revision, true); err != nil {
			return err
		}
		signed = append(signed, output)
	}
	for index, apk := range []string{options.SeedTest, options.NextTest} {
		output := filepath.Join(temporary, fmt.Sprintf("release-%d-test.apk", index))
		if err = t.signFixture(ctx, apk, output, options.SDK, keystore); err != nil {
			return err
		}
		tests = append(tests, output)
	}
	if err = emulator.install(ctx, signed[0], false); err != nil {
		return err
	}
	if err = emulator.install(ctx, tests[0], false); err != nil {
		return err
	}
	if err = emulator.fixture(ctx, "seed"); err != nil {
		return err
	}
	if _, err = emulator.run(ctx, "shell", "am", "force-stop", Package); err != nil {
		return err
	}
	if err = emulator.install(ctx, signed[1], true); err != nil {
		return err
	}
	if err = emulator.install(ctx, tests[1], true); err != nil {
		return err
	}
	if err = emulator.fixture(ctx, "verify"); err != nil {
		return err
	}
	return writeJSON(options.Output, map[string]any{"schema_version": 2, "sourceRevision": options.Revision, "deviceApi": api, "disposableEmulator": true, "disposableSigningKeys": true, "fixtureModifiedApks": true, "applicationId": Package, "versionCodes": []int{manifests[0].Code, manifests[1].Code}, "sameSignerUpdatePassed": true, "preservedState": []string{"application UID", "Keystore session", "Room Send history and file decryption", "Receive-v2 private key and HPKE/AES decryption", "guest history and encrypted capability"}, "operatorInstallationVerified": false, "networkPairingShareQrVerified": false})
}
