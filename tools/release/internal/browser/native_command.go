package browser

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

// RunNative exposes real capture and independent retained-capture replay.
func RunNative(ctx context.Context, root string, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || (args[0] != "collect" && args[0] != "verify") {
		return fmt.Errorf("browser native requires collect or verify")
	}
	flags := flag.NewFlagSet("browser native "+args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	o := NativeOptions{Root: root, Archives: map[string]string{}, Configs: map[string]string{}}
	flags.StringVar(&o.Source.Repository, "repository", "", "exact source repository")
	flags.StringVar(&o.Source.Version, "version", "", "stable source version")
	flags.StringVar(&o.Source.Commit, "commit", "", "full source commit")
	flags.StringVar(&o.Platform, "platform", "", "native Linux platform")
	flags.StringVar(&o.BuilderConfig, "builder-config", "", "preloaded immutable browser builder")
	flags.StringVar(&o.CommonCollection, "common-collection", "", "verified common source originals")
	flags.StringVar(&o.RuntimeCollection, "runtime-collection", "", "retained runtime sources")
	flags.StringVar(&o.Cosign, "cosign", "", "pinned legacy runtime source verifier")
	flags.StringVar(&o.Capture, "capture", "", "retained regular browser capture for verify")
	flags.StringVar(&o.Output, "output", "", "new output directory for collect; optional new report file for verify")
	backendArchive := flags.String("backend-archive", "", "tested backend OCI archive")
	webArchive := flags.String("web-archive", "", "tested web OCI archive")
	backendConfig := flags.String("backend-config", "", "tested backend config digest")
	webConfig := flags.String("web-config", "", "tested web config digest")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("browser native accepts flags only")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	o.Archives["backend"], o.Archives["web"] = *backendArchive, *webArchive
	o.Configs["backend"], o.Configs["web"] = *backendConfig, *webConfig
	var result NativeFacts
	var err error
	if args[0] == "collect" {
		if o.Capture != "" {
			return fmt.Errorf("browser native collect cannot accept supplied capture evidence")
		}
		result, err = CollectNative(ctx, o)
	} else {
		if o.Capture == "" {
			return fmt.Errorf("browser native verify requires retained capture")
		}
		result, err = VerifyNative(ctx, o)
		if err == nil && o.Output != "" {
			err = writeNativeReport(o.Output, result)
		}
	}
	if err != nil {
		return err
	}
	raw, err := contract.JSON(result)
	if err != nil {
		return err
	}
	_, err = stdout.Write(raw)
	return err
}
