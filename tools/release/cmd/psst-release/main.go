// psst-release is development tooling; it is never linked into the psst client.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/endorses/psst.zip/tools/release/internal/androidrelease"
	"github.com/endorses/psst.zip/tools/release/internal/clibuild"
	"github.com/endorses/psst.zip/tools/release/internal/notices"
	"github.com/endorses/psst.zip/tools/release/internal/publication"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "psst-release:", err)
		os.Exit(1)
	}
}

func checkout() string {
	path, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(path, "client/go.mod")); err == nil {
			return path
		}
		parent := filepath.Dir(path)
		if parent == path {
			return "."
		}
		path = parent
	}
}

func run(ctx context.Context, args []string) error {
	global := flag.NewFlagSet("psst-release", flag.ContinueOnError)
	root := global.String("root", checkout(), "psst.zip source checkout")
	if err := global.Parse(args); err != nil {
		return err
	}
	args = global.Args()
	if len(args) == 0 || args[0] == "help" {
		fmt.Println("psst-release — psst.zip development tooling\n\nUsage: psst-release [--root CHECKOUT] COMMAND\n\n  assets              Prepare or check embedded LAN assets and notices\n  notices             Collect resolved CLI target dependency notices\n  cli-package         Build six CLI targets, notices and offline source\n  android             Verify, sign or package Android releases\n  publication-result  Consolidate exact container release evidence\n\nProject: https://psst.zip")
		return nil
	}
	absolute, err := filepath.Abs(*root)
	if err != nil {
		return err
	}
	switch args[0] {
	case "android":
		return androidrelease.Run(ctx, absolute, args[1:], os.Stdout, os.Stderr)
	case "publication-result":
		return publication.Run(ctx, args[1:])
	case "assets":
		flags := flag.NewFlagSet("assets", flag.ContinueOnError)
		check := flags.Bool("check", false, "verify cached assets without rebuilding")
		version := flags.String("release-version", "", "stable CLI version for source metadata")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return fmt.Errorf("assets accepts flags only")
		}
		result, err := clibuild.Prepare(ctx, clibuild.AssetsOptions{Root: absolute, Check: *check, ReleaseVersion: *version})
		if err != nil {
			return err
		}
		fmt.Println("LAN browser assets verified:", result.AssetDigest)
		return nil
	case "notices":
		flags := flag.NewFlagSet("notices", flag.ContinueOnError)
		output := flags.String("output", "", "empty output directory")
		workspace := flags.Bool("workspace", false, "use development workspace")
		web := flags.Bool("include-web", true, "include embedded browser legal resources")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *output == "" || flags.NArg() != 0 {
			return fmt.Errorf("notices requires --output DIR")
		}
		result, err := notices.Collect(ctx, notices.Options{Root: absolute, Output: *output, Workspace: *workspace, IncludeWeb: *web})
		if err != nil {
			return err
		}
		fmt.Printf("Retained notices for %d dependency modules and Go.\n", len(result.Modules)-1)
		return nil
	case "cli-package":
		flags := flag.NewFlagSet("cli-package", flag.ContinueOnError)
		version := flags.String("version", "", "stable CLI X.Y.Z version")
		revision := flags.String("revision", "", "exact source commit")
		output := flags.String("output", "", "empty output directory")
		dirty := flags.Bool("allow-dirty", false, "development-only nonpublishable output")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 || *output == "" {
			return fmt.Errorf("cli-package requires --version, --revision, --output")
		}
		if err := clibuild.Package(ctx, clibuild.PackageOptions{Root: absolute, Version: *version, Revision: *revision, Output: *output, AllowDirty: *dirty}); err != nil {
			return err
		}
		fmt.Println("Built six targets, vendored source and checksums. Nothing published.")
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}
