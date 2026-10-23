package androidrelease

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
var tagPattern = regexp.MustCompile(`^android-v[0-9]+\.[0-9]+\.[0-9]+$`)
var requiredSteps = map[string][]string{
	"Repository security":       {"Reject sensitive files and oversized blobs", "Scan complete Git history", "Repository check regression tests", "Check distributed project licenses", "Check native notice inputs and archive boundaries"},
	"Android and shared module": {"Assemble and test", "Verify actual resolved native dependency notices"},
}

func decodeAPI(data []byte, target any) error {
	var raw any
	if err := contract.Decode(data, 8<<20, &raw); err != nil {
		return errors.New("invalid GitHub inspection response")
	}
	if err := json.Unmarshal(data, target); err != nil {
		return errors.New("invalid GitHub inspection fields")
	}
	return nil
}
func (t Tool) ghJSON(ctx context.Context, endpoint string, target any) error {
	data, err := t.command(ctx, 45*time.Second, "gh", "api", "-H", "X-GitHub-Api-Version: 2026-03-10", endpoint)
	if err != nil {
		return errors.New("GitHub evidence inspection failed")
	}
	return decodeAPI([]byte(data), target)
}
func (t Tool) ProtectedMain(ctx context.Context, repository string) error {
	if !repositoryPattern.MatchString(repository) {
		return errors.New("invalid repository")
	}
	var branch struct {
		Name      string
		Protected bool
	}
	if err := t.ghJSON(ctx, "repos/"+repository+"/branches/main", &branch); err != nil {
		return err
	}
	if branch.Name != "main" || !branch.Protected {
		return errors.New("Android release requires currently protected main")
	}
	return nil
}

type EvidenceRecord struct {
	RunID   int64    `json:"run_id"`
	Attempt int      `json:"attempt"`
	JobID   int64    `json:"job_id"`
	Steps   []string `json:"steps"`
}
type Evidence struct {
	Revision string          `json:"sourceRevision"`
	Android  *EvidenceRecord `json:"android"`
	Security *EvidenceRecord `json:"security"`
}
type JobStep struct {
	Name       string `json:"name"`
	Conclusion string `json:"conclusion"`
	Started    string `json:"started_at"`
	Completed  string `json:"completed_at"`
}
type CIJob struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	Status     string    `json:"status"`
	Conclusion string    `json:"conclusion"`
	Steps      []JobStep `json:"steps"`
}

