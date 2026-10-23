package scanning

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/github"
	"github.com/endorses/psst.zip/tools/release/internal/process"
	"github.com/endorses/psst.zip/tools/release/internal/source"
)

type SourceEvidence struct {
	Measurement []byte
	Directory   string
}
type SourceAggregationOptions struct {
	Root               string
	Binding            github.Binding
	Subjects           map[string]string
	Sources            map[string]SourceEvidence
	Application        source.ApplicationVerification
	GoImage, NodeImage string
	ResolvedBases      []byte
	Authenticate       func(context.Context, []byte) error
}

func sourceRaw(directory string, inventory any) (map[string][]byte, error) {
	items := object(inventory)
	if len(items) == 0 || len(items) > 1024 {
		return nil, fmt.Errorf("source raw inventory missing or exceeds bounds")
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("unsafe source report directory")
	}
	result := map[string][]byte{}
	var total int64
	for name, value := range items {
		if !rawFilename(name) || !digestPattern.MatchString(text(value)) {
			return nil, fmt.Errorf("source raw file identity invalid")
		}
		raw, err := bounded(filepath.Join(directory, name), scannerRawLimit)
		if err != nil {
			return nil, err
		}
		total += int64(len(raw))
		if total > 512<<20 || hash(raw) != value {
			return nil, fmt.Errorf("raw source bytes changed or exceed bounds")
		}
		result[name] = raw
	}
	return result, nil
}
func sourceBuilder(raw any, image, platform string) error {
	value := object(raw)
	if !exactFields(value, "reference", "config_digest", "architecture", "rootfs_layers", "docker_engine_architecture") {
		return fmt.Errorf("source builder metadata incomplete")
	}
	arch := strings.TrimPrefix(platform, "linux/")
	if value["reference"] != image || value["architecture"] != arch || !digestPattern.MatchString(text(value["config_digest"])) || value["docker_engine_architecture"] != arch && !(arch == "amd64" && value["docker_engine_architecture"] == "x86_64") && !(arch == "arm64" && value["docker_engine_architecture"] == "aarch64") {
		return fmt.Errorf("source builder differs from exact native resolution")
	}
	layers, err := sourceStrings(value["rootfs_layers"])
	if err != nil || len(layers) == 0 {
		return fmt.Errorf("source builder layers missing")
	}
	for _, layer := range layers {
		if !digestPattern.MatchString(layer) {
			return fmt.Errorf("source builder layer identity invalid")
		}
	}
	return nil
}
func sourceCommon(raw map[string][]byte, identity Source, inputs map[string]string, image string) (NPMMeasurement, error) {
	var result NPMMeasurement
	if len(raw) != len(npmFiles)+1 {
		return result, fmt.Errorf("common npm raw inventory differs")
	}
	encoded, ok := raw["common-npm-audit.json"]
	if !ok {
		return result, fmt.Errorf("common npm measurement missing")
	}
	if err := contract.Decode(encoded, scannerRawLimit, &result); err != nil {
		return result, err
	}
	value, err := sourceJSON(encoded)
	if err != nil {
		return result, err
	}
	if !exactFields(value, "schema_version", "kind", "source", "execution", "platform", "source_inputs", "builder", "started_at", "completed_at", "raw_files", "publication_authorized") || result.SchemaVersion != 1 || result.Kind != "common-npm-lock-scanner-measurement" || result.Execution != "common-lock" || result.Source != identity || value["publication_authorized"] != false || result.Platform != "linux/amd64" && result.Platform != "linux/arm64" || len(result.SourceInputs) != 2 || len(result.RawFiles) != len(npmFiles) {
		return result, fmt.Errorf("common npm exact source/lock identity differs")
	}
	if err := sourceBuilder(value["builder"], image, result.Platform); err != nil {
		return result, err
	}
	start, err := scanTime(value["started_at"])
	if err != nil {
		return result, err
	}
	end, err := scanTime(value["completed_at"])
	if err != nil || end.Before(start) {
		return result, fmt.Errorf("common npm timestamps inconsistent")
	}
	for _, name := range []string{"web/package.json", "web/package-lock.json"} {
		if !digestPattern.MatchString(inputs[name]) || result.SourceInputs[name] != inputs[name] {
			return result, fmt.Errorf("common npm source lock differs")
		}
	}
	for _, name := range npmFiles {
		body, ok := raw[name]
		if !ok || result.RawFiles[name] != hash(body) {
			return result, fmt.Errorf("common npm retained raw bytes differ")
		}
	}
	return result, nil
}
func sourceMain(ctx context.Context, mod []byte) (string, error) {
	dir, err := os.MkdirTemp("", "psst-source-module-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	if err = os.WriteFile(filepath.Join(dir, "go.mod"), mod, 0600); err != nil {
		return "", err
	}
	raw, err := (process.Runner{Dir: dir, CleanEnv: true, Env: map[string]string{"PATH": os.Getenv("PATH"), "GOWORK": "off", "GOTOOLCHAIN": "local", "GOPROXY": "off"}}).Output(ctx, 20*time.Second, "go", "mod", "edit", "-json")
	if err != nil {
		return "", err
	}
	var result struct {
		Module  struct{ Path string }
		Replace []any
		Exclude []any
	}
	if json.Unmarshal(raw, &result) != nil || !validImport(result.Module.Path) || len(result.Replace) > 0 || len(result.Exclude) > 0 {
		return "", fmt.Errorf("committed source module identity invalid or replaced")
	}
	return result.Module.Path, nil
}
func sourceGoFacts(raw map[string][]byte, main string, sumBytes []byte, platform string) ([]map[string]any, error) {
	env, err := sourceJSON(raw["go-env.json"])
	if err != nil {
		return nil, err
	}
	arch := strings.TrimPrefix(platform, "linux/")
	variant, key := "v1", "GOAMD64"
	if arch == "arm64" {
		variant, key = "v8.0", "GOARM64"
	}
	if env["CGO_ENABLED"] != "0" || env["GOEXPERIMENT"] != nil && env["GOEXPERIMENT"] != "" || env[key] != variant {
		return nil, fmt.Errorf("source graph package-selection settings unsupported")
	}
	all, allModules, err := GraphPaths(raw["go-all-graph.json"])
	if err != nil {
		return nil, err
	}
	server, serverModules, err := GraphPaths(raw["go-server-graph.json"])
	if err != nil {
		return nil, err
	}
	if !slices.Contains(server, main+"/cmd/server") {
		return nil, fmt.Errorf("server graph omits exact executable root")
	}
	modules, err := sourceStream(raw["go-modules.json"])
	if err != nil {
		return nil, err
	}
	selected := map[string]map[string]any{}
	for _, item := range modules {
		name := text(item["Path"])
		if !validImport(name) || selected[name] != nil || item["Replace"] != nil || item["Error"] != nil {
			return nil, fmt.Errorf("source module missing, duplicated or replaced")
		}
		selected[name] = item
	}
	if selected[main]["Main"] != true {
		return nil, fmt.Errorf("source module graph differs from committed module")
	}
	sums := map[string]bool{}
	for _, line := range strings.Split(string(sumBytes), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 {
			sums[strings.Join(fields, " ")] = true
		} else if len(fields) != 0 {
			return nil, fmt.Errorf("invalid committed Go checksum")
		}
	}
	for _, graph := range []map[string]GraphModule{allModules, serverModules} {
		for name, module := range graph {
			item := selected[name]
			if item == nil || text(item["Version"]) != module.Version {
				return nil, fmt.Errorf("imported source module differs from selected graph")
			}
			if name == main {
				continue
			}
			sum := text(item["Sum"])
			if !sourceModuleSum.MatchString(sum) || !sums[name+" "+module.Version+" "+sum] || sum != module.Sum {
				return nil, fmt.Errorf("imported source module differs from exact committed checksum")
			}
		}
	}
	messages, err := sourceStream(raw["govulncheck.json"])
	if err != nil {
		return nil, err
	}
	advisories := map[string]map[string]any{}
	findings := []map[string]any{}
	for _, m := range messages {
		if advisory := object(m["osv"]); advisory != nil {
			advisories[text(advisory["id"])] = advisory
		}
		if finding := object(m["finding"]); finding != nil {
			findings = append(findings, finding)
		}
	}
	imported := sourceSet(append(all, server...))
	result := []map[string]any{}
	for _, finding := range findings {
		id := text(finding["osv"])
		advisory := advisories[id]
		if !sourceGoID.MatchString(id) || advisory == nil || advisory["withdrawn"] != nil && advisory["withdrawn"] != "" {
			return nil, fmt.Errorf("source finding official active advisory missing")
		}
		if _, err := scanTime(advisory["modified"]); err != nil {
			return nil, err
		}
		trace, ok := finding["trace"].([]any)
		if !ok || len(trace) != 1 {
			return nil, fmt.Errorf("package/symbol source findings require repair and rescan")
		}
		frame := object(trace[0])
		if !exactFields(frame, "module", "version") {
			return nil, fmt.Errorf("uncertain source finding frame requires repair")
		}
		module, version := text(frame["module"]), text(frame["version"])
		if module == "" || version == "" || module == "stdlib" && version != "v"+strings.TrimPrefix(CompilerVersion, "go") || module != "stdlib" && (selected[module] == nil || selected[module]["Version"] != version) {
			return nil, fmt.Errorf("source finding module/version differs from actual graph")
		}
		affected := map[string]bool{}
		entries, ok := advisory["affected"].([]any)
		if !ok || len(entries) == 0 {
			return nil, fmt.Errorf("official affected source paths missing")
		}
		for _, v := range entries {
			entry := object(v)
			pkg := object(entry["package"])
			if entry == nil || pkg == nil {
				return nil, fmt.Errorf("malformed official source advisory affected entry")
			}
			if pkg["ecosystem"] != "Go" || pkg["name"] != module {
				continue
			}
			imports, ok := object(entry["ecosystem_specific"])["imports"].([]any)
			if !ok || len(imports) == 0 {
				return nil, fmt.Errorf("official source import coverage incomplete")
			}
			for _, v := range imports {
				name := text(object(v)["path"])
				if !validImport(name) || !regexp.MustCompile(`^[A-Za-z0-9_~.+/-]+$`).MatchString(name) || module != "stdlib" && name != module && !strings.HasPrefix(name, module+"/") {
					return nil, fmt.Errorf("official affected source import is uncertain")
				}
				affected[name] = true
			}
		}
		if len(affected) == 0 {
			return nil, fmt.Errorf("official source affected package coverage empty")
		}
		for name := range affected {
			if imported[name] {
				return nil, fmt.Errorf("affected source package is present; repair and rescan required")
			}
		}
		result = append(result, map[string]any{"id": id, "finding": finding, "finding_sha256": hash(legacyJSON(finding)), "advisory_sha256": hash(legacyJSON(advisory)), "official_advisory": map[string]any{"id": id, "origin": "https://vuln.go.dev", "modified": advisory["modified"], "raw_report_sha256": hash(raw["govulncheck.json"])}, "affected_packages": sourceSorted(affected), "disposition": "not-applicable", "reason": "Every official affected Go package across all advisory ranges is absent from both exact native all-root and server graphs; this is a module-only finding.", "all_root_graph_sha256": hash(raw["go-all-graph.json"]), "server_graph_sha256": hash(raw["go-server-graph.json"])})
	}
	return result, nil
}

