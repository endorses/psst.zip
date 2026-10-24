package androidrelease

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/artifact"
	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func (t Tool) env(name string) string {
	if value, ok := t.Runner.Env[name]; ok {
		return value
	}
	return os.Getenv(name)
}
func outside(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
func exists(path string) bool { _, err := os.Lstat(path); return !errors.Is(err, os.ErrNotExist) }
func (t Tool) SignAPK(ctx context.Context, unsigned, output, sdk string) (err error) {
	for _, name := range []string{"ANDROID_RELEASE_KEY_ALIAS", "ANDROID_RELEASE_STORE_PASSWORD", "ANDROID_RELEASE_KEY_PASSWORD"} {
		if t.env(name) == "" {
			return errors.New("release signing credentials are incomplete")
		}
	}
	if exists(output) {
		return errors.New("signed output already exists")
	}
	info, err := os.Lstat(unsigned)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxAPK {
		return errors.New("invalid unsigned APK input")
	}
	unsignedAbs, _ := filepath.Abs(unsigned)
	outputAbs, _ := filepath.Abs(output)
	if unsignedAbs == outputAbs {
		return errors.New("signed output must differ from unsigned APK")
	}
	temporary, err := os.MkdirTemp("", "psst-android-sign-")
	if err != nil {
		return errors.New("private signing directory could not be created")
	}
	defer os.RemoveAll(temporary)
	keystore := t.env("ANDROID_RELEASE_KEYSTORE")
	encoded := t.env("ANDROID_RELEASE_KEYSTORE_BASE64")
	if encoded != "" {
		if keystore != "" || len(encoded) > 1<<20 || strings.ContainsAny(encoded, " \t\r\n") {
			return errors.New("invalid private signing keystore input")
		}
		decoded, decodeErr := base64.StdEncoding.Strict().DecodeString(encoded)
		if decodeErr != nil || len(decoded) == 0 {
			return errors.New("invalid private signing keystore input")
		}
		keystore = filepath.Join(temporary, "release.keystore")
		if err = os.WriteFile(keystore, decoded, 0600); err != nil {
			return errors.New("private signing keystore could not be prepared")
		}
	}
	if keystore == "" {
		return errors.New("release signing credentials are incomplete")
	}
	keystore, err = filepath.EvalSymlinks(keystore)
	if err != nil {
		return errors.New("signing keystore is unavailable")
	}
	keystore, err = filepath.Abs(keystore)
	root, rootErr := filepath.EvalSymlinks(t.Root)
	if err != nil || rootErr != nil || !outside(root, keystore) {
		return errors.New("signing keystore must be outside source checkout")
	}
	if info, err = os.Stat(keystore); err != nil || !info.Mode().IsRegular() {
		return errors.New("signing keystore is not regular")
	}
	if err = RequireUnsigned(unsigned); err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(output), 0700); err != nil {
		return errors.New("signing output directory could not be created")
	}
	defer func() {
		if err != nil {
			_ = os.Remove(output)
		}
	}()
	t.Runner = t.Runner.With(map[string]string{"ANDROID_RELEASE_KEYSTORE_BASE64": ""})
	aligned := filepath.Join(temporary, "aligned.apk")
	if _, err = t.command(ctx, 120*time.Second, filepath.Join(sdk, "zipalign"), "-P", "16", "-f", "4", unsigned, aligned); err != nil {
		return err
	}
	signing := t
	signing.Runner = signing.Runner.With(map[string]string{"ANDROID_RELEASE_KEYSTORE_BASE64": "", "ANDROID_RELEASE_KEYSTORE": keystore})
	_, err = signing.command(ctx, 120*time.Second, filepath.Join(sdk, "apksigner"), "sign", "--ks", keystore, "--ks-key-alias", t.env("ANDROID_RELEASE_KEY_ALIAS"), "--ks-pass", "env:ANDROID_RELEASE_STORE_PASSWORD", "--key-pass", "env:ANDROID_RELEASE_KEY_PASSWORD", "--alignment-preserved", "true", "--v4-signing-enabled", "false", "--debuggable-apk-permitted", "false", "--out", output, aligned)
	return err
}
func (t Tool) PackageRelease(ctx context.Context, apk, verification, tag, revision, output string) (err error) {
	data, err := safeRead(verification, 1<<20)
	if err != nil {
		return err
	}
	var report Verification
	if err = contract.Decode(data, 1<<20, &report); err != nil || report.Schema != 1 || !report.Signed || report.Revision != revision || !revisionPattern.MatchString(revision) || report.ApplicationID != Package || report.Signer == nil || tag != "android-v"+report.Name {
		return errors.New("only exact-source verified signed Android APKs can be packaged")
	}
	if _, err = CertificateDigest(*report.Signer); err != nil {
		return err
	}
	head, err := t.git(ctx, "rev-parse", "HEAD")
	if err != nil || strings.TrimSpace(head) != revision {
		return errors.New("source archive checkout mismatch")
	}
	if exists(output) {
		return errors.New("publication directory already exists")
	}
	expected, err := VersionMetadata(filepath.Join(t.Root, "android/release-version.properties"))
	if err != nil || expected != report.Version || report.Size <= 0 || report.Size > maxAPK {
		return errors.New("Android package version/size differs from selected source")
	}
	if err = os.MkdirAll(output, 0755); err != nil {
		return errors.New("publication directory could not be created")
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(output)
		}
	}()
	apkName := "psst.zip-" + report.Name + ".apk"
	if err = copyVerified(apk, filepath.Join(output, apkName), report.Size, report.Digest); err != nil {
		return err
	}
	// Preserve the established flattened public manifest, including tag/APK names.
	var metadata map[string]any
	if err = contract.Decode(data, 1<<20, &metadata); err != nil {
		return err
	}
	metadata["tag"], metadata["apk"] = tag, apkName
	if err = writeJSON(filepath.Join(output, "android-release.json"), metadata); err != nil {
		return err
	}
	archivePath, err := filepath.Abs(filepath.Join(output, "source.tar.gz"))
	if err != nil {
		return errors.New("source archive path is invalid")
	}
	if _, err = t.git(ctx, "archive", "--format=tar.gz", "--prefix=psst.zip/", "--output="+archivePath, revision); err != nil {
		return err
	}
	legal := filepath.Join(t.Root, "android/app/src/main/assets/licenses")
	files := map[string]artifact.File{}
	err = filepath.WalkDir(legal, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return errors.New("legal resources unavailable")
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("legal resource contains symbolic link")
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("legal resources must be regular")
		}
		name, err := filepath.Rel(legal, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(name)] = artifact.File{Source: path, Mode: 0644}
		return nil
	})
	if err != nil {
		return err
	}
	if len(files) < len(legalNames) {
		return errors.New("Android legal resources are incomplete")
	}
	if err = artifact.Archive(filepath.Join(output, "third-party-notices.tar.gz"), "licenses", files, 0); err != nil {
		return err
	}
	instructions := fmt.Sprintf("# psst.zip Android %s\n\nAndroid versionCode: %d\n\nSource: `%s` (`%s`).\n\nSigning certificate SHA256: `%s`.\n\n", report.Name, report.Code, revision, tag, *report.Signer) +
		"Install only after checking the checksum and signing certificate. Updates require the same package, approved production signer and a higher version code. A same-signer in-place update preserves app data. Do not uninstall or clear an incompatible installation to bypass signing errors: this can destroy private encryption keys and history. Debug-to-production signing bridges are no longer supported.\n\n" +
		"Build corresponding source with the checked-in Gradle wrapper: `PSST_ANDROID_UNSIGNED_RELEASE=true ./android/gradlew -p android :app:assembleRelease :app:lintRelease --no-daemon`. Use the SDK/JDK versions in the tagged Android release workflow. Zipalign the unsigned APK and sign with your own private key; the publisher's key is intentionally excluded. Ordinary Android source builds require neither Python nor Go.\n\n" +
		"The source archive includes project source and build inputs. Retained third-party notices and source references are in `third-party-notices.tar.gz`. This release does not claim independent reproducible-build verification or availability in Google Play or F-Droid.\n"
	if err = os.WriteFile(filepath.Join(output, "INSTALL.md"), []byte(instructions), 0644); err != nil {
		return errors.New("installation instructions could not be written")
	}
	entries, err := os.ReadDir(output)
	if err != nil {
		return errors.New("publication outputs unavailable")
	}
	var lines []string
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			return errors.New("unexpected publication output")
		}
		sum, err := artifact.HashFile(filepath.Join(output, entry.Name()))
		if err != nil {
			return errors.New("publication output could not be hashed")
		}
		lines = append(lines, sum+"  "+entry.Name()+"\n")
	}
	return os.WriteFile(filepath.Join(output, "SHA256SUMS"), []byte(strings.Join(lines, "")), 0644)
}
func copyVerified(source, destination string, size int64, expected string) error {
	before, err := os.Lstat(source)
	if err != nil || !before.Mode().IsRegular() {
		return errors.New("APK source must be regular")
	}
	in, err := os.Open(source)
	if err != nil {
		return errors.New("artifact could not be opened")
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return errors.New("artifact output already exists or could not be created")
	}
	info, statErr := in.Stat()
	if statErr != nil || !info.Mode().IsRegular() || !os.SameFile(before, info) || info.Size() != size {
		_ = out.Close()
		_ = os.Remove(destination)
		return errors.New("APK changed after verification")
	}
	hash := sha256.New()
	count, copyErr := io.Copy(io.MultiWriter(out, hash), io.LimitReader(in, size+1))
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil || count != size || hex.EncodeToString(hash.Sum(nil)) != expected {
		_ = os.Remove(destination)
		return errors.New("APK changed after verification or copy failed")
	}
	return nil
}

