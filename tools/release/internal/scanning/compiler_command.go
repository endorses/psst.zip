package scanning

import (
	"context"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"slices"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

type repeatedValues []string

func (v *repeatedValues) String() string         { return "repeatable value" }
func (v *repeatedValues) Set(value string) error { *v = append(*v, value); return nil }

func compilerCommand(ctx context.Context, root string, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("scan compiler", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	o := CompilerOptions{Root: root}
	flags.StringVar(&o.Source.Repository, "repository", "", "source repository")
	flags.StringVar(&o.Source.Version, "version", "", "exact stable release version")
	flags.StringVar(&o.Source.Commit, "commit", "", "exact source commit")
	flags.StringVar(&o.Platform, "platform", "", "native linux platform")
	flags.StringVar(&o.Collection, "collection", "", "retained runtime source offering directory")
	flags.StringVar(&o.SourceSHA256, "source-sha256", "", "external runtime source archive digest")
	flags.StringVar(&o.Component, "component", "", "backend or web")
	flags.StringVar(&o.Archive, "archive", "", "exact final native OCI archive")
	flags.StringVar(&o.TestedConfig, "tested-config", "", "smoke-tested native configuration")
	flags.StringVar(&o.GoImage, "go-image", "", "immutable actual executable compiler image")
	flags.StringVar(&o.Cosign, "cosign", "", "pinned native Caddy signature verifier")
	flags.StringVar(&o.Output, "output", "", "new private output directory")
	native := flags.String("native-artifacts", "", "local native descriptor supplying tested configuration/source digest")
	var ids, inputs repeatedValues
	flags.Var(&ids, "advisory-id", "official Go advisory identifier")
	flags.Var(&inputs, "advisory-input", "bounded scanner JSON containing Go advisory identifiers")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("invalid scan compiler arguments")
	}
	if flags.NArg() != 0 || o.Collection == "" || o.Output == "" || o.Archive == "" {
		return fmt.Errorf("scan compiler requires exact source, runtime offering, native image and new output")
	}
	if *native != "" {
		if o.TestedConfig != "" || o.SourceSHA256 != "" {
			return fmt.Errorf("native descriptor inputs cannot be overridden")
		}
		config, sha, err := nativeScanInputs(*native, NativeIdentity{o.Source, o.Platform}, o.Component)
		if err != nil {
			return err
		}
		o.TestedConfig, o.SourceSHA256 = config, sha
	}
	for _, name := range inputs {
		raw, err := bounded(name, scannerRawLimit)
		if err != nil {
			return err
		}
		collected, err := AdvisoryIDs(raw)
		if err != nil {
			return err
		}
		ids = append(ids, collected...)
	}
	slices.Sort(ids)
	o.Advisories = slices.Compact(ids)
	var err error
	o.Collection, err = filepath.Abs(o.Collection)
	if err != nil {
		return err
	}
	o.Output, err = filepath.Abs(o.Output)
	if err != nil {
		return err
	}
	o.Archive, err = filepath.Abs(o.Archive)
	if err != nil {
		return err
	}
	_, err = MeasureCompiler(ctx, o)
	if err == nil {
		fmt.Fprintln(output, "Retained exact native executable/compiler correspondence; finding review remains pending.")
	}
	return err
}

// AdvisoryIDs collects evidence references only; it never treats an identifier
// as a suppression, acceptance or finding disposition.
func AdvisoryIDs(raw []byte) ([]string, error) {
	var value any
	if err := contract.Decode(raw, scannerRawLimit, &value); err != nil {
		return nil, err
	}
	pattern := regexp.MustCompile(`GO-[0-9]{4}-[0-9]+`)
	seen := map[string]bool{}
	var walk func(any)
	walk = func(value any) {
		switch v := value.(type) {
		case string:
			for _, position := range pattern.FindAllStringIndex(v, -1) {
				if position[0] > 0 {
					previous := v[position[0]-1]
					if previous == '-' || previous >= 'A' && previous <= 'Z' || previous >= '0' && previous <= '9' {
						continue
					}
				}
				seen[v[position[0]:position[1]]] = true
			}
		case []any:
			for _, item := range v {
				walk(item)
			}
		case map[string]any:
			for _, item := range v {
				walk(item)
			}
		}
	}
	walk(value)
	if len(seen) > 32 {
		return nil, fmt.Errorf("official advisory inventory exceeds bounds")
	}
	result := make([]string, 0, len(seen))
	for id := range seen {
		result = append(result, id)
	}
	slices.Sort(result)
	return result, nil
}
