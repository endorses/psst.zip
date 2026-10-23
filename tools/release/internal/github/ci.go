package github

import (
	"context"
	"errors"
	"fmt"
	"sort"
)

type Step struct {
	Name       string `json:"name"`
	Conclusion string `json:"conclusion"`
	Started    string `json:"started_at"`
	Completed  string `json:"completed_at"`
}
type Job struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	Steps      []Step `json:"steps"`
}
type Evidence struct {
	RunID   int64    `json:"run_id"`
	Attempt int      `json:"attempt"`
	JobID   int64    `json:"job_id"`
	Steps   []string `json:"steps"`
}
type WorkflowRun struct {
	ID         int64  `json:"id"`
	Attempt    int    `json:"run_attempt"`
	Revision   string `json:"head_sha"`
	Branch     string `json:"head_branch"`
	Event      string `json:"event"`
	Path       string `json:"path"`
	Conclusion string `json:"conclusion"`
	Repository struct {
		Name string `json:"full_name"`
	} `json:"repository"`
	HeadRepository struct {
		Name string `json:"full_name"`
	} `json:"head_repository"`
}

func Executed(job Job, required []string) bool {
	if job.Status != "completed" || job.Conclusion != "success" || len(job.Steps) > 256 {
		return false
	}
	steps := map[string]Step{}
	for _, step := range job.Steps {
		if _, exists := steps[step.Name]; exists {
			return false
		}
		steps[step.Name] = step
	}
	for _, name := range required {
		step, ok := steps[name]
		if !ok || step.Conclusion != "success" || step.Started == "" || step.Completed == "" {
			return false
		}
	}
	return true
}
func (run WorkflowRun) Exact(repository, revision string) bool {
	return run.ID > 0 && run.Attempt > 0 && run.Revision == revision && run.Branch == "main" && (run.Event == "push" || run.Event == "workflow_dispatch") && run.Path == ".github/workflows/ci.yml" && run.Repository.Name == repository && run.HeadRepository.Name == repository
}
func MatchJobs(run WorkflowRun, jobs []Job, required map[string][]string) map[string]Evidence {
	passed := map[string]Evidence{}
	for name, steps := range required {
		var matches []Job
		for _, job := range jobs {
			if job.Name == name {
				matches = append(matches, job)
			}
		}
		if len(matches) == 1 && matches[0].ID > 0 && Executed(matches[0], steps) {
			ordered := append([]string(nil), steps...)
			sort.Strings(ordered)
			passed[name] = Evidence{run.ID, run.Attempt, matches[0].ID, ordered}
		}
	}
	return passed
}
func (c Client) Runs(ctx context.Context, repository, revision string, page int) ([]WorkflowRun, error) {
	if !RepositoryPattern.MatchString(repository) || !CommitPattern.MatchString(revision) || page < 1 || page > 5 {
		return nil, errors.New("invalid exact-source CI lookup")
	}
	var response struct {
		Runs []WorkflowRun `json:"workflow_runs"`
	}
	if err := c.API(ctx, "GET", fmt.Sprintf("repos/%s/actions/workflows/ci.yml/runs?head_sha=%s&per_page=100&page=%d", repository, revision, page), &response); err != nil {
		return nil, err
	}
	if len(response.Runs) > 100 {
		return nil, errors.New("CI run page exceeds inspection bound")
	}
	return response.Runs, nil
}
func (c Client) Jobs(ctx context.Context, repository string, run WorkflowRun) ([]Job, error) {
	if !RepositoryPattern.MatchString(repository) || run.ID <= 0 || run.Attempt <= 0 {
		return nil, errors.New("invalid CI job invocation")
	}
	var response struct {
		Jobs []Job `json:"jobs"`
	}
	if err := c.API(ctx, "GET", fmt.Sprintf("repos/%s/actions/runs/%d/attempts/%d/jobs?per_page=100", repository, run.ID, run.Attempt), &response); err != nil {
		return nil, err
	}
	if len(response.Jobs) > 100 {
		return nil, errors.New("CI job page exceeds inspection bound")
	}
	return response.Jobs, nil
}

// ExecutedCI requires all selected jobs in one exact successful invocation. The
// Android adapter may explicitly reuse MatchJobs across compatible invocations.
func (c Client) ExecutedCI(ctx context.Context, repository, revision string, required map[string][]string) (int64, error) {
	for page := 1; page <= 5; page++ {
		runs, err := c.Runs(ctx, repository, revision, page)
		if err != nil {
			return 0, err
		}
		for _, run := range runs {
			if !run.Exact(repository, revision) || run.Conclusion != "success" {
				continue
			}
			jobs, err := c.Jobs(ctx, repository, run)
			if err != nil {
				return 0, err
			}
			if len(MatchJobs(run, jobs, required)) == len(required) {
				return run.ID, nil
			}
		}
		if len(runs) < 100 {
			break
		}
	}
	return 0, errors.New("exact tagged source needs successful executed CLI and security checks on main; run CI at that revision with cli_only=true, then retry")
}
