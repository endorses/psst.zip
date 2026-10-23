package publication

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

type journalRecord struct {
	Sequence  int            `json:"sequence"`
	Binding   string         `json:"binding"`
	Phase     string         `json:"phase"`
	Operation string         `json:"operation"`
	Details   map[string]any `json:"details"`
}
type journal struct {
	file     *os.File
	binding  string
	sequence int
}

func privateDirectory(name string) error {
	if !filepath.IsAbs(name) {
		return fmt.Errorf("publication state must use an absolute path")
	}
	if err := os.MkdirAll(name, 0700); err != nil {
		return fmt.Errorf("private publication state unavailable")
	}
	info, err := os.Lstat(name)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 || !owned(info) {
		return fmt.Errorf("publication state must be a private regular directory")
	}
	return nil
}
func syncDirectory(name string) error {
	f, e := os.Open(name)
	if e != nil {
		return fmt.Errorf("publication directory sync unavailable")
	}
	defer f.Close()
	if f.Sync() != nil {
		return fmt.Errorf("publication directory could not be synced")
	}
	return nil
}
func newJournal(name string, inputs Inputs) (*journal, error) {
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, fmt.Errorf("publication journal exists; reconcile before any new attempt")
	}
	j := &journal{file: f, binding: inputs.Result.Binding}
	b := inputs.Binding
	if err = j.append("begin", "transaction", map[string]any{"repository": b.Repository, "version": b.Version, "commit": b.Commit, "run_id": b.RunID, "attempt": b.Attempt}); err != nil {
		f.Close()
		return nil, err
	}
	if err = syncDirectory(filepath.Dir(name)); err != nil {
		f.Close()
		return nil, err
	}
	return j, nil
}
func (j *journal) append(phase, operation string, details map[string]any) error {
	j.sequence++
	if j.sequence > 128 {
		return fmt.Errorf("publication journal record bound exceeded")
	}
	record := journalRecord{j.sequence, j.binding, phase, operation, details}
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("invalid publication journal record")
	}
	data = append(data, '\n')
	if _, err = j.file.Write(data); err != nil {
		return fmt.Errorf("publication journal could not be written")
	}
	if j.file.Sync() != nil {
		return fmt.Errorf("publication journal could not be synced")
	}
	return nil
}
func (j *journal) mutate(operation string, details map[string]any, action func() error) error {
	if err := j.append("intent", operation, details); err != nil {
		return err
	}
	if err := action(); err != nil {
		_ = j.append("uncertain", operation, map[string]any{"automatic_resume": false})
		return fmt.Errorf("publication operation %s stopped; reconcile retained state instead of replaying", operation)
	}
	return j.append("complete", operation, details)
}

// Diagnostics exports only reviewed identities and fixed operations. Private
// journal details, paths, API bodies and credentials are never projected.
func Diagnostics(state, repository, version, commit string, runID, attempt int64, outcome, output string) error {
	if !repositoryPattern.MatchString(repository) || !versionPattern.MatchString(version) || !commitPattern.MatchString(commit) || runID < 1 || attempt < 1 {
		return fmt.Errorf("invalid publication diagnostic identity")
	}
	if outcome != "success" && outcome != "failure" && outcome != "cancelled" && outcome != "skipped" {
		return fmt.Errorf("invalid publication diagnostic outcome")
	}
	result := map[string]any{"schema_version": 1, "kind": "publication-diagnostics", "repository": repository, "version": version, "commit": commit, "run_id": runID, "attempt": attempt, "step_outcome": outcome, "journal_present": false, "records": []map[string]any{}}
	path := filepath.Join(state, version+".jsonl")
	data, err := read(path)
	if os.IsNotExist(err) {
		data = nil
	} else if err != nil {
		return fmt.Errorf("publication diagnostics unavailable")
	}
	if len(data) > 1<<20 {
		return fmt.Errorf("publication diagnostics exceeded bound")
	}
	if len(data) > 0 {
		rows := []map[string]any{}
		scanner := bufio.NewScanner(bytes.NewReader(data))
		scanner.Buffer(make([]byte, 4096), 1<<20)
		binding := ""
		for scanner.Scan() {
			var r journalRecord
			if contract.Decode(scanner.Bytes(), 1<<20, &r) != nil || r.Sequence != len(rows)+1 || r.Sequence > 128 || !digestPattern.MatchString(r.Binding) || !allowedPhase(r.Phase) || !allowedOperation(r.Operation) {
				return fmt.Errorf("invalid bounded diagnostic journal")
			}
			if r.Sequence == 1 {
				if r.Phase != "begin" || r.Operation != "transaction" {
					return fmt.Errorf("invalid diagnostic transaction")
				}
				binding = r.Binding
				expected, _ := contract.JSON(map[string]any{"repository": repository, "version": version, "commit": commit, "run_id": runID, "attempt": attempt})
				actual, _ := contract.JSON(r.Details)
				if !bytes.Equal(expected, actual) {
					return fmt.Errorf("diagnostic identity differs")
				}
			} else if r.Binding != binding || r.Phase == "begin" {
				return fmt.Errorf("diagnostic journal binding differs")
			}
			rows = append(rows, map[string]any{"sequence": r.Sequence, "phase": r.Phase, "operation": r.Operation})
		}
		if scanner.Err() != nil || len(rows) == 0 {
			return fmt.Errorf("invalid bounded diagnostic journal")
		}
		result["journal_present"], result["records"], result["binding_digest"], result["journal_sha256"] = true, rows, binding, digest(data)
	}
	raw, err := contract.JSON(result)
	if err != nil || len(raw) > 64<<10 {
		return fmt.Errorf("diagnostic output exceeds bounds")
	}
	file, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("diagnostic output already exists or unavailable")
	}
	defer file.Close()
	if _, err = file.Write(raw); err != nil {
		return fmt.Errorf("diagnostic output could not be written")
	}
	return nil
}
func allowedPhase(value string) bool {
	return value == "begin" || value == "intent" || value == "complete" || value == "uncertain" || value == "stopped"
}
func allowedOperation(value string) bool {
	switch value {
	case "transaction", "reserve-draft", "publish-release", "public-readback", "push-backend-index", "push-web-index", "tag-backend", "tag-web":
		return true
	}
	return false
}