// AggregateSources derives source-scanners details from authenticated native Go
// graphs and one shared exact-lock npm audit. Caller finding dispositions never
// replace raw official advisory/import evidence.
func AggregateSources(ctx context.Context, o SourceAggregationOptions) (map[string]any, error) {
	if err := o.Binding.Validate(); err != nil {
		return nil, err
	}
	selected := source.Context{Repository: o.Binding.Repository, Version: o.Binding.Version, Commit: o.Binding.Commit}
	if o.Application.Source != selected || len(o.Sources) != 2 || !strings.HasPrefix(o.GoImage, "docker.io/library/golang@") || !digestPattern.MatchString(strings.TrimPrefix(o.GoImage, "docker.io/library/golang@")) || !strings.HasPrefix(o.NodeImage, "docker.io/library/node@") || !digestPattern.MatchString(strings.TrimPrefix(o.NodeImage, "docker.io/library/node@")) {
		return nil, fmt.Errorf("source scanner aggregation requires exact application and both native graphs")
	}
	authenticate := o.Authenticate
	if authenticate == nil {
		client := github.New(o.Root)
		authenticate = func(ctx context.Context, raw []byte) error { return client.VerifyBytes(ctx, raw, o.Binding, nil) }
	}
	if err := authenticate(ctx, o.ResolvedBases); err != nil {
		return nil, err
	}
	bases, err := sourceJSON(o.ResolvedBases)
	if err != nil {
		return nil, err
	}
	if bases["schema_version"] != float64(1) || bases["kind"] != "release-candidate" || bases["candidate_only"] != true || bases["version"] != o.Binding.Version || bases["source_commit"] != o.Binding.Commit || object(bases["base_images"])["golang"] != o.GoImage || object(bases["base_images"])["node"] != o.NodeImage {
		return nil, fmt.Errorf("source builder resolution belongs to another exact source")
	}
	inputs := map[string]string{}
	for name, body := range o.Application.Files {
		if strings.HasPrefix(name, "backend/") || strings.HasPrefix(name, "web/") {
			inputs[name] = hash(body)
		}
	}
	main, err := sourceMain(ctx, o.Application.Files["backend/go.mod"])
	if err != nil {
		return nil, err
	}
	measured := map[string]map[string]any{"backend-source": {}, "web-source": {}}
	combined := map[string][]map[string]any{"backend-source": {}, "web-source": {}}
	archive, commonDigest := "", ""
	commonCount := 0
	for _, platform := range []string{"linux/amd64", "linux/arm64"} {
		evidence, ok := o.Sources[platform]
		if !ok {
			return nil, fmt.Errorf("native source scan missing")
		}
		if err := authenticate(ctx, evidence.Measurement); err != nil {
			return nil, err
		}
		identity := NativeIdentity{Source{o.Binding.Repository, o.Binding.Version, o.Binding.Commit}, platform}
		record, err := ReadSourceMeasurement(evidence.Measurement, identity, o.GoImage, o.NodeImage)
		if err != nil {
			return nil, err
		}
		if !reflect.DeepEqual(record.Inputs, inputs) {
			return nil, fmt.Errorf("source scan input bytes differ from exact application snapshot")
		}
		if archive == "" {
			archive = record.ArchiveSHA256
		} else if archive != record.ArchiveSHA256 {
			return nil, fmt.Errorf("native source archive identities differ")
		}
		completed, _ := time.Parse(time.RFC3339Nano, record.Completed)
		for _, scan := range record.Scans {
			target := text(scan["target"])
			raw, err := sourceRaw(filepath.Join(evidence.Directory, target), scan["raw_files"])
			if err != nil {
				return nil, err
			}
			image, builderPlatform := o.GoImage, platform
			if target == "web-source" {
				image = o.NodeImage
			}
			if target == "web-source" && record.Schema == 2 {
				common, err := sourceCommon(raw, identity.Source, inputs, o.NodeImage)
				if err != nil {
					return nil, err
				}
				digest := hash(raw["common-npm-audit.json"])
				commonRaw, _ := contract.JSON(common.Builder)
				var commonBuilder map[string]any
				json.Unmarshal(commonRaw, &commonBuilder)
				if !sourceSame(scan["common_lock"], map[string]string{"platform": common.Platform, "measurement_sha256": digest}) || !sourceSame(scan["builder"], commonBuilder) || scan["started_at"] != common.Started || scan["scanned_at"] != common.Completed {
					return nil, fmt.Errorf("common npm mapping differs from actual retained collector")
				}
				if commonDigest == "" {
					commonDigest = digest
				} else if commonDigest != digest {
					return nil, fmt.Errorf("native graphs do not share one common npm audit")
				}
				commonCount++
				builderPlatform = common.Platform
			}
			if err := sourceBuilder(scan["builder"], image, builderPlatform); err != nil {
				return nil, err
			}
			start, err := scanTime(scan["started_at"])
			if err != nil {
				return nil, err
			}
			end, err := scanTime(scan["scanned_at"])
			if err != nil || end.Before(start) || completed.Before(end) {
				return nil, fmt.Errorf("source execution timestamps inconsistent")
			}
			var calculated map[string]any
			if target == "backend-source" {
				calculated, err = AnalyzeGo(raw, platform)
			} else {
				calculated, err = AnalyzeNPM(raw, o.Application.Files["web/package-lock.json"])
			}
			if err != nil {
				return nil, err
			}
			keys := []string{"target", "subject", "started_at", "scanned_at", "builder", "raw_files"}
			if target == "web-source" && record.Schema == 2 {
				keys = append(keys, "common_lock")
			}
			for name := range calculated {
				keys = append(keys, name)
			}
			if !exactFields(scan, keys...) {
				return nil, fmt.Errorf("source scan complete field inventory differs")
			}
			for name, want := range calculated {
				if !sourceSame(scan[name], want) {
					return nil, fmt.Errorf("source measurement altered raw findings, scanner, database or graph facts")
				}
			}
			findings := []map[string]any{}
			if target == "backend-source" {
				findings, err = sourceGoFacts(raw, main, o.Application.Files["backend/go.sum"], platform)
				if err != nil {
					return nil, err
				}
			} else if len(calculated["findings"].([]map[string]any)) > 0 {
				return nil, fmt.Errorf("npm source findings require repair and rescan")
			}
			row := map[string]any{}
			for name, value := range calculated {
				row[name] = value
			}
			row["findings"] = findings
			row["builder"] = scan["builder"]
			row["started_at"] = scan["started_at"]
			row["scanned_at"] = scan["scanned_at"]
			row["completed_at"] = record.Completed
			row["measurement_sha256"] = hash(evidence.Measurement)
			row["raw_files"] = scan["raw_files"]
			measured[target][platform] = row
			for _, finding := range findings {
				copy := map[string]any{}
				for name, v := range finding {
					copy[name] = v
				}
				copy["platform"] = platform
				combined[target] = append(combined[target], copy)
			}
		}
	}
	if commonCount != 0 && commonCount != 2 {
		return nil, fmt.Errorf("both native measurements must bind the same common audit")
	}
	scans := []any{}
	for _, target := range []string{"backend-source", "web-source"} {
		versions := map[string]bool{}
		database := map[string]any{}
		latest := ""
		var latestTime time.Time
		for platform, item := range measured[target] {
			row := object(item)
			versions[text(object(row["scanner"])["version"])] = true
			database[platform] = row["database"]
			at, _ := scanTime(row["scanned_at"])
			if latest == "" || at.After(latestTime) {
				latest = text(row["scanned_at"])
				latestTime = at
			}
		}
		scanner := "govulncheck"
		if target == "web-source" {
			scanner = "npm audit"
		}
		scans = append(scans, map[string]any{"target": target, "subject": "git:" + o.Binding.Repository + "@" + o.Binding.Commit, "status": "complete", "exit_code": 0, "scanner": scanner, "version": strings.Join(sourceSorted(versions), ","), "database": hash(legacyJSON(database)), "scanned_at": latest, "findings": combined[target], "native_measurements": measured[target]})
	}
	policy := "authenticated-both-native-source-graphs-official-Go-package-absence-and-zero-npm-v1"
	if commonCount > 0 {
		policy = "authenticated-native-Go-graphs-and-common-lock-npm-v2"
	}
	return map[string]any{"scans": scans, "release_subjects": o.Subjects, "source_archive_sha256": archive, "source_inputs": inputs, "resolved_bases_sha256": hash(o.ResolvedBases), "derivation_policy": policy}, nil
}
