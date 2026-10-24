package source

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha512"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/process"
)

var caddyImageRE = regexp.MustCompile(`^(?:docker.io/library/)?caddy(?:[:][A-Za-z0-9._-]+)?@(sha256:[a-f0-9]{64})$`)
var caddyRecipeRE = regexp.MustCompile(`^https://github.com/caddyserver/caddy-docker/blob/([a-f0-9]{40})/([A-Za-z0-9._/-]+)$`)

const caddyCosignVersion = "v2.6.5"
const caddyCosignCommit = "3e82f50a2839855693aacf7b3d0e7e2f30774cb4"

var caddyCosignHashes = map[string]string{"linux/amd64": "sha256:c3b4f5410e608af03a5eb0aaac84a4313d8da131248e08ff1759ac70c79d1644", "linux/arm64": "sha256:426193b4c5da4d4d643e822f48fe0cc8a476ca1782a272704831f5a0cef716d7"}

type SignatureVerification struct {
	Artifact        File              `json:"artifact"`
	Certificate     File              `json:"certificate"`
	Signature       File              `json:"signature"`
	Signer          string            `json:"signer"`
	Issuer          string            `json:"issuer"`
	WorkflowCommit  string            `json:"workflow_commit"`
	Verifier        File              `json:"verifier"`
	VerifierVersion string            `json:"verifier_version"`
	Platform        string            `json:"platform"`
	Log             File              `json:"log"`
	SHA512Bindings  map[string]string `json:"sha512_bindings"`
}

func caddyRecipe(index []byte, base, arch string) (string, string, error) {
	match := caddyImageRE.FindStringSubmatch(base)
	if match == nil {
		return "", "", fmt.Errorf("Caddy base must be immutable official image")
	}
	var value struct {
		Digest    string
		Manifests []struct {
			Platform    struct{ OS, Architecture string }
			Annotations map[string]string
		}
	}
	if json.Unmarshal(index, &value) != nil || value.Digest != match[1] {
		return "", "", fmt.Errorf("Caddy index digest differs")
	}
	var source, revision string
	count := 0
	for _, m := range value.Manifests {
		if m.Platform.OS == "linux" && m.Platform.Architecture == arch {
			count++
			source = m.Annotations["org.opencontainers.image.source"]
			revision = m.Annotations["org.opencontainers.image.revision"]
		}
	}
	recipe := caddyRecipeRE.FindStringSubmatch(source)
	if count != 1 || recipe == nil || recipe[1] != revision || !safePath(recipe[2]) {
		return "", "", fmt.Errorf("official Caddy recipe is unbound or ambiguous")
	}
	return recipe[1], recipe[2], nil
}
func originalTarFiles(raw []byte, capture func(string) bool) (map[string][]byte, error) {
	if len(raw) == 0 || int64(len(raw)) > OriginalLimit {
		return nil, fmt.Errorf("invalid original tar size")
	}
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	limit := &io.LimitedReader{R: gz, N: (2 << 30) + 1}
	reader := tar.NewReader(limit)
	seen := map[string]bool{}
	result := map[string][]byte{}
	for count := 0; ; count++ {
		h, e := reader.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, e
		}
		name := strings.TrimSuffix(h.Name, "/")
		if !safePath(name) || seen[name] || count >= 100000 || h.Size < 0 || h.Size > OriginalLimit {
			return nil, fmt.Errorf("unsafe original source member")
		}
		seen[name] = true
		if h.Typeflag == tar.TypeReg && capture(name) {
			body, e := io.ReadAll(io.LimitReader(reader, OriginalLimit+1))
			if e != nil || int64(len(body)) != h.Size {
				return nil, fmt.Errorf("original source member read failed")
			}
			result[name] = body
		}
	}
	if _, err = io.Copy(io.Discard, limit); err != nil || limit.N <= 0 {
		return nil, fmt.Errorf("original source expansion failed or exceeded bounds")
	}
	return result, nil
}
func soleSuffix(files map[string][]byte, suffix string, exact bool) ([]byte, error) {
	var result []byte
	count := 0
	for name, raw := range files {
		if name == suffix || !exact && strings.HasSuffix(name, "/"+suffix) {
			count++
			result = raw
		}
	}
	if count != 1 {
		return nil, fmt.Errorf("original source member missing or ambiguous")
	}
	return result, nil
}
func verifyCaddyModules(binary, source []byte) (string, string, error) {
	build, err := buildinfo.Read(bytes.NewReader(binary))
	if err != nil || build.Main.Path != "caddy" || build.Main.Version != "(devel)" || len(build.Deps) == 0 {
		return "", "", fmt.Errorf("unsupported official Caddy binary metadata")
	}
	files, err := originalTarFiles(source, func(name string) bool { return name == "go.sum" || name == "vendor/modules.txt" })
	if err != nil {
		return "", "", err
	}
	sums, err := moduleSums(files["go.sum"])
	if err != nil {
		return "", "", err
	}
	declarations := map[string]bool{}
	for _, line := range strings.Split(string(files["vendor/modules.txt"]), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[0] == "#" {
			declarations[fields[1]+"@"+fields[2]] = true
		}
	}
	for _, dependency := range build.Deps {
		key := dependency.Path + "@" + dependency.Version
		if dependency.Replace != nil || !declarations[key] || dependency.Sum != "" && sums[key] != dependency.Sum {
			return "", "", fmt.Errorf("Caddy vendored source differs from actual binary dependencies")
		}
	}
	settings := map[string]string{}
	for _, setting := range build.Settings {
		settings[setting.Key] = setting.Value
	}
	if !commitRE.MatchString(settings["vcs.revision"]) || settings["vcs.modified"] != "false" || !goVersionRE.MatchString(build.GoVersion) {
		return "", "", fmt.Errorf("Caddy binary revision or compiler is unbound")
	}
	return settings["vcs.revision"], build.GoVersion, nil
}

