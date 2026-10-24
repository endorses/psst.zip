package scanning

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

const GovulnVersion = "v1.8.0"
const GovulnSum = "h1:clG4qBU6zH5VKjti8n5j8BBuYzoSha392xXMkXS351U="

var sourceGoID = regexp.MustCompile(`^GO-[0-9]{4}-[0-9]+$`)
var sourceModuleSum = regexp.MustCompile(`^h1:[A-Za-z0-9+/]{43}=$`)

func sourceJSON(raw []byte) (map[string]any, error) {
	var result map[string]any
	err := contract.Decode(raw, scannerRawLimit, &result)
	if err == nil && result == nil {
		err = fmt.Errorf("source report must be a JSON object")
	}
	return result, err
}
func sourceStream(raw []byte) ([]map[string]any, error) {
	if len(raw) == 0 || len(raw) > scannerRawLimit {
		return nil, fmt.Errorf("source JSON stream missing or exceeds bounds")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	result := []map[string]any{}
	for {
		var next json.RawMessage
		err := decoder.Decode(&next)
		if err == io.EOF {
			break
		}
		if err != nil || len(result) >= 100000 {
			return nil, fmt.Errorf("source JSON stream invalid or exceeds bounds")
		}
		item, err := sourceJSON(next)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("empty source JSON stream")
	}
	return result, nil
}
func sourceExit(raw []byte) (int, error) {
	value, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return 0, fmt.Errorf("invalid source execution receipt")
	}
	return value, nil
}
func sourceSet(values []string) map[string]bool {
	result := map[string]bool{}
	for _, value := range values {
		result[value] = true
	}
	return result
}
func sourceSorted(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for name := range values {
		result = append(result, name)
	}
	slices.Sort(result)
	return result
}
func sourceStrings(value any) ([]string, error) {
	values, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("source coverage must be an array")
	}
	result := []string{}
	for _, v := range values {
		name, ok := v.(string)
		if !ok || name == "" {
			return nil, fmt.Errorf("source coverage member invalid")
		}
		result = append(result, name)
	}
	return result, nil
}
func sourceSubset(first, second map[string]bool) bool {
	for name := range first {
		if !second[name] {
			return false
		}
	}
	return true
}
func sourceSame(a, b any) bool { return bytes.Equal(legacyJSON(a), legacyJSON(b)) }

