package containerbuild

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/scanning"
)

// RunTransfer retains or verifies the complete cross-job payload without replay.
func RunTransfer(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 || args[0] != "collect" && args[0] != "verify" {
		return fmt.Errorf("container-build transfer requires collect or verify")
	}
	flags := flag.NewFlagSet("container-build transfer "+args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var identity scanning.NativeIdentity
	flags.StringVar(&identity.Repository, "repository", "", "exact source repository")
	flags.StringVar(&identity.Version, "version", "", "exact stable version")
	flags.StringVar(&identity.Commit, "commit", "", "full source commit")
	flags.StringVar(&identity.Platform, "platform", "", "native Linux platform")
	sourceKind := flags.String("source-kind", "", "version-tag or planned-main-dispatch")
	private := flags.String("private", "", "native producer private tree for collect")
	transfer := flags.String("transfer", "", "complete reconstructed tree for verify")
	output := flags.String("output", "", "new tree for collect; optional new report for verify")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("transfer accepts flags only")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var result TransferVerification
	var err error
	if args[0] == "collect" {
		if *private == "" || *transfer != "" || *output == "" {
			return fmt.Errorf("transfer collect requires private input and new output")
		}
		result, err = CollectTransfer(ctx, *private, *output, identity, *sourceKind)
	} else {
		if *transfer == "" || *private != "" {
			return fmt.Errorf("transfer verify requires only the reconstructed transfer tree")
		}
		result, err = VerifyTransfer(ctx, *transfer, identity, *sourceKind)
	}
	if err != nil {
		return err
	}
	raw, err := contract.JSON(result.Transfer)
	if err != nil {
		return err
	}
	if args[0] == "verify" && *output != "" {
		if err := writeRecord(*output, raw); err != nil {
			return err
		}
	}
	_, err = stdout.Write(raw)
	return err
}

// HeavyTransferPaths are uploaded separately and restored at the same relative
// paths before VerifyTransfer. No image save/OCI bytes are duplicated in metadata.
func HeavyTransferPaths(descriptor NativeDescriptor) []string {
	var names []string
	for _, key := range []string{"final_archive", "original_archive", "backend_archive", "web_archive"} {
		if file := descriptor.Artifacts[key].File; file != "" {
			names = append(names, "native/"+file)
		}
	}
	return names
}
