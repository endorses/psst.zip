package scanning

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/github"
)

type ImageEvidence struct {
	Measurement, Report []byte
	CompilerDirectory   string
	Expected            CompilerExpected
}
type ImageAggregationOptions struct {
	Root          string
	Binding       github.Binding
	Subjects      map[string]string
	Images        map[string]ImageEvidence
	ResolvedBases []byte
	Now           time.Time
	// An assembler may share its exact-byte authentication cache. Omitting the
	// adapter invokes the supported GitHub verifier with this exact run/attempt.
	Authenticate func(context.Context, []byte) error
}

// AggregateImages derives final-image-scanners details from exact authenticated
// native facts. It neither publishes nor accepts caller disposition booleans.
func AggregateImages(ctx context.Context, o ImageAggregationOptions) (map[string]any, error) {
	if err := o.Binding.Validate(); err != nil {
		return nil, err
	}
	if o.Now.IsZero() {
		o.Now = time.Now().UTC()
	}
	authenticate := o.Authenticate
	if authenticate == nil {
		client := github.New(o.Root)
		authenticate = func(ctx context.Context, raw []byte) error { return client.VerifyBytes(ctx, raw, o.Binding, nil) }
	}
	if len(o.Images) != 4 {
		return nil, fmt.Errorf("image scanner aggregation requires both exact native pairs")
	}
	scans := []any{}
	var policy, fact map[string]any
	for _, component := range []string{"backend", "web"} {
		for _, arch := range []string{"amd64", "arm64"} {
			target := component + "-" + arch
			evidence, ok := o.Images[target]
			if !ok {
				return nil, fmt.Errorf("missing native image scan")
			}
			expected := evidence.Expected
			owner, _, _ := strings.Cut(strings.ToLower(o.Binding.Repository), "/")
			if expected.Source != (NativeIdentity{Source{o.Binding.Repository, o.Binding.Version, o.Binding.Commit}, "linux/" + arch}) || expected.Component != component || expected.Image.Platform != expected.Source.Platform || !digestPattern.MatchString(expected.Image.Manifest) || !digestPattern.MatchString(expected.Image.Config) || !digestPattern.MatchString(expected.Image.Archive) || o.Subjects[target] != "oci://ghcr.io/"+owner+"/psst-zip-"+component+"@"+expected.Image.Manifest {
				return nil, fmt.Errorf("image scan expected context differs from final release subject")
			}
			if err := authenticate(ctx, evidence.Measurement); err != nil {
				return nil, err
			}
			scan, err := ReadImageMeasurement(evidence.Measurement, evidence.Report, expected.Source, component, expected.Image)
			if err != nil {
				return nil, err
			}
			assessed := []any{}
			var compilerDigest any
			if len(scan.Findings) > 0 || evidence.CompilerDirectory != "" {
				if evidence.CompilerDirectory == "" {
					return nil, fmt.Errorf("affected image lacks exact compiler evidence")
				}
				raw, e := bounded(filepath.Join(evidence.CompilerDirectory, "compiler-graph-measurement.json"), scannerRawLimit)
				if e != nil {
					return nil, e
				}
				if e = authenticate(ctx, raw); e != nil {
					return nil, e
				}
				compiler, e := decodeCompiler(evidence.CompilerDirectory, raw, expected)
				if e != nil {
					return nil, e
				}
				compilerDigest = hash(raw)
				graphRaw, e := compilerRaw(evidence.CompilerDirectory, compiler, "compiler-graph.json", compiler.Graph.SHA256)
				if e != nil {
					return nil, e
				}
				paths, modules, e := GraphPaths(graphRaw)
				if e != nil {
					return nil, e
				}
				selected := map[string]string{}
				for name, module := range modules {
					selected[name] = module.Version
				}
				selected["stdlib"] = strings.TrimPrefix(compiler.Binary.Build.GoVersion, "go")
				for _, row := range scan.Findings {
					finding, e := deriveImageFinding(row, compiler, evidence.CompilerDirectory, paths, selected)
					if e != nil {
						return nil, e
					}
					if finding["disposition"] == "affected" {
						if component != "web" {
							return nil, fmt.Errorf("affected backend packages require repair/rescan")
						}
						if policy == nil {
							policy, fact, e = committedPolicy(ctx, o.Root, o.Binding.Repository, o.Binding.Commit)
							if e != nil {
								return nil, e
							}
							if len(o.ResolvedBases) == 0 {
								return nil, fmt.Errorf("temporary Caddy acceptance requires authenticated resolved bases")
							}
							if e = authenticate(ctx, o.ResolvedBases); e != nil {
								return nil, e
							}
							var bases map[string]any
							if contract.Decode(o.ResolvedBases, 16<<20, &bases) != nil || bases["source_commit"] != o.Binding.Commit || bases["version"] != o.Binding.Version || object(bases["base_images"])["caddy"] != policy["base_image"] {
								return nil, fmt.Errorf("resolved Caddy base differs from committed policy")
							}
							native := object(object(bases["base_platform_digests"])["caddy"])
							for _, p := range []string{"linux/amd64", "linux/arm64"} {
								if native[p] != object(object(policy["platforms"])[p])["base_digest"] {
									return nil, fmt.Errorf("resolved Caddy native digest differs from policy")
								}
							}
						}
						finding, e = acceptImageFinding(finding, compiler, expected, policy, fact, o.Now)
						if e != nil {
							return nil, e
						}
					}
					assessed = append(assessed, finding)
				}
			}
			scans = append(scans, map[string]any{"target": target, "subject": o.Subjects[target], "status": "complete", "exit_code": 0, "scanner": scan.Scanner["name"], "version": scan.Scanner["version"], "database": scan.Database["sha256"], "scanned_at": scan.ScannedAt, "findings": assessed, "measurement_sha256": scan.MeasurementSHA256, "raw_report_sha256": scan.ReportSHA256, "compiler_measurement_sha256": compilerDigest, "scanner_evidence": scan.Scanner, "database_evidence": scan.Database, "image": scan.Image})
		}
	}
	details := map[string]any{"scans": scans, "derivation_policy": "exact-native-binary-compiler-graph-authoritative-Go-package-absence-v1"}
	if policy != nil {
		details["temporary_caddy_exception_policy"] = fact
		details["derivation_policy"] = "exact-native-binary-compiler-graph-authoritative-Go-package-absence-or-committed-temporary-caddy-acceptance-v2"
	}
	encoded, _ := contract.JSON(details)
	if err := ValidatePublication(ctx, o.Root, encoded, o.Binding.Repository, o.Binding.Version, o.Binding.Commit, o.Now); err != nil {
		return nil, err
	}
	return details, nil
}

