package github

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"
)

type gitObject struct {
	Type string `json:"type"`
	SHA  string `json:"sha"`
}

func (c Client) ResolveTag(ctx context.Context, repository, tag string) (string, error) {
	if !RepositoryPattern.MatchString(repository) || !ReleaseTagPattern.MatchString(tag) {
		return "", errors.New("invalid release repository or tag")
	}
	var response struct{ Object gitObject }
	if err := c.API(ctx, "GET", "repos/"+repository+"/git/ref/"+url.PathEscape("tags/"+tag), &response); err != nil {
		return "", err
	}
	for depth := 0; depth <= 8; depth++ {
		if !CommitPattern.MatchString(response.Object.SHA) {
			return "", errors.New("invalid release tag target identity")
		}
		if response.Object.Type == "commit" {
			return response.Object.SHA, nil
		}
		if depth == 8 || response.Object.Type != "tag" {
			return "", errors.New("unsupported or excessively nested release tag")
		}
		if err := c.API(ctx, "GET", "repos/"+repository+"/git/tags/"+response.Object.SHA, &response); err != nil {
			return "", err
		}
	}
	return "", errors.New("release tag resolution exceeded bounds")
}
func (c Client) git(ctx context.Context, args ...string) (string, error) {
	data, err := c.Command(ctx, 15*time.Second, append([]string{"git"}, args...)...)
	return strings.TrimSpace(string(data)), err
}

type CLISource struct {
	Revision string `json:"revision"`
	Version  string `json:"version"`
	RunID    int64  `json:"ci_run"`
}

func (c Client) CLISource(ctx context.Context, repository, tag, ref, dispatcher string) (CLISource, error) {
	var source CLISource
	if !RepositoryPattern.MatchString(repository) || !CLITagPattern.MatchString(tag) || ref != "refs/heads/main" || !CommitPattern.MatchString(dispatcher) {
		return source, errors.New("dispatch from main with an existing stable client/vX.Y.Z tag")
	}
	head, err := c.git(ctx, "rev-parse", "HEAD")
	if err != nil || head != dispatcher {
		return source, errors.New("dispatcher source identity mismatch")
	}
	revision, err := c.git(ctx, "rev-parse", "--verify", "refs/tags/"+tag+"^{commit}")
	if err != nil || !CommitPattern.MatchString(revision) {
		return source, errors.New("selected CLI source tag is unavailable")
	}
	if _, err = c.git(ctx, "merge-base", "--is-ancestor", revision, "HEAD"); err != nil {
		return source, errors.New("CLI source tag is outside reviewed main ancestry")
	}
	remote, err := c.ResolveTag(ctx, repository, tag)
	if err != nil || remote != revision {
		return source, errors.New("selected remote tag differs from reviewed source")
	}
	if err = c.ProtectedMain(ctx, repository); err != nil {
		return source, err
	}
	trusted, err := c.git(ctx, "show", "HEAD:.github/workflows/ci.yml")
	if err != nil {
		return source, err
	}
	candidate, err := c.git(ctx, "show", revision+":.github/workflows/ci.yml")
	if err != nil || trusted != candidate {
		return source, errors.New("tagged CI definition differs from trusted dispatcher; select source using the current reviewed CI definition")
	}
	required := map[string][]string{
		"Repository security": {"Scan complete Git history", "Repository check regression tests"},
		"Go client and CLI":   {"Vet Go client", "Client and CLI race tests", "Build and smoke-test CLI executable once", "Check resolved CLI dependency notices", "Disposable backend and browser crypto interoperability"},
	}
	if _, err = c.git(ctx, "cat-file", "-e", revision+":client/internal/lan/session.go"); err == nil {
		required["Go client and CLI"] = append(required["Go client and CLI"], "Prepare matching embedded LAN browser assets", "Embedded LAN Send and Receive browser interoperability")
	}
	run, err := c.ExecutedCI(ctx, repository, revision, required)
	if err != nil {
		return source, err
	}
	return CLISource{revision, strings.TrimPrefix(tag, "client/v"), run}, nil
}
