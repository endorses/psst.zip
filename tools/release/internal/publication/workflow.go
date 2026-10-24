package publication

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	releasegithub "github.com/endorses/psst.zip/tools/release/internal/github"
)

func verifyWorkflow(ctx context.Context, client releasegithub.Client, inputs Inputs) error {
	b := inputs.Binding
	var run struct {
		ID         int64  `json:"id"`
		Attempt    int64  `json:"run_attempt"`
		Status     string `json:"status"`
		Event      string `json:"event"`
		Commit     string `json:"head_sha"`
		Path       string `json:"path"`
		Repository struct {
			Name string `json:"full_name"`
		} `json:"repository"`
		HeadRepository struct {
			Name string `json:"full_name"`
		} `json:"head_repository"`
	}
	if err := client.API(ctx, "GET", fmt.Sprintf("repos/%s/actions/runs/%d/attempts/%d", b.Repository, b.RunID, b.Attempt), &run); err != nil {
		return err
	}
	if run.ID != b.RunID || run.Attempt != b.Attempt || run.Status != "in_progress" || run.Event != "push" || run.Commit != b.Commit || run.Path != releasegithub.ReleaseWorkflow || run.Repository.Name != b.Repository || run.HeadRepository.Name != b.Repository {
		return fmt.Errorf("current run is not the selected active publication workflow")
	}
	var contents struct{ Encoding, Content string }
	if err := client.API(ctx, "GET", "repos/"+b.Repository+"/contents/"+releasegithub.ReleaseWorkflow+"?ref="+b.Commit, &contents); err != nil {
		return err
	}
	remote, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(contents.Content), ""))
	if err != nil || contents.Encoding != "base64" || len(remote) > 16<<20 {
		return fmt.Errorf("trusted workflow content is unavailable")
	}
	local, err := client.Command(ctx, 15*time.Second, "git", "show", b.Commit+":"+releasegithub.ReleaseWorkflow)
	if err != nil || !bytes.Equal(local, remote) {
		return fmt.Errorf("committed publication workflow differs from the active trusted source")
	}
	lines := strings.Split(string(remote), "\n")
	groups, publish := 0, 0
	for i, line := range lines {
		if line == "concurrency:" {
			groups++
			if i+2 >= len(lines) || lines[i+1] != "  group: container-release-publication" || lines[i+2] != "  cancel-in-progress: false" {
				return fmt.Errorf("trusted publication workflow lacks repository-wide serialization")
			}
		}
		if line == "  publish:" {
			publish++
		}
	}
	if groups != 1 || publish != 1 {
		return fmt.Errorf("trusted publication workflow lacks one serialized publication job")
	}
	return nil
}