type TimingGroup struct {
	Duration int64 `json:"duration_ms"`
	Tasks    int   `json:"tasks"`
	Failures int   `json:"failures"`
}
type Timings struct {
	Schema      int                     `json:"schema_version"`
	Measurement string                  `json:"measurement"`
	Count       int                     `json:"task_count"`
	Groups      map[string]*TimingGroup `json:"groups"`
}

var timingGroups = []string{"R8 shrinking", "Lint", "Source tests", "Compile and assembly", "Other tasks"}

func ReportTimings(path string) (Timings, error) {
	report := Timings{Schema: 1, Measurement: "sum of observed task durations; parallel tasks can overlap", Groups: map[string]*TimingGroup{}}
	for _, name := range timingGroups {
		report.Groups[name] = &TimingGroup{}
	}
	data, err := safeRead(path, 4<<20)
	if err != nil {
		return report, err
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 1024), 1025)
	pattern := regexp.MustCompile(`^:[A-Za-z0-9_.:-]{1,240}$`)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) > 1024 {
			return report, errors.New("task timing record exceeds bounds")
		}
		var record struct {
			Task     string `json:"task"`
			Duration *int64 `json:"duration_ms"`
			Success  *bool  `json:"success"`
		}
		if err = contract.Decode(line, 1024, &record); err != nil || !pattern.MatchString(record.Task) || record.Duration == nil || *record.Duration < 0 || *record.Duration > 24*60*60*1000 || record.Success == nil {
			return report, errors.New("invalid public task timing record")
		}
		task := strings.ToLower(record.Task[strings.LastIndex(record.Task, ":")+1:])
		group := "Other tasks"
		switch {
		case strings.Contains(task, "r8") || strings.Contains(task, "minify"):
			group = "R8 shrinking"
		case strings.Contains(task, "lint"):
			group = "Lint"
		case strings.HasPrefix(task, "test") || strings.Contains(task, "unittest"):
			group = "Source tests"
		default:
			for _, word := range []string{"compile", "assemble", "package", "dex", "merge", "process", "generate", "ksp"} {
				if strings.Contains(task, word) {
					group = "Compile and assembly"
					break
				}
			}
		}
		g := report.Groups[group]
		g.Duration += *record.Duration
		g.Tasks++
		if !*record.Success {
			g.Failures++
		}
		report.Count++
		if report.Count > 10000 {
			return report, errors.New("too many task timing records")
		}
	}
	if scanner.Err() != nil || report.Count == 0 {
		return report, errors.New("task timing evidence is missing or invalid")
	}
	return report, nil
}
func TimingSummary(report Timings) string {
	var output strings.Builder
	output.WriteString("\nAndroid Gradle task timings (task sums can overlap; total build wall time is reported separately):\n\n| Work | Observed task seconds | Tasks | Failures |\n| --- | ---: | ---: | ---: |\n")
	for _, name := range timingGroups {
		g := report.Groups[name]
		fmt.Fprintf(&output, "| %s | %.3f | %d | %d |\n", name, float64(g.Duration)/1000, g.Tasks, g.Failures)
	}
	return output.String()
}