type caddySignatureVerifier func(context.Context, string, string, string, string, map[string][]byte) ([]SignatureVerification, map[string][]byte, error)

func collectCaddy(ctx context.Context, image SavedImage, base, cosign string, fetch Fetch, execute func(context.Context, time.Duration, ...string) ([]byte, error)) (Source, map[string][]byte, []SignatureVerification, error) {
	return retainedCaddy(ctx, image, base, cosign, fetch, execute, verifyCaddySignatures)
}

func retainedCaddy(ctx context.Context, image SavedImage, base, cosign string, fetch Fetch, execute func(context.Context, time.Duration, ...string) ([]byte, error), verifySignatures caddySignatureVerifier) (Source, map[string][]byte, []SignatureVerification, error) {
	var empty Source
	if fetch == nil {
		fetch = publicFetch
	}
	index, err := execute(ctx, 55*time.Second, "docker", "buildx", "imagetools", "inspect", base, "--format", "{{json .Manifest}}")
	if err != nil {
		return empty, nil, nil, err
	}
	recipeCommit, directory, err := caddyRecipe(index, base, image.Architecture)
	if err != nil {
		return empty, nil, nil, err
	}
	recipeURL := "https://raw.githubusercontent.com/caddyserver/caddy-docker/" + recipeCommit + "/" + directory + "/Dockerfile"
	dockerfile, err := fetch(ctx, recipeURL, 8<<20)
	if err != nil {
		return empty, nil, nil, err
	}
	versionMatch := regexp.MustCompile(`(?m)^ENV CADDY_VERSION[ =](v[0-9]+\.[0-9]+\.[0-9]+)$`).FindSubmatch(dockerfile)
	distMatch := regexp.MustCompile(`caddyserver/dist/(?:archive|raw)/([a-f0-9]{40})(?:\.tar\.gz|/)`).FindSubmatch(dockerfile)
	if versionMatch == nil || distMatch == nil {
		return empty, nil, nil, fmt.Errorf("Caddy recipe version/distribution inputs are unbound")
	}
	version, distCommit := string(versionMatch[1]), string(distMatch[1])
	releaseURL := "https://api.github.com/repos/caddyserver/caddy/releases/tags/" + version
	release, err := fetch(ctx, releaseURL, MetadataLimit)
	if err != nil {
		return empty, nil, nil, err
	}
	var metadata struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name               string
			BrowserDownloadURL string `json:"browser_download_url"`
			Digest             string
			Size               int64
		}
	}
	if json.Unmarshal(release, &metadata) != nil || metadata.TagName != version {
		return empty, nil, nil, fmt.Errorf("Caddy official release identity differs")
	}
	assets := map[string]struct {
		url, digest string
		size        int64
	}{}
	for _, asset := range metadata.Assets {
		if _, ok := assets[asset.Name]; ok {
			return empty, nil, nil, fmt.Errorf("duplicate Caddy release asset")
		}
		assets[asset.Name] = struct {
			url, digest string
			size        int64
		}{asset.BrowserDownloadURL, asset.Digest, asset.Size}
	}
	short := strings.TrimPrefix(version, "v")
	archiveName := "caddy_" + short + "_buildable-artifact.tar.gz"
	binaryName := "caddy_" + short + "_linux_" + image.Architecture + ".tar.gz"
	checksumsName := "caddy_" + short + "_checksums.txt"
	names := []string{archiveName, archiveName + ".sig", strings.TrimSuffix(archiveName, ".tar.gz") + ".pem", binaryName, checksumsName, checksumsName + ".sig", checksumsName + ".pem"}
	retained := map[string][]byte{}
	for _, name := range names {
		asset := assets[name]
		expectedURL := "https://github.com/caddyserver/caddy/releases/download/" + version + "/" + name
		if asset.url != expectedURL || !digestRE.MatchString(asset.digest) || asset.size <= 0 || asset.size > OriginalLimit {
			return empty, nil, nil, fmt.Errorf("required exact Caddy release asset missing")
		}
		raw, e := fetch(ctx, expectedURL, asset.size)
		if e != nil {
			return empty, nil, nil, e
		}
		if int64(len(raw)) != asset.size || hash(raw) != asset.digest {
			return empty, nil, nil, fmt.Errorf("Caddy release asset differs from official identity")
		}
		retained[name] = raw
	}
	expected := regexp.MustCompile(`binArch='` + regexp.QuoteMeta(image.Architecture) + `';\s*checksum='([a-f0-9]{128})'`).FindSubmatch(dockerfile)
	sum := sha512.Sum512(retained[binaryName])
	if expected == nil || hex.EncodeToString(sum[:]) != string(expected[1]) {
		return empty, nil, nil, fmt.Errorf("official Caddy binary differs from immutable recipe SHA512")
	}
	binaryFiles, err := originalTarFiles(retained[binaryName], func(name string) bool { return name == "caddy" || strings.HasSuffix(name, "/caddy") })
	if err != nil {
		return empty, nil, nil, err
	}
	upstreamBinary, err := soleSuffix(binaryFiles, "caddy", false)
	if err != nil || !bytes.Equal(upstreamBinary, image.Files["usr/bin/caddy"]) {
		return empty, nil, nil, fmt.Errorf("final Caddy executable differs from official binary input")
	}
	revision, goVersion, err := verifyCaddyModules(upstreamBinary, retained[archiveName])
	if err != nil {
		return empty, nil, nil, err
	}
	sourceMain, err := fetch(ctx, "https://raw.githubusercontent.com/caddyserver/caddy/"+revision+"/cmd/caddy/main.go", 8<<20)
	if err != nil {
		return empty, nil, nil, err
	}
	wrapper, err := originalTarFiles(retained[archiveName], func(name string) bool { return name == "main.go" })
	if err != nil || !bytes.Equal(wrapper["main.go"], sourceMain) {
		return empty, nil, nil, fmt.Errorf("Caddy build wrapper differs from executable source revision")
	}
	id := "caddy-" + version + "-" + image.Architecture
	item := Source{ID: id, Ecosystem: "caddy", Name: "caddyserver/caddy", Version: version, Origin: "caddyserver/caddy", URL: assets[archiveName].url, Original: fact("caddy/"+archiveName, retained[archiveName]), LicensePaths: []string{}, BuildInputPaths: []string{"main.go", "go.mod", "go.sum", "vendor/modules.txt"}, PreferredSourcePaths: []string{"main.go", "vendor"}, Authentication: map[string]string{"source_commit": revision, "go_version": goVersion, "recipe_commit": recipeCommit, "recipe_directory": directory, "distribution_commit": distCommit, "base_index": base}}
	files := map[string][]byte{}
	for name, raw := range retained {
		target := "caddy/" + name
		files[target] = raw
		if name != archiveName {
			item.Auxiliary = append(item.Auxiliary, fact(target, raw))
		}
	}
	for name, raw := range map[string][]byte{"index.json": index, "release.json": release, "Dockerfile": dockerfile, "main.go": sourceMain} {
		target := "caddy/evidence/" + name
		files[target] = raw
		item.Auxiliary = append(item.Auxiliary, fact(target, raw))
	}
	documents := map[string][]byte{}
	for _, repo := range []struct{ name, commit string }{{"caddy-docker", recipeCommit}, {"dist", distCommit}} {
		name := repo.name + "-" + repo.commit + ".tar.gz"
		raw, e := fetch(ctx, "https://codeload.github.com/caddyserver/"+repo.name+"/tar.gz/"+repo.commit, OriginalLimit)
		if e != nil {
			return item, nil, nil, e
		}
		files["caddy/"+name] = raw
		item.Auxiliary = append(item.Auxiliary, fact("caddy/"+name, raw))
		legal, e := sourceLegal(ctx, raw, name)
		if e != nil || len(legal.Fixtures) != 0 {
			return item, nil, nil, fmt.Errorf("Caddy recipe source notices or nested originals failed")
		}
		for label, body := range legal.Documents {
			documents[name+"::"+label] = body
		}
	}
	legal, err := sourceLegal(ctx, retained[archiveName], archiveName)
	if err != nil || len(legal.Fixtures) != 0 {
		return item, nil, nil, fmt.Errorf("Caddy preferred-source notices or nested originals failed")
	}
	for label, body := range legal.Documents {
		documents[archiveName+"::"+label] = body
	}
	if len(documents) == 0 {
		return item, nil, nil, fmt.Errorf("Caddy preferred-source legal originals missing")
	}
	paths, noticeFacts, noticeFiles := retainedDocuments("notices/"+id, documents)
	item.LicensePaths = paths
	item.NoticeFiles = noticeFacts
	for name, body := range noticeFiles {
		files[name] = body
	}
	signatures, logs, err := verifySignatures(ctx, cosign, version, revision, image.Architecture, retained)
	if err != nil {
		return item, nil, nil, err
	}
	for name, body := range logs {
		files[name] = body
		item.Auxiliary = append(item.Auxiliary, fact(name, body))
	}
	sort.Slice(item.Auxiliary, func(i, j int) bool { return item.Auxiliary[i].Path < item.Auxiliary[j].Path })
	return item, files, signatures, nil
}
func verifyCaddySignatures(ctx context.Context, executable, version, revision, arch string, files map[string][]byte) ([]SignatureVerification, map[string][]byte, error) {
	tool, err := readRegular(executable, OriginalLimit)
	if err != nil {
		return nil, nil, err
	}
	platform := "linux/" + arch
	if hash(tool) != caddyCosignHashes[platform] {
		return nil, nil, fmt.Errorf("Caddy legacy verifier differs from pinned official executable")
	}
	runner := process.Runner{CleanEnv: true, Env: map[string]string{"PATH": "/usr/bin:/bin", "SIGSTORE_NO_CACHE": "1"}}
	raw, err := runner.Output(ctx, 55*time.Second, executable, "version", "--json")
	if err != nil {
		return nil, nil, err
	}
	var identity struct {
		GitVersion   string `json:"gitVersion"`
		GitCommit    string `json:"gitCommit"`
		GitTreeState string `json:"gitTreeState"`
		Platform     string `json:"platform"`
	}
	if json.Unmarshal(raw, &identity) != nil || identity.GitVersion != caddyCosignVersion || identity.GitCommit != caddyCosignCommit || identity.GitTreeState != "clean" || identity.Platform != platform {
		return nil, nil, fmt.Errorf("Caddy verifier build identity differs")
	}
	private, err := os.MkdirTemp("", "psst-caddy-signatures-")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(private)
	for name, body := range files {
		if path.Base(name) != name || !safePath(name) {
			return nil, nil, fmt.Errorf("unsafe Caddy signature input filename")
		}
		if err = os.WriteFile(filepath.Join(private, name), body, 0600); err != nil {
			return nil, nil, err
		}
	}
	short := strings.TrimPrefix(version, "v")
	archive := "caddy_" + short + "_buildable-artifact.tar.gz"
	checksums := "caddy_" + short + "_checksums.txt"
	bindings, err := caddyChecksumBindings(version, arch, files)
	if err != nil {
		return nil, nil, err
	}
	var verifications []SignatureVerification
	logs := map[string][]byte{}
	for _, name := range []string{archive, checksums} {
		certificate := strings.TrimSuffix(name, ".tar.gz") + ".pem"
		signature := name + ".sig"
		signer := "https://github.com/caddyserver/caddy/.github/workflows/release.yml@refs/tags/" + version
		args := []string{executable, "--timeout", "45s", "verify-blob", "--certificate-identity", signer, "--certificate-oidc-issuer", "https://token.actions.githubusercontent.com", "--certificate-github-workflow-sha", revision, "--certificate-github-workflow-ref", "refs/tags/" + version, "--certificate-github-workflow-repository", "caddyserver/caddy", "--certificate-github-workflow-trigger", "push", "--certificate-github-workflow-name", "Release", "--rekor-url", "https://rekor.sigstore.dev", "--insecure-ignore-tlog=false", "--insecure-ignore-sct=false", "--private-infrastructure=false", "--offline=false", "--new-bundle-format=false", "--certificate", filepath.Join(private, certificate), "--signature", filepath.Join(private, signature), filepath.Join(private, name)}
		result, e := runner.Result(ctx, 55*time.Second, args...)
		if e != nil {
			return nil, nil, fmt.Errorf("official Caddy signature verification failed: %w", e)
		}
		log, err := contract.JSON(map[string]any{"stdout": string(result.Stdout), "stderr": string(result.Stderr), "exit_code": 0})
		if err != nil {
			return nil, nil, err
		}
		logName := "caddy/evidence/" + name + ".verification.json"
		logs[logName] = log
		verifications = append(verifications, SignatureVerification{Artifact: fact("caddy/"+name, files[name]), Certificate: fact("caddy/"+certificate, files[certificate]), Signature: fact("caddy/"+signature, files[signature]), Signer: signer, Issuer: "https://token.actions.githubusercontent.com", WorkflowCommit: revision, Verifier: fact("cosign", tool), VerifierVersion: caddyCosignVersion, Platform: platform, Log: fact(logName, log), SHA512Bindings: bindings})
	}
	return verifications, logs, nil
}

