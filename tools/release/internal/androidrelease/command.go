package androidrelease

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
)

// Run exposes one Android command family to the shared development tool.
func Run(ctx context.Context, root string, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "help" {
		fmt.Fprintln(stdout, "psst-release android COMMAND\n\n  validate-source       Bind tag, protected-main ancestry and version\n  verify-apk            Check actual manifest, assets, ELF, alignment and signature\n  sign-apk              Align/sign using protected environment-only credentials\n  package               Package verified APK, exact source, notices and checksums\n  check-publication     Reject existing tags or reused/decreasing version codes\n  report-timings        Summarize normal Gradle timing observations\n  evidence              Inspect executed exact-source Android/security CI\n  protected-main        Require current main protection\n  publication-policy    Require immutable releases, reviewed readiness and tag rules\n  check-update          Local disposable same-signer installed-data fixture")
		return nil
	}
	flags := flag.NewFlagSet("android "+args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	t := New(root)
	type operation func() error
	var execute operation
	required := func(values ...string) error {
		for _, value := range values {
			if value == "" {
				return errors.New("required Android command arguments are missing")
			}
		}
		return nil
	}
	switch args[0] {
	case "validate-source":
		tag := flags.String("tag", "", "Android release tag")
		revision := flags.String("expected-commit", "", "exact source commit")
		main := flags.String("main-ref", "origin/main", "protected main reference")
		version := flags.String("version-file", filepath.Join(root, "android/release-version.properties"), "version properties")
		output := flags.String("output", "", "new output JSON")
		execute = func() error {
			if err := required(*tag, *revision, *output); err != nil {
				return err
			}
			result, err := t.ValidateSource(ctx, *tag, *revision, *main, *version)
			if err != nil {
				return err
			}
			return writeJSON(*output, result)
		}
	case "verify-apk":
		apk := flags.String("apk", "", "APK file")
		sdk := flags.String("sdk-tools", "", "SDK build-tools directory")
		signer := flags.String("signer-sha256", "", "approved public signer fingerprint")
		unsigned := flags.Bool("unsigned", false, "require unsigned APK")
		version := flags.String("version-file", filepath.Join(root, "android/release-version.properties"), "version properties")
		revision := flags.String("revision", "", "source commit")
		output := flags.String("output", "", "new output JSON")
		execute = func() error {
			if err := required(*apk, *sdk, *revision, *output); err != nil {
				return err
			}
			if *unsigned == (*signer != "") {
				return errors.New("choose exactly one of --unsigned or --signer-sha256")
			}
			result, err := t.VerifyAPK(ctx, *apk, *sdk, *signer, *version, *revision, false)
			if err != nil {
				return err
			}
			return writeJSON(*output, result)
		}
	case "sign-apk":
		unsigned := flags.String("unsigned", "", "verified unsigned APK")
		output := flags.String("output", "", "new signed APK")
		sdk := flags.String("sdk-tools", "", "SDK build-tools directory")
		execute = func() error {
			if err := required(*unsigned, *output, *sdk); err != nil {
				return err
			}
			return t.SignAPK(ctx, *unsigned, *output, *sdk)
		}
	case "package":
		apk := flags.String("apk", "", "verified signed APK")
		verification := flags.String("verification", "", "verification JSON")
		tag := flags.String("tag", "", "Android release tag")
		revision := flags.String("revision", "", "source commit")
		output := flags.String("output-dir", "", "new publication directory")
		execute = func() error {
			if err := required(*apk, *verification, *tag, *revision, *output); err != nil {
				return err
			}
			return t.PackageRelease(ctx, *apk, *verification, *tag, *revision, *output)
		}
	case "check-publication":
		listing := flags.String("releases-json", "", "GitHub release list")
		tag := flags.String("tag", "", "Android release tag")
		version := flags.String("version-file", filepath.Join(root, "android/release-version.properties"), "version properties")
		execute = func() error {
			if err := required(*listing, *tag); err != nil {
				return err
			}
			data, err := safeRead(*listing, 8<<20)
			if err != nil {
				return err
			}
			metadata, err := VersionMetadata(*version)
			if err != nil {
				return err
			}
			return CheckPublication(data, *tag, metadata)
		}
	case "report-timings":
		input := flags.String("input", "", "Gradle JSONL timings")
		summary := flags.String("summary", "", "append Markdown summary")
		output := flags.String("output", "", "optional new timing JSON")
		execute = func() error {
			if err := required(*input, *summary); err != nil {
				return err
			}
			report, err := ReportTimings(*input)
			if err != nil {
				return err
			}
			file, err := os.OpenFile(*summary, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0644)
			if err != nil {
				return errors.New("timing summary unavailable")
			}
			_, err = io.WriteString(file, TimingSummary(report))
			closeErr := file.Close()
			if err != nil || closeErr != nil {
				return errors.New("timing summary write failed")
			}
			if *output != "" {
				return writeJSON(*output, report)
			}
			return nil
		}
	case "protected-main", "publication-policy", "evidence":
		repository := flags.String("repository", "", "GitHub owner/name")
		tag := flags.String("tag", "", "Android tag for publication-policy")
		revision := flags.String("revision", "", "source commit for evidence")
		output := flags.String("output", "", "new evidence JSON")
		githubOutput := flags.String("github-output", "", "append evidence reuse outputs")
		execute = func() error {
			if err := required(*repository); err != nil {
				return err
			}
			switch args[0] {
			case "protected-main":
				return t.ProtectedMain(ctx, *repository)
			case "publication-policy":
				return t.ProtectedPublication(ctx, *repository, *tag)
			default:
				if err := required(*revision, *output, *githubOutput); err != nil {
					return err
				}
				evidence, err := t.CIEvidence(ctx, *repository, *revision)
				if err != nil {
					return err
				}
				if err = writeJSON(*output, evidence); err != nil {
					return err
				}
				file, err := os.OpenFile(*githubOutput, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0644)
				if err != nil {
					return errors.New("GitHub output file unavailable")
				}
				_, err = fmt.Fprintf(file, "android_reused=%s\nsecurity_reused=%s\n", strconv.FormatBool(evidence.Android != nil), strconv.FormatBool(evidence.Security != nil))
				closeErr := file.Close()
				if err != nil || closeErr != nil {
					return errors.New("GitHub output write failed")
				}
				return nil
			}
		}
	case "check-update":
		options := UpdateOptions{}
		for _, item := range []struct {
			name   string
			target *string
		}{{"seed-unsigned", &options.Seed}, {"next-unsigned", &options.Next}, {"seed-test-apk", &options.SeedTest}, {"next-test-apk", &options.NextTest}, {"sdk-tools", &options.SDK}, {"adb", &options.ADB}, {"keytool", &options.Keytool}, {"serial", &options.Serial}, {"revision", &options.Revision}, {"output", &options.Output}} {
			flags.StringVar(item.target, item.name, "", item.name)
		}
		execute = func() error {
			if err := required(options.Seed, options.Next, options.SeedTest, options.NextTest, options.SDK, options.ADB, options.Keytool, options.Serial, options.Revision, options.Output); err != nil {
				return err
			}
			return t.CheckUpdate(ctx, options)
		}
	default:
		return errors.New("unknown Android release command")
	}
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected Android command arguments")
	}
	return execute()
}
