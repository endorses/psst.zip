package androidrelease

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	releasegithub "github.com/endorses/psst.zip/tools/release/internal/github"
)

var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
var tagPattern = regexp.MustCompile(`^android-v[0-9]+\.[0-9]+\.[0-9]+$`)
var requiredSteps = map[string][]string{
	"Repository security":       {"Reject sensitive files and oversized blobs", "Scan complete Git history", "Repository check regression tests", "Check distributed project licenses", "Check native notice inputs and archive boundaries"},
	"Android and shared module": {"Assemble and test", "Verify actual resolved native dependency notices"},
}

func decodeAPI(data []byte, target any) error { return releasegithub.DecodeAPI(data, target) }
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

type EvidenceRecord = releasegithub.Evidence
type Evidence struct {
	Revision string          `json:"sourceRevision"`
	Android  *EvidenceRecord `json:"android"`
	Security *EvidenceRecord `json:"security"`
}
type JobStep = releasegithub.Step
type CIJob = releasegithub.Job

func Executed(job CIJob, required []string) bool { return releasegithub.Executed(job, required) }
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
		Runs []releasegithub.WorkflowRun `json:"workflow_runs"`
	}
	if err = t.ghJSON(ctx, "repos/"+repository+"/actions/workflows/ci.yml/runs?head_sha="+revision+"&per_page=100", &runs); err != nil {
		return evidence, err
	}
	for _, run := range runs.Runs {
		if !run.Exact(repository, revision) {
			continue
		}
		var response struct {
			Jobs []CIJob `json:"jobs"`
		}
		if err = t.ghJSON(ctx, fmt.Sprintf("repos/%s/actions/runs/%d/attempts/%d/jobs?per_page=100", repository, run.ID, run.Attempt), &response); err != nil {
			return evidence, err
		}
		for name, record := range releasegithub.MatchJobs(run, response.Jobs, requiredSteps) {
			slot := &evidence.Security
			if strings.HasPrefix(name, "Android") {
				slot = &evidence.Android
			}
			if *slot == nil {
				copy := record
				*slot = &copy
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