func caddyChecksumBindings(version, arch string, files map[string][]byte) (map[string]string, error) {
	short := strings.TrimPrefix(version, "v")
	archive := "caddy_" + short + "_buildable-artifact.tar.gz"
	checksums := "caddy_" + short + "_checksums.txt"
	binary := "caddy_" + short + "_linux_" + arch + ".tar.gz"
	sha512Bindings := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(files[checksums])), "\n") {
		fields := strings.Split(line, "  ")
		if len(fields) != 2 || len(fields[0]) != 128 || path.Base(fields[1]) != fields[1] || !safePath(fields[1]) {
			return nil, fmt.Errorf("unsupported signed Caddy SHA512 table")
		}
		if _, ok := sha512Bindings[fields[1]]; ok {
			return nil, fmt.Errorf("duplicate signed Caddy checksum")
		}
		if _, err := hex.DecodeString(fields[0]); err != nil {
			return nil, err
		}
		sha512Bindings[fields[1]] = fields[0]
	}
	bindings := map[string]string{}
	for _, name := range []string{archive, binary} {
		sum := sha512.Sum512(files[name])
		digest := hex.EncodeToString(sum[:])
		if sha512Bindings[name] != digest {
			return nil, fmt.Errorf("Caddy original differs from signed SHA512 binding")
		}
		bindings[name] = digest
	}
	return bindings, nil
}

