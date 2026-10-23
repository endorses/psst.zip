package github

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
)

func appendOutput(path, text string) error {
	if path == "" {
		return nil
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0600)
	if err != nil {
		return errors.New("GitHub output file unavailable")
	}
	_, err = io.WriteString(file, text)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return errors.New("GitHub output could not be written")
	}
	return nil
}

func Run(ctx context.Context, root string, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("github requires cli-source, cli-publish, tag-event or verify-file")
	}
	flags := flag.NewFlagSet("github "+args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	repository := flags.String("repository", os.Getenv("GITHUB_REPOSITORY"), "GitHub source repository")
	tag := flags.String("tag", "", "exact existing stable tag")
	revision := flags.String("revision", "", "reviewed source revision")
	version := flags.String("version", "", "stable CLI version")
	directory := flags.String("artifacts", "", "downloaded CLI candidate directory")
	receipt := flags.String("receipt", "", "new private partial-publication receipt")
	file := flags.String("file", "", "private event or evidence file")
	defaultRun, _ := strconv.ParseInt(os.Getenv("GITHUB_RUN_ID"), 10, 64)
	defaultAttempt, _ := strconv.ParseInt(os.Getenv("GITHUB_RUN_ATTEMPT"), 10, 64)
	runID := flags.Int64("run-id", defaultRun, "exact workflow run ID")
	attempt := flags.Int64("attempt", defaultAttempt, "exact workflow attempt")
	output := flags.String("github-output", os.Getenv("GITHUB_OUTPUT"), "GitHub output file")
	summary := flags.String("summary", os.Getenv("GITHUB_STEP_SUMMARY"), "GitHub step summary file")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("github release commands accept flags only")
	}
	client := New(root)
	switch args[0] {
	case "tag-event":
		data, err := boundedFile(*file, MaxJSON)
		if err != nil {
			return err
		}
		return ValidateTagPush(data, Binding{Repository: *repository, Version: *version, Commit: *revision})
	case "verify-file":
		return client.VerifyFile(ctx, *file, Binding{*repository, *version, *revision, *runID, *attempt}, nil)
	case "cli-source":
		source, err := client.CLISource(ctx, *repository, *tag, os.Getenv("GITHUB_REF"), os.Getenv("GITHUB_SHA"))
		if err != nil {
			return err
		}
		if err = appendOutput(*output, fmt.Sprintf("revision=%s\nversion=%s\nci_run=%d\n", source.Revision, source.Version, source.RunID)); err != nil {
			return err
		}
		return appendOutput(*summary, fmt.Sprintf("CLI source `%s` reuses executed checks in CI run %d. No application suites are rerun for packaging.\n", source.Revision, source.RunID))
	case "cli-publish":
		if *tag != "client/v"+*version {
			return errors.New("CLI tag and version differ")
		}
		private, assets, err := CLIAssets(*directory, *version, *revision)
		if err != nil {
			return err
		}
		defer os.RemoveAll(private)
		notes := fmt.Sprintf("psst.zip CLI %s\n\nReviewed source: %s\n\nPlatform archives contain licenses and installation instructions. SHA256SUMS covers every release asset; the source archive includes vendored dependencies.\n", *version, *revision)
		result, err := client.PublishNew(ctx, Publication{Repository: *repository, Tag: *tag, Revision: *revision, Title: "psst.zip CLI " + *version, Notes: notes, Receipt: *receipt, Assets: assets})
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "Immutable CLI release %s verified; release %d and all %d assets match the reviewed candidate.\n", *tag, result.ReleaseID, len(result.Assets))
		return err
	default:
		return errors.New("unknown GitHub release command")
	}
}
