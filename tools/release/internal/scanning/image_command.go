package scanning

import (
	"context"
	"flag"
	"fmt"
	"io"
	"path/filepath"
)

func imageCommand(ctx context.Context, root string, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("scan image", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	o := ImageOptions{Root: root}
	flags.StringVar(&o.Source.Repository, "repository", "", "source repository")
	flags.StringVar(&o.Source.Version, "version", "", "exact stable release version")
	flags.StringVar(&o.Source.Commit, "commit", "", "exact source commit")
	flags.StringVar(&o.Source.Platform, "platform", "", "native linux platform")
	flags.StringVar(&o.Component, "component", "", "backend or web")
	flags.StringVar(&o.Archive, "archive", "", "exact final native OCI archive")
	flags.StringVar(&o.TestedConfig, "tested-config", "", "smoke-tested native configuration")
	flags.StringVar(&o.ToolArchive, "tool-archive", "", "pinned official Trivy release archive")
	flags.StringVar(&o.ToolBundle, "tool-bundle", "", "pinned official Trivy signature bundle")
	flags.StringVar(&o.Cosign, "cosign", "", "pinned native signature verifier")
	flags.StringVar(&o.Output, "output", "", "new private measurement directory")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("invalid scan image arguments")
	}
	if flags.NArg() != 0 || o.Output == "" || o.Archive == "" || o.ToolArchive == "" || o.ToolBundle == "" || o.Cosign == "" {
		return fmt.Errorf("scan image requires exact image, pinned scanner inputs and new output")
	}
	for _, value := range []*string{&o.Archive, &o.ToolArchive, &o.ToolBundle, &o.Cosign, &o.Output} {
		absolute, err := filepath.Abs(*value)
		if err != nil {
			return err
		}
		*value = absolute
	}
	_, err := MeasureImage(ctx, o)
	if err == nil {
		fmt.Fprintln(output, "Retained complete native image scan and every finding; release finding review remains pending.")
	}
	return err
}
