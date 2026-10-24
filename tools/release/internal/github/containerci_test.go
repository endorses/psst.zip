package github

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestContainerCIRequiresCurrentTaggedAttemptAndExecutedServerSteps(t *testing.T) {
	b := Binding{Repository: "endorses/psst.zip", Version: "v1.2.3", Commit: strings.Repeat("a", 40), RunID: 12, Attempt: 2}
	for _, failure := range []string{"", "attempt", "skipped-step", "duplicate-job", "head"} {
		t.Run(failure, func(t *testing.T) {
			readRuns := 0
			client := Client{Runner: executor(func(_ context.Context, _ time.Duration, args ...string) ([]byte, error) {
				if strings.Contains(endpoint(args), "/jobs?") {
					jobs := []any{}
					index := int64(1)
					for name, steps := range map[string][]string{"Repository security": {"Scan complete Git history", "Repository check regression tests", "Shared development tooling regressions", "Check distributed project licenses"}, "Backend": {"Lint", "Check dependency notices", "Test", "Build"}, "Web": {"Check dependency notices", "Type check", "Integration tests", "Browser tests", "Disposable administrator authentication lifecycle", "Build", "Verify browser build inventory"}} {
						entries := []any{}
						for _, step := range steps {
							conclusion := "success"
							if failure == "skipped-step" && step == "Browser tests" {
								conclusion = "skipped"
							}
							entries = append(entries, map[string]any{"name": step, "conclusion": conclusion, "started_at": "2026-10-24T00:00:00Z", "completed_at": "2026-10-24T00:00:01Z"})
						}
						head := b.Commit
						if failure == "head" {
							head = strings.Repeat("f", 40)
						}
						job := map[string]any{"id": index, "name": "Exact tagged commit CI / " + name, "run_id": b.RunID, "head_sha": head, "status": "completed", "conclusion": "success", "completed_at": "2026-10-24T00:00:01Z", "steps": entries}
						jobs = append(jobs, job)
						index++
						if failure == "duplicate-job" && name == "Web" {
							jobs = append(jobs, job)
						}
					}
					jobs = append(jobs, map[string]any{"id": 90, "name": "Exact tagged commit CI / Native iOS app, extension and XCTest", "status": "completed", "conclusion": "skipped"})
					return encoded(map[string]any{"total_count": len(jobs), "jobs": jobs})
				}
				readRuns++
				attempt := b.Attempt
				if failure == "attempt" {
					attempt++
				}
				return encoded(map[string]any{"id": b.RunID, "run_attempt": attempt, "head_sha": b.Commit, "head_branch": b.Version, "event": "push", "path": ReleaseWorkflow, "status": "in_progress", "repository": map[string]string{"full_name": b.Repository}, "head_repository": map[string]string{"full_name": b.Repository}})
			})}
			facts, err := client.ContainerCI(context.Background(), b)
			if failure == "" {
				if err != nil || readRuns != 2 || len(facts["jobs"].(map[string]string)) != 3 {
					t.Fatalf("exact container CI rejected: %v", err)
				}
			} else if err == nil {
				t.Fatal("accepted altered CI evidence")
			}
		})
	}
}
