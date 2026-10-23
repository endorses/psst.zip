package browser

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

func Run(ctx context.Context, root string, args []string, stdout, stderr io.Writer) error {
	if len(args) > 0 && args[0] == "native" {
		return RunNative(ctx, root, args[1:], stdout, stderr)
	}
	if len(args) == 0 || args[0] != "verify" {
		return fmt.Errorf("browser requires verify")
	}
	flags := flag.NewFlagSet("browser verify", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inputs := flags.String("inputs-root", filepath.Join(root, "web"), "browser source/build input root")
	outputDirectory := flags.String("output-directory", "", "actual static build output")
	version := flags.String("version", "", "observed build version")
	revision := flags.String("revision", "", "observed source revision")
	output := flags.String("output", "", "new verification record")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || *outputDirectory == "" || *output == "" {
		return fmt.Errorf("browser verify requires --output-directory and --output")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	facts, err := VerifyLocal(*inputs, *outputDirectory, *version, *revision)
	if err != nil {
		return err
	}
	data, err := contract.JSON(facts)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		os.Remove(*output)
		return fmt.Errorf("could not retain browser verification")
	}
	fmt.Fprintf(stdout, "Verified %d browser inputs and %d generated outputs; source reproduction is not claimed.\n", len(facts.Inputs), len(facts.Outputs))
	return nil
}
