package github

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/process"
)

const ReleaseWorkflow = ".github/workflows/release.yml"
const Issuer = "https://token.actions.githubusercontent.com"
const Predicate = "https://slsa.dev/provenance/v1"

var containerTag = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
var sha256Pattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

type Binding struct {
	Repository string
	Version    string
	Commit     string
	RunID      int64
	Attempt    int64
}

func (b Binding) Validate() error {
	if !RepositoryPattern.MatchString(b.Repository) || !containerTag.MatchString(b.Version) || !CommitPattern.MatchString(b.Commit) || b.RunID < 1 || b.Attempt < 1 {
		return errors.New("invalid exact release evidence identity")
	}
	return nil
}

// ValidateTagPush binds annotated and lightweight tag events to head_commit,
// rather than incorrectly requiring the event's after object to be a commit.
func ValidateTagPush(data []byte, binding Binding) error {
	var event struct {
		Repository struct {
			Visibility string `json:"visibility"`
			Name       string `json:"full_name"`
		} `json:"repository"`
		Ref        string `json:"ref"`
		After      string `json:"after"`
		HeadCommit struct {
			ID string `json:"id"`
		} `json:"head_commit"`
	}
	if !RepositoryPattern.MatchString(binding.Repository) || !containerTag.MatchString(binding.Version) || !CommitPattern.MatchString(binding.Commit) || DecodeAPI(data, &event) != nil || event.Repository.Visibility != "public" || event.Repository.Name != binding.Repository || event.Ref != "refs/tags/"+binding.Version || !CommitPattern.MatchString(event.After) || event.HeadCommit.ID != binding.Commit {
		return errors.New("signing requires the exact public repository push event")
	}
	return nil
}

type attestedSubject struct {
	Name   string            `json:"name"`
	Digest map[string]string `json:"digest"`
}
type statement struct {
	Type      string            `json:"_type"`
	Predicate string            `json:"predicateType"`
	Subjects  []attestedSubject `json:"subject"`
	Details   json.RawMessage   `json:"predicate"`
}
type verification struct {
	Verified struct {
		Signature struct {
			Certificate map[string]json.RawMessage `json:"certificate"`
		} `json:"signature"`
		Timestamps []json.RawMessage `json:"verifiedTimestamps"`
		Statement  statement         `json:"statement"`
	} `json:"verificationResult"`
}

func invocationMatches(s statement, b Binding) bool {
	var predicate struct {
		Build struct {
			Type     string `json:"buildType"`
			External struct {
				Workflow map[string]string `json:"workflow"`
			} `json:"externalParameters"`
			Internal struct {
				GitHub struct {
					Event       string `json:"event_name"`
					Environment string `json:"runner_environment"`
				} `json:"github"`
			} `json:"internalParameters"`
			Resolved []struct {
				URI    string            `json:"uri"`
				Digest map[string]string `json:"digest"`
			} `json:"resolvedDependencies"`
		} `json:"buildDefinition"`
		Run struct {
			Builder  struct{ ID string } `json:"builder"`
			Metadata struct {
				Invocation string `json:"invocationId"`
			} `json:"metadata"`
		} `json:"runDetails"`
	}
	if json.Unmarshal(s.Details, &predicate) != nil {
		return false
	}
	repository, ref := "https://github.com/"+b.Repository, "refs/tags/"+b.Version
	build := predicate.Build
	return s.Type == "https://in-toto.io/Statement/v1" && s.Predicate == Predicate && build.Type == "https://actions.github.io/buildtypes/workflow/v1" &&
		reflect.DeepEqual(build.External.Workflow, map[string]string{"repository": repository, "ref": ref, "path": ReleaseWorkflow}) &&
		len(build.Resolved) == 1 && build.Resolved[0].URI == "git+"+repository+"@"+ref && reflect.DeepEqual(build.Resolved[0].Digest, map[string]string{"gitCommit": b.Commit}) &&
		build.Internal.GitHub.Event == "push" && build.Internal.GitHub.Environment == "github-hosted" && predicate.Run.Builder.ID == repository+"/"+ReleaseWorkflow+"@"+ref &&
		predicate.Run.Metadata.Invocation == fmtInvocation(repository, b.RunID, b.Attempt)
}

