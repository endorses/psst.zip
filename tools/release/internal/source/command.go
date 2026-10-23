package source

import (
	"context"
	"flag"
	"fmt"
)

// Run implements source retention/verification without a Python interpreter.
func Run(ctx context.Context, root string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("sources requires a command: common collect|verify")
	}
	switch args[0] {
	case "modules":
		if len(args) < 2 {
			return fmt.Errorf("sources modules requires collect or verify")
		}
		flags := flag.NewFlagSet("sources modules "+args[1], flag.ContinueOnError)
		var o ModuleOptions
		o.Root = root
		flags.StringVar(&o.Output, "output", "", "new private collection directory")
		flags.StringVar(&o.Collection, "collection", "", "retained collection directory")
		flags.StringVar(&o.Source.Repository, "repository", "", "source repository")
		flags.StringVar(&o.Source.Version, "version", "", "exact stable version")
		flags.StringVar(&o.Source.Commit, "commit", "", "exact source commit")
		flags.StringVar(&o.Platform, "platform", "", "native Linux platform")
		flags.StringVar(&o.GoImage, "go-image", "", "immutable official Go builder")
		if err := flags.Parse(args[2:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return fmt.Errorf("sources accepts flags only")
		}
		switch args[1] {
		case "collect":
			if o.Output == "" {
				return fmt.Errorf("collect requires --output")
			}
			_, err := CollectModules(ctx, o)
			return err
		case "verify":
			if o.Collection == "" {
				return fmt.Errorf("verify requires --collection")
			}
			_, err := VerifyModules(ctx, o)
			return err
		default:
			return fmt.Errorf("unknown module command")
		}
	case "common":
		if len(args) < 2 {
			return fmt.Errorf("sources common requires collect or verify")
		}
		flags := flag.NewFlagSet("sources common "+args[1], flag.ContinueOnError)
		var o CommonOptions
		o.Root = root
		flags.StringVar(&o.Output, "output", "", "new private collection directory")
		flags.StringVar(&o.Collection, "collection", "", "retained collection directory")
		flags.StringVar(&o.Source.Repository, "repository", "", "source repository")
		flags.StringVar(&o.Source.Version, "version", "", "exact stable version")
		flags.StringVar(&o.Source.Commit, "commit", "", "exact source commit")
		if err := flags.Parse(args[2:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return fmt.Errorf("sources accepts flags only")
		}
		switch args[1] {
		case "collect":
			if o.Output == "" {
				return fmt.Errorf("collect requires --output")
			}
			_, err := CollectCommon(ctx, o)
			return err
		case "verify":
			if o.Collection == "" {
				return fmt.Errorf("verify requires --collection")
			}
			_, err := VerifyCommon(ctx, o)
			return err
		default:
			return fmt.Errorf("unknown common command")
		}
	default:
		return fmt.Errorf("unknown source command")
	}
}
