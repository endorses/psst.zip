package evaluation

import (
	"context"
	"flag"
	"fmt"
	"io"
	"path/filepath"
)

func Run(ctx context.Context, root string, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("evaluate", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var o Options
	flags.StringVar(&o.Binding.Repository, "repository", "", "exact source repository")
	flags.StringVar(&o.Binding.Version, "version", "", "stable version tag")
	flags.StringVar(&o.Binding.Commit, "commit", "", "exact source commit")
	flags.Int64Var(&o.Binding.RunID, "run-id", 0, "current tagged workflow run")
	flags.Int64Var(&o.Binding.Attempt, "attempt", 0, "current workflow attempt")
	flags.StringVar(&o.EventSHA, "event-sha", "", "exact push event object")
	flags.StringVar(&o.Candidate, "candidate", "", "authenticated resolved base candidate")
	flags.StringVar(&o.CommonCollection, "common", "", "retained common upstream original collection")
	flags.StringVar(&o.Prepared, "prepared", "", "new assembled deployment/source directory")
	flags.StringVar(&o.Output, "output", "", "new source evaluation/result directory")
	flags.StringVar(&o.CaddyGoImage, "caddy-go-image", "", "immutable original Caddy compiler builder")
	flags.StringVar(&o.MigrationNotes, "migration-notes", "Stopped checkpoint required; preserve existing administrator and storage.", "deployment checkpoint notes")
	flags.StringVar(&o.RollbackNotes, "rollback-notes", "Restore the matching checkpoint into isolated storage before activation.", "isolated rollback notes")
	amd64 := flags.String("amd64-inputs", "", "complete authenticated native transfer tree")
	arm64 := flags.String("arm64-inputs", "", "complete authenticated native transfer tree")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("invalid evaluate arguments")
	}
	if flags.NArg() != 0 || *amd64 == "" || *arm64 == "" {
		return fmt.Errorf("evaluate requires exact tagged source and both native input trees")
	}
	for _, name := range []*string{&o.Candidate, &o.CommonCollection, &o.Prepared, &o.Output, amd64, arm64} {
		if *name == "" {
			return fmt.Errorf("evaluation path missing")
		}
		absolute, err := filepath.Abs(*name)
		if err != nil {
			return err
		}
		*name = absolute
	}
	o.Native = map[string]string{"linux/amd64": *amd64, "linux/arm64": *arm64}
	_, err := (Evaluator{Root: root}).Evaluate(ctx, o)
	if err == nil {
		fmt.Fprintln(output, "Authenticated and evaluated both exact native pairs; one source release result is ready for the workflow attestation.")
	}
	return err
}