func compilerRaw(directory string, compiler CompilerMeasurement, name, digest string) ([]byte, error) {
	if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,199}$`).MatchString(name) || !digestPattern.MatchString(digest) || compiler.RawFiles[name] != digest {
		return nil, fmt.Errorf("raw compiler evidence not covered by measurement")
	}
	raw, err := bounded(filepath.Join(directory, name), scannerRawLimit)
	if err != nil || hash(raw) != digest {
		return nil, fmt.Errorf("raw compiler evidence substituted")
	}
	return raw, nil
}

// Package absence is deliberately conservative across every advisory range and
// architecture. Symbol reachability cannot suppress an affected import path.
func deriveImageFinding(row map[string]any, compiler CompilerMeasurement, directory string, paths []string, modules map[string]string) (map[string]any, error) {
	finding := object(row["finding"])
	module := text(finding["PkgName"])
	version := text(finding["InstalledVersion"])
	if module == "stdlib" {
		version = strings.TrimPrefix(version, "v")
	}
	if row["class"] != "lang-pkgs" || object(finding["DataSource"])["ID"] != "govulndb" || modules[module] != version || version == "" {
		return nil, fmt.Errorf("finding authoritative Go module/version differs from actual executable")
	}
	ids := map[string]bool{}
	for _, value := range []any{finding["VulnerabilityID"]} {
		if regexp.MustCompile(`^GO-[0-9]{4}-[0-9]+$`).MatchString(text(value)) {
			ids[text(value)] = true
		}
	}
	if vendors, ok := finding["VendorIDs"].([]any); ok {
		for _, value := range vendors {
			if regexp.MustCompile(`^GO-[0-9]{4}-[0-9]+$`).MatchString(text(value)) {
				ids[text(value)] = true
			}
		}
	}
	if len(ids) != 1 {
		return nil, fmt.Errorf("finding lacks unambiguous official Go advisory")
	}
	var id string
	for value := range ids {
		id = value
	}
	entry, ok := compiler.Advisories[id]
	if !ok || entry.Origin != "https://vuln.go.dev/ID/"+id+".json" {
		return nil, fmt.Errorf("official advisory evidence missing")
	}
	raw, err := compilerRaw(directory, compiler, entry.RawFile, entry.SHA256)
	if err != nil {
		return nil, err
	}
	var advisory map[string]any
	if contract.Decode(raw, 1<<20, &advisory) != nil || advisory["id"] != id || advisory["withdrawn"] != nil && advisory["withdrawn"] != "" {
		return nil, fmt.Errorf("official advisory is uncertain or withdrawn")
	}
	aliases := map[string]bool{id: true}
	if entries, ok := advisory["aliases"].([]any); ok {
		for _, alias := range entries {
			aliases[text(alias)] = true
		}
	}
	if !aliases[text(finding["VulnerabilityID"])] {
		return nil, fmt.Errorf("scanner finding is not an official advisory alias")
	}
	if _, err = scanTime(advisory["modified"]); err != nil {
		return nil, err
	}
	entries, ok := advisory["affected"].([]any)
	if !ok || len(entries) == 0 {
		return nil, fmt.Errorf("official advisory lacks affected packages")
	}
	affected := map[string]bool{}
	for _, value := range entries {
		affectedEntry := object(value)
		pkg := object(affectedEntry["package"])
		if pkg == nil {
			return nil, fmt.Errorf("malformed official affected package")
		}
		if pkg["ecosystem"] != "Go" || pkg["name"] != module {
			continue
		}
		imports, ok := object(affectedEntry["ecosystem_specific"])["imports"].([]any)
		if !ok || len(imports) == 0 {
			return nil, fmt.Errorf("affected module has incomplete authoritative import paths")
		}
		for _, value := range imports {
			name := text(object(value)["path"])
			if !validImport(name) || module != "stdlib" && name != module && !strings.HasPrefix(name, module+"/") {
				return nil, fmt.Errorf("official affected import path is uncertain")
			}
			affected[name] = true
		}
	}
	if len(affected) == 0 {
		return nil, fmt.Errorf("official affected Go packages missing")
	}
	packages := make([]string, 0, len(affected))
	present := false
	for name := range affected {
		packages = append(packages, name)
		if slices.Contains(paths, name) {
			present = true
		}
	}
	slices.Sort(packages)
	result := map[string]any{}
	for name, value := range row {
		result[name] = value
	}
	result["official_advisory"] = map[string]any{"id": id, "sha256": entry.SHA256, "origin": entry.Origin, "modified": advisory["modified"]}
	result["affected_packages"] = packages
	result["graph_sha256"] = compiler.Graph.SHA256
	result["binary_sha256"] = compiler.Binary.SHA256
	result["source_inputs"] = compiler.Inputs
	result["disposition"] = "not-applicable"
	result["reason"] = "Every official affected Go import path (conservatively across all advisory ranges) is absent from the exact native compiler dependency graph matching this executable."
	if present {
		result["disposition"] = "affected"
		delete(result, "reason")
	}
	return result, nil
}

func acceptImageFinding(row map[string]any, compiler CompilerMeasurement, expected CompilerExpected, node, fact map[string]any, now time.Time) (map[string]any, error) {
	start, _ := policyTime(node["authorized_at"])
	end, _ := policyTime(node["expires_at"])
	native := object(object(node["platforms"])[expected.Source.Platform])
	if now.Before(start) || !now.Before(end) || compiler.Component != "web" || compiler.Binary.Build.GoVersion != node["go_version"] || compiler.Binary.SHA256 != native["binary_sha256"] || expected.Caddy.Version != node["caddy_version"] || expected.Caddy.Authentication["source_commit"] != node["source_revision"] || compiler.Inputs["wrapper_revision"] != node["source_revision"] || expected.Caddy.Authentication["base_index"] != node["base_image"] {
		return nil, fmt.Errorf("affected Caddy binary/source exceeds active exact committed acceptance")
	}
	finding := object(row["finding"])
	official := object(row["official_advisory"])
	count := 0
	for _, value := range node["findings"].([]any) {
		accepted := object(value)
		if accepted["go_id"] == official["id"] && accepted["scanner_id"] == finding["VulnerabilityID"] && accepted["module"] == finding["PkgName"] && accepted["installed_version"] == finding["InstalledVersion"] && accepted["fixed_version"] == finding["FixedVersion"] && accepted["advisory_sha256"] == official["sha256"] {
			count++
		}
	}
	if count != 1 {
		return nil, fmt.Errorf("affected Caddy finding is outside temporary policy")
	}
	result := map[string]any{}
	for name, value := range row {
		result[name] = value
	}
	result["disposition"] = "temporarily-accepted"
	result["reason"] = node["reason"]
	result["acceptance"] = map[string]any{"policy": fact, "exception_id": node["id"], "authorized_by": node["authorized_by"], "expires_at": node["expires_at"], "caddy_version": node["caddy_version"], "source_revision": node["source_revision"], "base_image": node["base_image"], "go_version": node["go_version"], "base_digest": native["base_digest"], "binary_sha256": native["binary_sha256"]}
	return result, nil
}