func Executed(job CIJob, required []string) bool {
	if job.Status != "completed" || job.Conclusion != "success" {
		return false
	}
	steps := map[string]JobStep{}
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
func (t Tool) CIEvidence(ctx context.Context, repository, revision string) (Evidence, error) {
	evidence := Evidence{Revision: revision}
	if !repositoryPattern.MatchString(repository) || !revisionPattern.MatchString(revision) {
		return evidence, errors.New("invalid repository or source revision")
	}
	trusted, err := t.git(ctx, "show", "origin/main:.github/workflows/ci.yml")
	if err != nil {
		return evidence, err
	}
	candidate, err := t.git(ctx, "show", revision+":.github/workflows/ci.yml")
	if err != nil {
		return evidence, err
	}
	if trusted != candidate {
		return evidence, nil
	}
	var runs struct {
		Runs []struct {
			ID         int64  `json:"id"`
			Attempt    int    `json:"run_attempt"`
			Revision   string `json:"head_sha"`
			Branch     string `json:"head_branch"`
			Event      string `json:"event"`
			Path       string `json:"path"`
			Repository struct {
				Name string `json:"full_name"`
			} `json:"repository"`
			HeadRepository struct {
				Name string `json:"full_name"`
			} `json:"head_repository"`
		} `json:"workflow_runs"`
	}
	if err = t.ghJSON(ctx, "repos/"+repository+"/actions/workflows/ci.yml/runs?head_sha="+revision+"&per_page=100", &runs); err != nil {
		return evidence, err
	}
	for _, run := range runs.Runs {
		if run.ID <= 0 || run.Attempt <= 0 || run.Revision != revision || run.Branch != "main" || run.Event != "push" && run.Event != "workflow_dispatch" || run.Path != ".github/workflows/ci.yml" || run.Repository.Name != repository || run.HeadRepository.Name != repository {
			continue
		}
		var response struct {
			Jobs []CIJob `json:"jobs"`
		}
		if err = t.ghJSON(ctx, fmt.Sprintf("repos/%s/actions/runs/%d/attempts/%d/jobs?per_page=100", repository, run.ID, run.Attempt), &response); err != nil {
			return evidence, err
		}
		for name, checks := range requiredSteps {
			var matches []CIJob
			for _, job := range response.Jobs {
				if job.Name == name {
					matches = append(matches, job)
				}
			}
			slot := &evidence.Security
			if strings.HasPrefix(name, "Android") {
				slot = &evidence.Android
			}
			if *slot == nil && len(matches) == 1 && matches[0].ID > 0 && Executed(matches[0], checks) {
				ordered := append([]string(nil), checks...)
				sort.Strings(ordered)
				*slot = &EvidenceRecord{run.ID, run.Attempt, matches[0].ID, ordered}
			}
		}
		if evidence.Android != nil && evidence.Security != nil {
			break
		}
	}
	return evidence, nil
}
func (t Tool) ProtectedPublication(ctx context.Context, repository, tag string) error {
	if !tagPattern.MatchString(tag) {
		return errors.New("invalid Android tag")
	}
	if t.env("ANDROID_RELEASE_PUBLICATION_READY") != "true" {
		return errors.New("device readiness is pending")
	}
	record := t.env("ANDROID_RELEASE_DEVICE_EVIDENCE")
	if len(record) < 8 || len(record) > 1024 || strings.ContainsAny(record, "\r\n") {
		return errors.New("reviewed device evidence reference is missing")
	}
	signer := t.env("ANDROID_RELEASE_SIGNER_SHA256")
	if len(signer) != 64 {
		return errors.New("public signer identity is missing")
	}
	if _, err := CertificateDigest(signer); err != nil {
		return err
	}
	if err := t.ProtectedMain(ctx, repository); err != nil {
		return err
	}
	var immutable struct{ Enabled bool }
	if err := t.ghJSON(ctx, "repos/"+repository+"/immutable-releases", &immutable); err != nil {
		return err
	}
	if !immutable.Enabled {
		return errors.New("repository immutable releases are not enabled")
	}
	var environment struct {
		Rules []struct {
			Type      string
			Reviewers []json.RawMessage
		} `json:"protection_rules"`
		BranchPolicy struct {
			Protected *bool `json:"protected_branches"`
			Custom    *bool `json:"custom_branch_policies"`
		} `json:"deployment_branch_policy"`
	}
	if err := t.ghJSON(ctx, "repos/"+repository+"/environments/android-release", &environment); err != nil {
		return err
	}
	reviewer := false
	for _, rule := range environment.Rules {
		if rule.Type == "required_reviewers" && len(rule.Reviewers) > 0 {
			reviewer = true
		}
	}
	if !reviewer {
		return errors.New("Android environment requires a configured reviewer")
	}
	if environment.BranchPolicy.Protected == nil || *environment.BranchPolicy.Protected || environment.BranchPolicy.Custom == nil || !*environment.BranchPolicy.Custom {
		return errors.New("Android environment requires an explicit main-only branch policy")
	}
	var branches struct {
		Total    int `json:"total_count"`
		Policies []struct {
			Name string
			Type string
		} `json:"branch_policies"`
	}
	if err := t.ghJSON(ctx, "repos/"+repository+"/environments/android-release/deployment-branch-policies?per_page=100", &branches); err != nil {
		return err
	}
	if branches.Total != 1 || len(branches.Policies) != 1 || branches.Policies[0].Name != "main" || branches.Policies[0].Type != "branch" {
		return errors.New("Android environment must allow only the main branch")
	}
	var summaries []struct {
		ID          int64
		Enforcement string
		Target      string
	}
	if err := t.ghJSON(ctx, "repos/"+repository+"/rulesets?includes_parents=true&per_page=100", &summaries); err != nil {
		return err
	}
	protected := map[string]bool{}
	for _, summary := range summaries {
		if summary.Enforcement != "active" || summary.Target != "tag" || summary.ID <= 0 {
			continue
		}
		var policy struct {
			Conditions struct {
				Refs struct {
					Include []string
					Exclude []string
				} `json:"ref_name"`
			} `json:"conditions"`
			Bypass *[]json.RawMessage `json:"bypass_actors"`
			Rules  []struct{ Type string }
		}
		if err := t.ghJSON(ctx, fmt.Sprintf("repos/%s/rulesets/%d", repository, summary.ID), &policy); err != nil {
			return err
		}
		applies := false
		for _, include := range policy.Conditions.Refs.Include {
			if include == "refs/tags/android-v*" || include == "~ALL" {
				applies = true
			}
		}
		if len(policy.Conditions.Refs.Exclude) > 0 || !applies {
			continue
		}
		if policy.Bypass == nil || len(*policy.Bypass) > 0 {
			return errors.New("Android tag bypass policy is hidden or permits bypass")
		}
		for _, rule := range policy.Rules {
			protected[rule.Type] = true
		}
	}
	if !protected["update"] || !protected["deletion"] {
		return errors.New("Android tags lack immutable protection")
	}
	return nil
}