// AnalyzeGo derives scanner identity, complete source coverage and unsuppressed
// findings from the actual raw native reports. Aggregation independently checks
// every proposed package-absence disposition against committed module checksums.
func AnalyzeGo(raw map[string][]byte, platform string) (map[string]any, error) {
	if platform != "linux/amd64" && platform != "linux/arm64" {
		return nil, fmt.Errorf("invalid source Go platform")
	}
	code, err := sourceExit(raw["govulncheck.exit"])
	if err != nil || string(raw["go-execution.complete"]) != "complete\n" || code != 0 {
		return nil, fmt.Errorf("Go source scanner failed or execution incomplete")
	}
	if strings.TrimSpace(string(raw["go-version.txt"])) != "go version "+CompilerVersion+" "+platform {
		return nil, fmt.Errorf("source Go toolchain differs from release builder")
	}
	env, err := sourceJSON(raw["go-env.json"])
	if err != nil {
		return nil, err
	}
	for name, value := range map[string]string{"GOOS": "linux", "GOARCH": strings.TrimPrefix(platform, "linux/"), "GOTOOLCHAIN": "local", "GOFLAGS": "-mod=readonly", "GOPROXY": "https://proxy.golang.org", "GOSUMDB": "sum.golang.org"} {
		if env[name] != value {
			return nil, fmt.Errorf("source Go build configuration changed")
		}
	}
	module, err := sourceJSON(raw["scanner-module.json"])
	if err != nil {
		return nil, err
	}
	if module["Path"] != "golang.org/x/vuln" || module["Version"] != GovulnVersion || module["Sum"] != GovulnSum || module["Error"] != nil {
		return nil, fmt.Errorf("govulncheck source module identity differs")
	}
	build, err := sourceJSON(raw["scanner-build.json"])
	if err != nil {
		return nil, err
	}
	main := object(build["Main"])
	if build["GoVersion"] != CompilerVersion || main["Path"] != "golang.org/x/vuln" || main["Version"] != GovulnVersion || main["Sum"] != GovulnSum {
		return nil, fmt.Errorf("govulncheck executable build identity differs")
	}
	binary := strings.Fields(string(raw["scanner-binary-sha256.txt"]))
	if len(binary) == 0 || !digestPattern.MatchString("sha256:"+binary[0]) {
		return nil, fmt.Errorf("actual govulncheck binary digest missing")
	}
	roots, _, err := GraphPaths(raw["go-roots.json"])
	if err != nil {
		return nil, err
	}
	all, allModules, err := GraphPaths(raw["go-all-graph.json"])
	if err != nil {
		return nil, err
	}
	server, _, err := GraphPaths(raw["go-server-graph.json"])
	if err != nil {
		return nil, err
	}
	rootSet, allSet, serverSet := sourceSet(roots), sourceSet(all), sourceSet(server)
	if !sourceSubset(rootSet, allSet) || !sourceSubset(serverSet, allSet) {
		return nil, fmt.Errorf("source graphs omit scanned root/server targets")
	}
	messages, err := sourceStream(raw["govulncheck.json"])
	if err != nil {
		return nil, err
	}
	if !exactFields(messages[0], "config") {
		return nil, fmt.Errorf("govulncheck first configuration missing")
	}
	var config, sbom map[string]any
	advisories := map[string]any{}
	findings := []map[string]any{}
	for _, message := range messages {
		if len(message) != 1 {
			return nil, fmt.Errorf("ambiguous govulncheck message")
		}
		for kind, value := range message {
			item := object(value)
			if item == nil {
				return nil, fmt.Errorf("invalid govulncheck message body")
			}
			switch kind {
			case "config":
				if config != nil {
					return nil, fmt.Errorf("duplicate govulncheck configuration")
				}
				config = item
			case "SBOM":
				if sbom != nil {
					return nil, fmt.Errorf("duplicate govulncheck source SBOM")
				}
				sbom = item
			case "osv":
				id := text(item["id"])
				if id == "" || advisories[id] != nil && !reflect.DeepEqual(advisories[id], item) {
					return nil, fmt.Errorf("duplicate or missing source advisory")
				}
				advisories[id] = item
			case "finding":
				findings = append(findings, item)
			case "progress":
			default:
				return nil, fmt.Errorf("unknown govulncheck message")
			}
		}
	}
	if config == nil || sbom == nil {
		return nil, fmt.Errorf("source scanner config or coverage SBOM missing")
	}
	for name, value := range map[string]string{"protocol_version": "v1.0.0", "scanner_name": "govulncheck", "scanner_version": GovulnVersion, "go_version": CompilerVersion, "scan_mode": "source", "scan_level": "symbol", "db": "https://vuln.go.dev"} {
		if config[name] != value {
			return nil, fmt.Errorf("govulncheck protocol/configuration differs")
		}
	}
	if _, err = scanTime(config["db_last_modified"]); err != nil {
		return nil, err
	}
	sbomRoots, err := sourceStrings(sbom["roots"])
	if err != nil || sbom["go_version"] != CompilerVersion || !reflect.DeepEqual(sourceSet(sbomRoots), rootSet) {
		return nil, fmt.Errorf("source scanner root coverage differs")
	}
	scanned := map[string]bool{}
	sbomModules, ok := sbom["modules"].([]any)
	if !ok {
		return nil, fmt.Errorf("source SBOM module versions missing")
	}
	for _, v := range sbomModules {
		item := object(v)
		name := text(item["path"])
		version := text(item["version"])
		if name == "" {
			return nil, fmt.Errorf("invalid source SBOM module")
		}
		scanned[name+"@"+version] = true
	}
	for _, m := range allModules {
		if !scanned[m.Path+"@"+m.Version] {
			return nil, fmt.Errorf("source SBOM omits imported module versions")
		}
	}
	assessed := []map[string]any{}
	for _, finding := range findings {
		id := text(finding["osv"])
		advisory := object(advisories[id])
		trace, ok := finding["trace"].([]any)
		if advisory == nil || !ok || len(trace) == 0 {
			return nil, fmt.Errorf("source finding lacks authoritative advisory/trace")
		}
		frame := object(trace[0])
		if frame == nil {
			return nil, fmt.Errorf("invalid source finding frame")
		}
		affected := map[string]bool{}
		uncertain := false
		entries, _ := advisory["affected"].([]any)
		for _, v := range entries {
			entry := object(v)
			pkg := object(entry["package"])
			if pkg["ecosystem"] != "Go" || pkg["name"] != frame["module"] {
				continue
			}
			imports, ok := object(entry["ecosystem_specific"])["imports"].([]any)
			if !ok || len(imports) == 0 {
				uncertain = true
			}
			for _, v := range imports {
				name := text(object(v)["path"])
				if name == "" {
					uncertain = true
				} else {
					affected[name] = true
				}
			}
		}
		absent := len(affected) > 0 && !uncertain
		for name := range affected {
			if allSet[name] || serverSet[name] {
				absent = false
			}
		}
		moduleOnly := len(trace) == 1 && frame["package"] == nil && frame["function"] == nil && frame["position"] == nil
		disposition, reason := "unresolved", "Affected/package/symbol/uncertain finding requires review; no suppression applied."
		if absent && moduleOnly {
			disposition = "not-applicable"
			reason = "All authoritative affected Go package paths are absent from both exact native source import graphs; this is a module-only finding."
		}
		assessed = append(assessed, map[string]any{"id": id, "finding": finding, "finding_sha256": hash(legacyJSON(finding)), "advisory_sha256": hash(legacyJSON(advisory)), "affected_packages": sourceSorted(affected), "absent_from_all_root_and_server_graphs": absent, "disposition": disposition, "reason": reason})
	}
	convert, err := sourceExit(raw["govulncheck-convert.exit"])
	if err != nil || convert != 0 && convert != 3 || len(raw["govulncheck.txt"]) == 0 {
		return nil, fmt.Errorf("govulncheck text conversion failed")
	}
	unresolved := false
	for _, finding := range assessed {
		if finding["disposition"] == "unresolved" {
			unresolved = true
		}
	}
	if convert == 3 && !unresolved {
		return nil, fmt.Errorf("scanner text failure cannot be justified by absent source packages")
	}
	graphs := map[string]string{}
	for _, name := range []string{"go-roots.json", "go-all-graph.json", "go-server-graph.json", "go-modules.json"} {
		graphs[name] = hash(raw[name])
	}
	return map[string]any{"scanner": map[string]any{"name": "govulncheck", "version": GovulnVersion, "module_sum": GovulnSum, "binary_sha256": "sha256:" + binary[0], "build": build}, "database": map[string]any{"url": config["db"], "last_modified": config["db_last_modified"], "advisories_sha256": hash(legacyJSON(advisories))}, "build_environment": env, "coverage": map[string]any{"root_packages": len(roots), "all_imported_packages": len(all), "server_imported_packages": len(server), "scanned_modules": len(scanned)}, "graphs": graphs, "status": "complete", "exit_code": 0, "text_conversion_exit_code": convert, "raw_report_sha256": hash(raw["govulncheck.json"]), "findings": assessed}, nil
}

