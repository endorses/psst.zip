package androidrelease

import (
	"archive/zip"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// This optional integration reuses a non-secret retained APK as a ZIP/manifest/
// ELF fixture, changes identity only in a temporary copy, and signs with a fresh
// disposable key. It neither approves that APK for release nor touches a device.
func TestOfficialSDKRoundTrip(t *testing.T) {
	input, sdk, keytool := os.Getenv("PSST_ANDROID_FIXTURE_APK"), os.Getenv("PSST_ANDROID_SDK_TOOLS"), os.Getenv("PSST_ANDROID_FIXTURE_KEYTOOL")
	if input == "" || sdk == "" || keytool == "" {
		t.Skip("set explicit retained APK, SDK tools and keytool for disposable SDK integration")
	}
	directory := t.TempDir()
	root := filepath.Join(directory, "source")
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	tool := New(root)
	ctx := context.Background()
	output, err := tool.command(ctx, 120*time.Second, filepath.Join(sdk, "aapt2"), "dump", "badging", input)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := ParseBadging(output)
	if err != nil {
		t.Fatal(err)
	}
	revision := strings.Repeat("a", 40)
	write(t, filepath.Join(root, "android/release-version.properties"), []byte("versionName="+manifest.Name+"\nversionCode="+strconv.Itoa(manifest.Code)+"\n"))
	write(t, filepath.Join(root, "android/gradle/libs.versions.toml"), []byte("[versions]\nandroid-minSdk = \""+strconv.Itoa(manifest.MinSDK)+"\"\nandroid-targetSdk = \""+strconv.Itoa(manifest.TargetSDK)+"\"\n"))
	archive, err := zip.OpenReader(input)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	unsigned := filepath.Join(directory, "unsigned.apk")
	file, err := os.Create(unsigned)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for _, entry := range archive.File {
		header := entry.FileHeader
		destination, err := writer.CreateHeader(&header)
		if err != nil {
			t.Fatal(err)
		}
		if entry.Name == "assets/psst-release.json" {
			identity := map[string]any{"versionName": manifest.Name, "versionCode": manifest.Code, "sourceRevision": revision, "deviceFixture": true}
			data, _ := json.Marshal(identity)
			if _, err = destination.Write(data); err != nil {
				t.Fatal(err)
			}
			continue
		}
		data, err := zipPayload(entry, maxMember)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = destination.Write(data); err != nil {
			t.Fatal(err)
		}
		for _, name := range legalNames {
			if entry.Name == "assets/licenses/"+name {
				write(t, filepath.Join(root, "android/app/src/main/assets/licenses", name), data)
			}
		}
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	// Rebuilding the fixture changes offsets; official zipalign establishes the
	// same 16 KB boundary checked by the ordinary unsigned/signed verifier.
	aligned := filepath.Join(directory, "aligned.apk")
	if _, err = tool.command(ctx, 120*time.Second, filepath.Join(sdk, "zipalign"), "-P", "16", "-f", "4", unsigned, aligned); err != nil {
		t.Fatal(err)
	}
	versionFile := filepath.Join(root, "android/release-version.properties")
	if _, err = tool.VerifyAPK(ctx, aligned, sdk, "", versionFile, revision, true); err != nil {
		t.Fatal(err)
	}
	tool.Runner = tool.Runner.With(map[string]string{"PSST_FIXTURE_PASSWORD": t.Name(), "ANDROID_RELEASE_KEYSTORE_BASE64": ""})
	keystore, fingerprint, err := tool.disposableKey(ctx, keytool, directory)
	if err != nil {
		t.Fatal(err)
	}
	tool.Runner = tool.Runner.With(map[string]string{"ANDROID_RELEASE_KEYSTORE": keystore, "ANDROID_RELEASE_KEY_ALIAS": "fixture", "ANDROID_RELEASE_STORE_PASSWORD": t.Name(), "ANDROID_RELEASE_KEY_PASSWORD": t.Name()})
	signed := filepath.Join(directory, "signed.apk")
	if err = tool.SignAPK(ctx, aligned, signed, sdk); err != nil {
		t.Fatal(err)
	}
	report, err := tool.VerifyAPK(ctx, signed, sdk, fingerprint, versionFile, revision, true)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Signed || report.Signer == nil || *report.Signer != fingerprint {
		t.Fatal("official SDK signature was not retained")
	}
	if _, err = tool.VerifyAPK(ctx, signed, sdk, "", versionFile, revision, true); err == nil {
		t.Fatal("signed fixture accepted as unsigned")
	}
	if _, err = tool.VerifyAPK(ctx, signed, sdk, fingerprint, versionFile, revision, false); err == nil {
		t.Fatal("private SDK fixture accepted for publication")
	}
	// Exercise the workflow's direct protected base64 input too; no shell/Python
	// decoder handles key bytes and children never inherit the base64 secret.
	keyBytes, err := os.ReadFile(keystore)
	if err != nil {
		t.Fatal(err)
	}
	tool.Runner = tool.Runner.With(map[string]string{"ANDROID_RELEASE_KEYSTORE": "", "ANDROID_RELEASE_KEYSTORE_BASE64": base64.StdEncoding.EncodeToString(keyBytes)})
	second := filepath.Join(directory, "base64-signed.apk")
	if err = tool.SignAPK(ctx, aligned, second, sdk); err != nil {
		t.Fatal(err)
	}
	if _, err = tool.VerifyAPK(ctx, second, sdk, fingerprint, versionFile, revision, true); err != nil {
		t.Fatal(err)
	}
}
