package scanning

import (
	"context"
	"flag"
	"fmt"
	"io"
	"path/filepath"
)

func sourceCommand(ctx context.Context, root string, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("scan source", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	o := SourceOptions{Root: root}
	flags.StringVar(&o.Source.Repository, "repository", "", "source repository")
	flags.StringVar(&o.Source.Version, "version", "", "exact stable release version")
	flags.StringVar(&o.Source.Commit, "commit", "", "exact committed source")
	flags.StringVar(&o.Platform, "platform", "", "native Linux platform")
	flags.StringVar(&o.GoImage, "go-image", "", "official immutable Go builder")
	flags.StringVar(&o.NodeImage, "node-image", "", "official immutable common npm builder")
	flags.StringVar(&o.CommonNPM, "common-npm", "", "one retained exact-lock common npm audit")
	flags.StringVar(&o.Output, "output", "", "new private source scan directory")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("invalid scan source arguments")
	}
	if flags.NArg() != 0 || o.Output == "" || o.CommonNPM == "" {
		return fmt.Errorf("scan source requires exact native source/builders, common npm and new output")
	}
	var err error
	o.Output, err = filepath.Abs(o.Output)
	if err != nil {
		return err
	}
	o.CommonNPM, err = filepath.Abs(o.CommonNPM)
	if err != nil {
		return err
	}
	if _, err = MeasureSources(ctx, o); err != nil {
		return err
	}
	fmt.Fprintln(output, "Retained native Go source scanner evidence and the shared npm audit; finding policy remains pending.")
	return nil
}
