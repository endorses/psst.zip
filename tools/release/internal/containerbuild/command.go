package containerbuild

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/oci"
)

// Run exposes read-only candidate preparation and exclusive v1 bundle creation.
// It never publishes, creates tags or establishes provenance/approval readiness.
func Run(ctx context.Context, root string, args []string, stdout io.Writer) error {
	return run(ctx, New(root), args, stdout)
}

func run(ctx context.Context, b Builder, args []string, stdout io.Writer) error {
	if len(args) > 0 && args[0] == "transfer" {
		return RunTransfer(ctx, args[1:], stdout)
	}
	if len(args) == 0 {
		return fmt.Errorf("container-build requires validate-tag, validate-source, resolve-bases, record-build, validate-candidate, defaults, bundle, assemble or transfer")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	flags := flag.NewFlagSet("container-build "+args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var output, githubOutput string
	var execute func() (any, map[string]string, error)
	switch args[0] {
	case "inspect-oci":
		var options oci.Options
		archive := flags.String("archive", "", "exact native OCI archive")
		flags.StringVar(&options.Repository, "repository", "", "source repository")
		flags.StringVar(&options.Version, "version", "", "exact stable version")
		flags.StringVar(&options.Commit, "commit", "", "exact source commit")
		flags.StringVar(&options.Platform, "platform", "", "native Linux platform")
		flags.StringVar(&options.Component, "component", "", "backend or web")
		flags.StringVar(&options.TestedConfig, "tested-config", "", "actually tested configuration digest")
		execute = func() (any, map[string]string, error) {
			image, err := oci.InspectArchive(*archive, options)
			return metadata(image), nil, err
		}
	case "assemble-oci":
		inputs := flags.String("inputs", "", "bounded native archive and tested configuration paths")
		execute = func() (any, map[string]string, error) {
			raw, err := readJSONFile(*inputs)
			if err != nil {
				return nil, nil, err
			}
			var options OCIAssemblyOptions
			if err := contract.Decode(raw, maxJSON, &options); err != nil {
				return nil, nil, err
			}
			result, err := AssembleOCI(ctx, options)
			return result, nil, err
		}
	case "assemble":
		inputs := flags.String("inputs", "", "bounded exact assembly input paths and source identity")
		execute = func() (any, map[string]string, error) {
			raw, err := readJSONFile(*inputs)
			if err != nil {
				return nil, nil, err
			}
			var options AssembleOptions
			if err := contract.Decode(raw, maxJSON, &options); err != nil {
				return nil, nil, err
			}
			foundation, err := b.Assemble(ctx, options)
			return foundation, nil, err
		}
	case "validate-tag", "validate-source":
		ref := flags.String("ref", "", "exact Git ref")
		event := flags.String("event-sha", "", "exact trigger object")
		planned := flags.String("planned-version", "", "unused planned stable version")
		flags.StringVar(&githubOutput, "github-output", "", "append fixed source outputs")
		execute = func() (any, map[string]string, error) {
			var identity SourceIdentity
			var err error
			if args[0] == "validate-tag" {
				if *planned != "" {
					return nil, nil, fmt.Errorf("version-tag validation cannot select a planned version")
				}
				identity, err = b.ValidateTag(ctx, *ref, *event)
			} else {
				identity, err = b.ValidateSource(ctx, *ref, *event, *planned)
			}
			return identity, map[string]string{"version": identity.Version, "commit": identity.Commit, "source_kind": identity.SourceKind}, err
		}
	case "resolve-bases":
		version := flags.String("version", "", "stable version")
		commit := flags.String("commit", "", "full source commit")
		flags.StringVar(&output, "output", "", "new candidate record")
		flags.StringVar(&githubOutput, "github-output", "", "append fixed immutable base outputs")
		execute = func() (any, map[string]string, error) {
			if output == "" {
				return nil, nil, fmt.Errorf("resolve-bases requires --output")
			}
			candidate, err := b.ResolveBases(ctx, *version, *commit)
			outputs := map[string]string{}
			for _, name := range baseNames {
				outputs[name+"_image"] = candidate.BaseImages[name]
			}
			return candidate, outputs, err
		}
	case "validate-candidate":
		candidate := flags.String("candidate", "", "existing candidate record")
		execute = func() (any, map[string]string, error) {
			raw, err := readJSONFile(*candidate)
			if err != nil {
				return nil, nil, err
			}
			value, err := DecodeCandidate(raw)
			return value, nil, err
		}
	case "record-build":
		candidate := flags.String("candidate", "", "verified base candidate")
		platform := flags.String("platform", "", "actual native Linux platform")
		backend := flags.String("backend-metadata", "", "original backend Buildx metadata")
		web := flags.String("web-metadata", "", "original web Buildx metadata")
		archive := flags.String("archive", "", "original paired Docker save")
		flags.StringVar(&output, "output", "", "new native build record")
		execute = func() (any, map[string]string, error) {
			if output == "" {
				return nil, nil, fmt.Errorf("record-build requires --output")
			}
			raw, err := readJSONFile(*candidate)
			if err != nil {
				return nil, nil, err
			}
			value, err := DecodeCandidate(raw)
			if err != nil {
				return nil, nil, err
			}
			metadata := map[string]json.RawMessage{}
			for name, file := range map[string]string{"backend": *backend, "web": *web} {
				metadata[name], err = readJSONFile(file)
				if err != nil {
					return nil, nil, err
				}
			}
			record, err := b.RecordBuild(ctx, value, *platform, metadata, *archive)
			return record, nil, err
		}
	case "bundle":
		var opts BundleOptions
		flags.StringVar(&opts.Output, "output", "", "bundle output directory")
		flags.StringVar(&opts.Version, "version", "", "stable version")
		flags.StringVar(&opts.Profile, "payload-profile", "artifact-foundation", "v1 payload profile")
		flags.StringVar(&opts.ExpectedCommit, "expected-commit", "", "exact selected source commit")
		execute = func() (any, map[string]string, error) {
			bundle, err := b.BuildBundle(ctx, opts)
			return bundle, nil, err
		}
	case "defaults":
		execute = func() (any, map[string]string, error) { return DefaultBases(), nil, nil }
	default:
		return fmt.Errorf("unknown container-build command")
	}
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("container-build accepts flags only")
	}
	value, outputs, err := execute()
	if err != nil {
		return err
	}
	raw, err := contract.JSON(value)
	if err != nil {
		return err
	}
	if output != "" {
		if err := writeRecord(output, raw); err != nil {
			return err
		}
	}
	if githubOutput != "" {
		if err := appendOutputs(githubOutput, outputs); err != nil {
			return err
		}
	}
	_, err = stdout.Write(raw)
	return err
}

func readJSONFile(name string) ([]byte, error) {
	info, err := os.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxJSON {
		return nil, fmt.Errorf("candidate input must be a bounded regular JSON file")
	}
	file, err := os.Open(name)
	if err != nil {
		return nil, fmt.Errorf("cannot open candidate input")
	}
	opened, statErr := file.Stat()
	raw, readErr := io.ReadAll(io.LimitReader(file, maxJSON+1))
	final, finalErr := file.Stat()
	closeErr := file.Close()
	if statErr != nil || !os.SameFile(info, opened) || readErr != nil || finalErr != nil || closeErr != nil || int64(len(raw)) != info.Size() || final.Size() != info.Size() || !final.ModTime().Equal(info.ModTime()) {
		return nil, fmt.Errorf("candidate JSON changed while reading")
	}
	return raw, nil
}

func writeRecord(name string, raw []byte) error {
	if len(raw) == 0 || len(raw) > maxJSON {
		return fmt.Errorf("candidate JSON output exceeds its byte bound")
	}
	if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(raw)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(name)
		return fmt.Errorf("cannot retain candidate record")
	}
	return nil
}

func appendOutputs(name string, values map[string]string) error {
	var lines strings.Builder
	for _, key := range []string{"version", "commit", "source_kind", "golang_image", "alpine_image", "node_image", "caddy_image", "buildkit_image"} {
		value, present := values[key]
		if !present {
			continue
		}
		if value == "" || strings.ContainsAny(value, "\r\n\x00") {
			return fmt.Errorf("unsafe candidate workflow output")
		}
		fmt.Fprintf(&lines, "%s=%s\n", key, value)
	}
	file, err := os.OpenFile(name, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := io.WriteString(file, lines.String())
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		return fmt.Errorf("cannot append candidate workflow outputs")
	}
	return nil
}
