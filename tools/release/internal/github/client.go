// Package github owns the bounded GitHub CLI boundary shared by release jobs.
// Credentials stay in gh's environment; server bodies and child diagnostics are
// never copied into public errors. There is no automatic mutation retry.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/process"
)

const APIVersion = "2026-03-10"
const MaxJSON = 16 << 20

var RepositoryPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*$`)
var CommitPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)
var CLITagPattern = regexp.MustCompile(`^client/v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
var ReleaseTagPattern = regexp.MustCompile(`^(client/v|android-v|v)(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

type Executor interface {
	Output(context.Context, time.Duration, ...string) ([]byte, error)
}
type Client struct{ Runner Executor }

func New(root string) Client { return Client{Runner: process.Runner{Dir: root}} }

// Command accepts fixed argument vectors from trusted adapters. Never pass token
// values in argv; gh reads its normal GH_TOKEN/GITHUB_TOKEN environment.
func (c Client) Command(ctx context.Context, timeout time.Duration, args ...string) ([]byte, error) {
	if c.Runner == nil || len(args) == 0 {
		return nil, errors.New("missing GitHub command adapter")
	}
	data, err := c.Runner.Output(ctx, timeout, args...)
	if err != nil {
		return nil, errors.New("GitHub release operation failed; private diagnostics withheld")
	}
	if len(data) > MaxJSON {
		return nil, errors.New("GitHub response exceeds its bound")
	}
	return data, nil
}

// DecodeAPI permits API additions but rejects duplicate keys and malformed JSON.
func DecodeAPI(data []byte, target any) error {
	var raw any
	if err := contract.Decode(data, MaxJSON, &raw); err != nil {
		return errors.New("invalid bounded GitHub response")
	}
	if err := json.Unmarshal(data, target); err != nil {
		return errors.New("invalid GitHub response fields")
	}
	return nil
}
func (c Client) API(ctx context.Context, method, endpoint string, target any, arguments ...string) error {
	if method != "GET" && method != "POST" && method != "PATCH" || !validEndpoint(endpoint) {
		return errors.New("untrusted GitHub API operation")
	}
	args := []string{"gh", "api", "--hostname", "github.com", "-H", "X-GitHub-Api-Version: " + APIVersion, "--method", method, endpoint}
	args = append(args, arguments...)
	data, err := c.Command(ctx, 45*time.Second, args...)
	if err != nil {
		return err
	}
	if target == nil {
		return nil
	}
	return DecodeAPI(data, target)
}

func validEndpoint(endpoint string) bool {
	return (strings.HasPrefix(endpoint, "repos/") || strings.HasPrefix(endpoint, "users/") || strings.HasPrefix(endpoint, "orgs/")) && !strings.ContainsAny(endpoint, "\r\n\\") && !strings.Contains(endpoint, "://") && !strings.Contains(endpoint, "..")
}

// OptionalGET distinguishes an authenticated HTTP 404 from network/permission
// failures. gh --include supplies structured status independently of stderr;
// no textual error-message matching can turn an uncertain result into absence.
func (c Client) OptionalGET(ctx context.Context, endpoint string, target any) (bool, error) {
	if !validEndpoint(endpoint) {
		return false, errors.New("untrusted optional GitHub lookup")
	}
	args := []string{"gh", "api", "--hostname", "github.com", "--include", "-H", "X-GitHub-Api-Version: " + APIVersion, endpoint}
	var data []byte
	var commandErr error
	if runner, ok := c.Runner.(interface {
		Result(context.Context, time.Duration, ...string) (process.Result, error)
	}); ok {
		result, err := runner.Result(ctx, 45*time.Second, args...)
		data, commandErr = result.Stdout, err
	} else {
		data, commandErr = c.Runner.Output(ctx, 45*time.Second, args...)
	}
	if len(data) > MaxJSON {
		return false, errors.New("optional GitHub response exceeds bounds")
	}
	headers, body, ok := strings.Cut(string(data), "\r\n\r\n")
	if !ok {
		headers, body, ok = strings.Cut(string(data), "\n\n")
	}
	line, _, _ := strings.Cut(headers, "\n")
	parts := strings.Fields(line)
	if !ok || len(parts) < 2 || !strings.HasPrefix(parts[0], "HTTP/") {
		return false, errors.New("optional GitHub status is unavailable; absence is unknown")
	}
	status, err := strconv.Atoi(parts[1])
	if err != nil || status != 200 && status != 404 || commandErr != nil && status != 404 {
		return false, errors.New("optional GitHub lookup failed; absence is unknown")
	}
	if status == 404 {
		var missing struct{ Message string }
		if DecodeAPI([]byte(body), &missing) != nil || missing.Message != "Not Found" {
			return false, errors.New("GitHub absence response is malformed")
		}
		return false, nil
	}
	return true, DecodeAPI([]byte(body), target)
}

func (c Client) ProtectedMain(ctx context.Context, repository string) error {
	if !RepositoryPattern.MatchString(repository) {
		return errors.New("invalid GitHub repository")
	}
	var branch struct {
		Name      string
		Protected bool
	}
	if err := c.API(ctx, "GET", "repos/"+repository+"/branches/main", &branch); err != nil {
		return err
	}
	if branch.Name != "main" || !branch.Protected {
		return errors.New("release requires currently protected main")
	}
	return nil
}
