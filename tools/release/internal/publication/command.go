package publication

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	releasegithub "github.com/endorses/psst.zip/tools/release/internal/github"
)

func RunPublish(ctx context.Context, root string, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("container-publish requires settings, prepare, publish or diagnostics")
	}
	flags := flag.NewFlagSet("container-publish "+args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	o := PublishOptions{Root: root}
	flags.StringVar(&o.Root, "root", root, "reviewed source checkout")
	flags.StringVar(&o.Inputs, "inputs-root", "", "downloaded exact prepared/native/recovery inputs")
	flags.StringVar(&o.ReviewedCommit, "reviewed-commit", "", "reviewed exact source revision")
	flags.StringVar(&o.State, "state", "", "private runner-local state directory")
	flags.StringVar(&o.Checksums, "checksums", "", "new final attestation checksum input")
	repository := flags.String("repository", os.Getenv("GITHUB_REPOSITORY"), "diagnostic repository")
	version := flags.String("version", os.Getenv("CANDIDATE_VERSION"), "diagnostic version")
	commit := flags.String("commit", os.Getenv("SOURCE_COMMIT"), "diagnostic source")
	run, _ := strconv.ParseInt(os.Getenv("GITHUB_RUN_ID"), 10, 64)
	attempt, _ := strconv.ParseInt(os.Getenv("GITHUB_RUN_ATTEMPT"), 10, 64)
	runID := flags.Int64("run-id", run, "diagnostic workflow run")
	runAttempt := flags.Int64("attempt", attempt, "diagnostic workflow attempt")
	outcome := flags.String("outcome", "", "publication step outcome")
	output := flags.String("output", "", "new bounded diagnostic file")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("container-publish accepts flags only")
	}
	environment := map[string]string{}
	for _, name := range []string{"GH_TOKEN", "PSST_IMMUTABLE_INSPECTION_TOKEN", "GITHUB_ACTIONS", "RUNNER_ENVIRONMENT", "GITHUB_REPOSITORY", "GITHUB_JOB", "GITHUB_REF", "GITHUB_SHA", "GITHUB_EVENT_NAME", "GITHUB_WORKFLOW_REF", "GITHUB_WORKFLOW_SHA", "GITHUB_RUN_ID", "GITHUB_RUN_ATTEMPT", "GITHUB_ACTOR", "PSST_INITIALIZE_GHCR_PACKAGES"} {
		environment[name] = os.Getenv(name)
	}
	if args[0] == "settings" {
		value := environment["PSST_INITIALIZE_GHCR_PACKAGES"]
		if value != "" && value != "false" && value != "true" {
			return fmt.Errorf("invalid first-package initialization setting")
		}
		token := environment["PSST_IMMUTABLE_INSPECTION_TOKEN"]
		if token == "" || strings.ContainsAny(token, "\r\n") {
			return fmt.Errorf("missing valid immutable-policy inspection credential")
		}
		return nil
	}
	if args[0] == "diagnostics" {
		if !filepath.IsAbs(*output) {
			return fmt.Errorf("diagnostic output requires an absolute path")
		}
		if err := Diagnostics(o.State, *repository, *version, *commit, *runID, *runAttempt, *outcome, *output); err != nil {
			return fmt.Errorf("publication diagnostics unavailable; no private details exported")
		}
		return nil
	}
	if o.Inputs == "" || o.State == "" || o.ReviewedCommit == "" {
		return fmt.Errorf("publication requires --inputs-root, --reviewed-commit and --state")
	}
	client := releasegithub.New(o.Root)
	var err error
	switch args[0] {
	case "prepare":
		if o.Checksums == "" {
			return fmt.Errorf("preparation requires --checksums")
		}
		fmt.Fprintln(stdout, "Publication stage: authenticated-subject-preparation")
		err = Prepare(ctx, o, environment, client)
	case "publish":
		reader := newPublicReader()
		defer reader.close()
		err = executePublication(ctx, o, environment, client, inspectionClient(o.Root, environment["PSST_IMMUTABLE_INSPECTION_TOKEN"]), stageImages, newRegistry, reader, stdout)
	default:
		return fmt.Errorf("unknown container publication command")
	}
	if err != nil {
		return fmt.Errorf("publication stopped; inspect its fixed stage and retained diagnostics, then reconcile before another attempt")
	}
	return nil
}