// VerifiedSubjects examines only successful gh verifier output. The flags in
// VerifyFile enforce actual certificate, issuer, signer/source digest and hosted
// runner policy; predicate content can never replace signature verification.
func VerifiedSubjects(data []byte, digest string, b Binding, expected map[string]string) error {
	if err := b.Validate(); err != nil || !sha256Pattern.MatchString(digest) || len(data) > 4<<20 {
		return errors.New("invalid attestation verification scope")
	}
	var results []verification
	if err := DecodeAPI(data, &results); err != nil || len(results) == 0 || len(results) > 30 {
		return errors.New("GitHub verifier returned no bounded verified results")
	}
	found := false
	for _, result := range results {
		v := result.Verified
		if len(v.Signature.Certificate) == 0 || len(v.Timestamps) == 0 || v.Statement.Predicate != Predicate || len(v.Statement.Subjects) == 0 || len(v.Statement.Subjects) > 128 {
			return errors.New("verified evidence lacks certificate, witnessed timestamp or subjects")
		}
		if !invocationMatches(v.Statement, b) {
			continue
		}
		covered, pairs := false, map[string]string{}
		duplicate := false
		for _, subject := range v.Statement.Subjects {
			if subject.Name == "" || len(subject.Digest) == 0 {
				return errors.New("invalid verified attestation subject")
			}
			hash := subject.Digest["sha256"]
			if hash == strings.TrimPrefix(digest, "sha256:") {
				covered = true
			}
			// Image index and child digests intentionally share a repository name.
			key := subject.Name + "@sha256:" + hash
			if _, exists := pairs[key]; exists {
				duplicate = true
			}
			pairs[key] = hash
		}
		if covered && !duplicate {
			if expected == nil {
				found = true
			} else if len(expected) == len(pairs) {
				exact := true
				for key, value := range expected {
					if pairs[key] != value {
						exact = false
					}
				}
				found = found || exact
			}
		}
	}
	if !found {
		return errors.New("verified attestation does not cover the exact bytes, subject set and hosted workflow attempt")
	}
	return nil
}

func fmtInvocation(repository string, run, attempt int64) string {
	return repository + "/actions/runs/" + strconv.FormatInt(run, 10) + "/attempts/" + strconv.FormatInt(attempt, 10)
}

// VerifyFile gives gh a private snapshot, verifies that same snapshot did not
// change, then authenticates parsed result identity. expected is nil for a report
// or the full name@digest set for final standard provenance.
func (c Client) VerifyFile(ctx context.Context, name string, b Binding, expected map[string]string) error {
	content, err := boundedFile(name, MaxJSON)
	if err != nil {
		return err
	}
	return c.VerifyBytes(ctx, content, b, expected)
}

// VerifyBytes authenticates exactly the immutable bytes parsed by the caller.
func (c Client) VerifyBytes(ctx context.Context, content []byte, b Binding, expected map[string]string) error {
	if err := b.Validate(); err != nil {
		return err
	}
	if len(content) == 0 || len(content) > MaxJSON {
		return errors.New("evidence bytes exceed their verification bound")
	}
	private, err := os.MkdirTemp("", "psst-evidence-")
	if err != nil {
		return errors.New("private evidence snapshot unavailable")
	}
	defer os.RemoveAll(private)
	file := filepath.Join(private, "report.json")
	if err = os.WriteFile(file, content, 0400); err != nil {
		return errors.New("private evidence snapshot could not be written")
	}
	ref, identity := "refs/tags/"+b.Version, "https://github.com/"+b.Repository+"/"+ReleaseWorkflow+"@refs/tags/"+b.Version
	args := []string{"gh", "attestation", "verify", file, "--hostname", "github.com", "--repo", b.Repository, "--signer-digest", b.Commit, "--source-digest", b.Commit, "--source-ref", ref, "--cert-identity", identity, "--cert-oidc-issuer", Issuer, "--deny-self-hosted-runners", "--predicate-type", Predicate, "--format", "json", "--limit", "30"}
	// Only the production Runner is replaced with an isolated credential/config
	// environment. Explicit fixture executors remain injectable for unit tests.
	if runner, ok := c.Runner.(process.Runner); ok {
		runner.CleanEnv = true
		runner.Env = map[string]string{"PATH": os.Getenv("PATH"), "HOME": private, "GH_CONFIG_DIR": filepath.Join(private, "gh-config"), "GH_PROMPT_DISABLED": "1", "LANG": "C"}
		if token := os.Getenv("GH_TOKEN"); token != "" {
			runner.Env["GH_TOKEN"] = token
		}
		c.Runner = runner
	}
	data, err := c.Command(ctx, 55*time.Second, args...)
	if err != nil {
		return errors.New("GitHub evidence signature verification failed")
	}
	after, err := boundedFile(file, MaxJSON)
	if err != nil || !bytes.Equal(content, after) {
		return errors.New("evidence snapshot changed during signature verification")
	}
	sum := sha256.Sum256(content)
	return VerifiedSubjects(data, "sha256:"+hex.EncodeToString(sum[:]), b, expected)
}