// AuthenticateCaddyForCompiler independently verifies the retained official
// buildable artifact, binary and signatures before compiling its vendor graph.
// Original acquisition/replay stays separate; no acquisition or OCI parse repeats.
func AuthenticateCaddyForCompiler(ctx context.Context, item Source, files map[string][]byte, binary []byte, platform, cosign string) ([]SignatureVerification, error) {
	if item.Ecosystem != "caddy" || item.Name != "caddyserver/caddy" || (platform != "linux/amd64" && platform != "linux/arm64") {
		return nil, fmt.Errorf("invalid Caddy compiler source context")
	}
	arch := strings.TrimPrefix(platform, "linux/")
	revision, version, err := verifyCaddyModules(binary, files[item.Original.Path])
	if err != nil || revision != item.Authentication["source_commit"] || version != item.Authentication["go_version"] {
		return nil, fmt.Errorf("retained Caddy source differs from actual executable compiler metadata")
	}
	facts := map[string]File{item.Original.Path: item.Original}
	for _, f := range item.Auxiliary {
		if _, exists := facts[f.Path]; exists {
			return nil, fmt.Errorf("duplicate retained Caddy input")
		}
		facts[f.Path] = f
	}
	short := strings.TrimPrefix(item.Version, "v")
	original := "caddy_" + short + "_buildable-artifact.tar.gz"
	upstream := "caddy_" + short + "_linux_" + arch + ".tar.gz"
	sums := "caddy_" + short + "_checksums.txt"
	retained := map[string][]byte{}
	for _, name := range []string{original, original + ".sig", strings.TrimSuffix(original, ".tar.gz") + ".pem", upstream, sums, sums + ".sig", sums + ".pem"} {
		target := "caddy/" + name
		body, ok := files[target]
		if !ok || facts[target] != fact(target, body) {
			return nil, fmt.Errorf("retained Caddy signed original changed")
		}
		retained[name] = body
	}
	members, err := originalTarFiles(retained[upstream], func(name string) bool { return name == "caddy" || strings.HasSuffix(name, "/caddy") })
	if err != nil {
		return nil, err
	}
	official, err := soleSuffix(members, "caddy", false)
	if err != nil || !bytes.Equal(official, binary) {
		return nil, fmt.Errorf("Caddy compiler input differs from signed official binary")
	}
	result, _, err := verifyCaddySignatures(ctx, cosign, item.Version, revision, arch, retained)
	return result, err
}

// CaddyCompilerModules reads only the two authenticated vendor graph inputs.
func CaddyCompilerModules(original []byte) (goSum, vendorModules []byte, err error) {
	files, err := originalTarFiles(original, func(name string) bool { return name == "go.sum" || name == "vendor/modules.txt" })
	if err != nil {
		return nil, nil, err
	}
	if len(files["go.sum"]) == 0 || len(files["vendor/modules.txt"]) == 0 {
		return nil, nil, fmt.Errorf("Caddy original lacks compiler graph inputs")
	}
	return files["go.sum"], files["vendor/modules.txt"], nil
}

// CaddyVerifierPolicy shares one reviewed executable identity across consumers.
func CaddyVerifierPolicy(platform string) (version, commit, digest string, err error) {
	digest = caddyCosignHashes[platform]
	if digest == "" {
		return "", "", "", fmt.Errorf("unsupported Caddy verifier platform")
	}
	return caddyCosignVersion, caddyCosignCommit, digest, nil
}