// AnalyzeNPM retains every advisory unchanged. No severity or dependency kind
// can suppress a finding; aggregation requires a complete zero-finding audit.
func AnalyzeNPM(raw map[string][]byte, lock []byte) (map[string]any, error) {
	if string(raw["node-execution.complete"]) != "complete\n" {
		return nil, fmt.Errorf("npm source execution incomplete")
	}
	node, npm := strings.TrimSpace(string(raw["node-version.txt"])), strings.TrimSpace(string(raw["npm-version.txt"]))
	if !regexp.MustCompile(`^v26\.[0-9]+\.[0-9]+$`).MatchString(node) || !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(npm) {
		return nil, fmt.Errorf("unexpected bundled Node/npm version")
	}
	code, err := sourceExit(raw["npm-audit.exit"])
	if err != nil || code != 0 && code != 1 {
		return nil, fmt.Errorf("npm audit exit status invalid")
	}
	report, err := sourceJSON(raw["npm-audit.json"])
	if err != nil {
		return nil, err
	}
	graph, err := sourceJSON(raw["npm-lock-graph.json"])
	if err != nil {
		return nil, err
	}
	state, err := sourceJSON(lock)
	if err != nil {
		return nil, err
	}
	vulnerabilities, ok := report["vulnerabilities"].(map[string]any)
	metadata := object(object(report["metadata"])["vulnerabilities"])
	if report["auditReportVersion"] != float64(2) || report["error"] != nil || !ok || metadata == nil {
		return nil, fmt.Errorf("npm audit report failed or incomplete")
	}
	problems := graph["problems"]
	badProblems := problems != nil
	if list, ok := problems.([]any); ok && len(list) == 0 {
		badProblems = false
	}
	packages := object(state["packages"])
	if graph["error"] != nil || badProblems || state["lockfileVersion"] != float64(3) || len(packages) == 0 {
		return nil, fmt.Errorf("npm source lock graph invalid or unresolved")
	}
	names := make([]string, 0, len(vulnerabilities))
	for name := range vulnerabilities {
		names = append(names, name)
	}
	slices.Sort(names)
	findings := []map[string]any{}
	for _, name := range names {
		value := vulnerabilities[name]
		findings = append(findings, map[string]any{"id": name, "finding": value, "finding_sha256": hash(legacyJSON(value)), "disposition": "unresolved", "reason": "npm advisory requires exact affected dependency review; retained without suppression."})
	}
	total, ok := metadata["total"].(float64)
	if !ok || total != float64(len(findings)) || code == 0 && total != 0 || code == 1 && total <= 0 {
		return nil, fmt.Errorf("npm findings and exit accounting differ")
	}
	return map[string]any{"scanner": map[string]any{"name": "npm audit", "version": npm, "node": node}, "database": map[string]any{"url": "https://registry.npmjs.org/-/npm/v1/security/advisories/bulk", "response_sha256": hash(raw["npm-audit.json"]), "snapshot_version_available": false}, "lock_sha256": hash(lock), "lock_graph_sha256": hash(raw["npm-lock-graph.json"]), "dependency_packages": len(packages) - 1, "status": "complete", "exit_code": code, "raw_report_sha256": hash(raw["npm-audit.json"]), "findings": findings}, nil
}
