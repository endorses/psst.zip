package github

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ContainerCI reads the current version-tag release attempt, not a prior main
// run. The reusable CI jobs must have completed their substantive server/security
// steps; skipped mobile/CLI jobs cannot substitute for those checks.
func (c Client) ContainerCI(ctx context.Context, b Binding) (map[string]any, error) {
	if err := b.Validate(); err != nil {
		return nil, err
	}
	endpoint := fmt.Sprintf("repos/%s/actions/runs/%d/attempts/%d", b.Repository, b.RunID, b.Attempt)
	checkRun := func() error {
		var run struct {
			WorkflowRun
			Status string `json:"status"`
		}
		if err := c.API(ctx, "GET", endpoint, &run); err != nil {
			return err
		}
		if run.ID != b.RunID || int64(run.Attempt) != b.Attempt || run.Revision != b.Commit || run.Branch != b.Version || run.Event != "push" || run.Path != ReleaseWorkflow || run.Repository.Name != b.Repository || run.HeadRepository.Name != b.Repository || run.Status != "in_progress" && run.Status != "completed" {
			return fmt.Errorf("source CI belongs to another tagged run attempt")
		}
		return nil
	}
	if err := checkRun(); err != nil {
		return nil, err
	}
	type containerJob struct {
		Job
		RunID     int64  `json:"run_id"`
		HeadSHA   string `json:"head_sha"`
		Completed string `json:"completed_at"`
	}
	jobs := []containerJob{}
	total := -1
	for page := 1; page <= 20; page++ {
		var response struct {
			Total int            `json:"total_count"`
			Jobs  []containerJob `json:"jobs"`
		}
		if err := c.API(ctx, "GET", fmt.Sprintf("%s/jobs?per_page=100&page=%d", endpoint, page), &response); err != nil {
			return nil, err
		}
		if response.Total < 1 || response.Total > 2000 || len(response.Jobs) > 100 || total >= 0 && response.Total != total {
			return nil, fmt.Errorf("source CI pagination malformed or changed")
		}
		total = response.Total
		jobs = append(jobs, response.Jobs...)
		if len(response.Jobs) < 100 {
			break
		}
	}
	if len(jobs) != total {
		return nil, fmt.Errorf("source CI pagination incomplete")
	}
	prefix := "Exact tagged commit CI / "
	required := map[string][]string{
		"Repository security": {"Scan complete Git history", "Repository check regression tests", "Shared development tooling regressions", "Check distributed project licenses"},
		"Backend":             {"Lint", "Check dependency notices", "Test", "Build"},
		"Web":                 {"Check dependency notices", "Type check", "Integration tests", "Browser tests", "Disposable administrator authentication lifecycle", "Build", "Verify browser build inventory"},
	}
	keys := map[string]string{"Repository security": "security", "Backend": "backend", "Web": "web"}
	evidence := map[string]any{}
	seen := map[int64]bool{}
	for _, job := range jobs {
		if job.ID < 1 || seen[job.ID] || job.Name == "" {
			return nil, fmt.Errorf("source CI job identity repeated or missing")
		}
		seen[job.ID] = true
		if !strings.HasPrefix(job.Name, prefix) {
			continue
		}
		name := strings.TrimPrefix(job.Name, prefix)
		if name == "Android and shared module" || name == "Native iOS app, extension and XCTest" || name == "Go client and CLI" {
			continue
		}
		steps, ok := required[name]
		key := keys[name]
		if !ok || evidence[key] != nil || job.RunID != b.RunID || job.HeadSHA != b.Commit || !Executed(job.Job, steps) {
			return nil, fmt.Errorf("exact server/security CI job was skipped, duplicated or unsuccessful")
		}
		if _, err := time.Parse(time.RFC3339Nano, job.Completed); err != nil {
			return nil, fmt.Errorf("CI job completion evidence missing")
		}
		evidence[key] = map[string]any{"job_id": job.ID, "name": job.Name, "completed_at": job.Completed, "steps": steps}
	}
	if len(evidence) != 3 {
		return nil, fmt.Errorf("source CI lacks complete successful executed server/security jobs")
	}
	if err := checkRun(); err != nil {
		return nil, err
	}
	return map[string]any{"jobs": map[string]string{"security": "success", "backend": "success", "web": "success"}, "run_id": b.RunID, "run_attempt": b.Attempt, "source_workflow": ".github/workflows/ci.yml", "job_evidence": evidence}, nil
}
