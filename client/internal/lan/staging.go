package lan

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const markerName = "session.json"
const markerValue = "psst.zip temporary LAN ciphertext v1"

type staging struct {
	path string
	lock *os.File
}

func newStaging() (*staging, error) {
	path, err := os.MkdirTemp("", "psst-lan-")
	if err != nil {
		return nil, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(path)
		}
	}()
	if err = protectStaging(path); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(path, markerName), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	locked, err := lockStaging(lock)
	if err != nil || !locked {
		_ = lock.Close()
		return nil, errors.New("cannot lock private LAN staging")
	}
	if _, err = lock.WriteString(markerValue + "\n"); err != nil {
		_ = lock.Close()
		return nil, err
	}
	if err = lock.Sync(); err != nil {
		_ = lock.Close()
		return nil, err
	}
	cleanup = false
	return &staging{path, lock}, nil
}
func (s *staging) close() error {
	// Windows cannot delete an open locked marker. The containing directory remains
	// private; release the lock immediately before removing only our own directory.
	err := s.lock.Close()
	return errors.Join(err, os.RemoveAll(s.path))
}

// Cleanup removes one explicitly selected crashed session. It never scans or
// deletes other sessions, and refuses a live process's held lock. No session key
// can be recovered, so ciphertext leftovers cannot resume a stopped session.
func Cleanup(path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(filepath.Base(absolute), "psst-lan-") {
		return errors.New("cleanup requires the exact psst-lan-* staging directory")
	}
	if err = protectStaging(absolute); err != nil {
		return err
	}
	root, err := os.OpenRoot(absolute)
	if err != nil {
		return err
	}
	defer root.Close()
	info, err := root.Lstat(markerName)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("LAN staging marker is missing or invalid")
	}
	lock, err := root.OpenFile(markerName, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	locked, err := lockStaging(lock)
	if err != nil || !locked {
		_ = lock.Close()
		return errors.New("LAN session is still active; stop its process before cleanup")
	}
	b := make([]byte, len(markerValue)+2)
	n, err := lock.Read(b)
	if err != nil || string(b[:n]) != markerValue+"\n" {
		_ = lock.Close()
		return errors.New("LAN staging marker is invalid")
	}
	// Refuse a replaced path before deleting by name. All contents are private and
	// RemoveAll itself does not follow any symlink found inside the directory.
	original, err := root.Stat(".")
	if err != nil {
		_ = lock.Close()
		return err
	}
	current, err := os.Lstat(absolute)
	if err != nil || !os.SameFile(original, current) {
		_ = lock.Close()
		return errors.New("LAN staging path changed")
	}
	if err = lock.Close(); err != nil {
		return err
	}
	if err = root.Close(); err != nil {
		return err
	}
	return os.RemoveAll(absolute)
}

// cleanupPending only removes unpublished plaintext names recorded by our own
// in-memory SDK journal. It never walks output directories or removes final files.
func (s *memoryStore) cleanupPending(output string) {
	if output == "" {
		return
	}
	root, err := os.OpenRoot(output)
	if err != nil {
		return
	}
	defer root.Close()
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, raw := range s.records {
		if !strings.HasPrefix(key, "operation/download/") {
			continue
		}
		var journal struct {
			ID      string `json:"id"`
			Pending map[string]struct {
				Temp string `json:"temp"`
			} `json:"pending"`
		}
		if json.Unmarshal(raw, &journal) != nil || !safeUUID(journal.ID) {
			continue
		}
		child, err := root.OpenRoot(journal.ID)
		if err != nil {
			continue
		}
		for _, pending := range journal.Pending {
			if strings.HasPrefix(pending.Temp, ".psst-") && strings.HasSuffix(pending.Temp, ".partial") && safeUUID(strings.TrimSuffix(strings.TrimPrefix(pending.Temp, ".psst-"), ".partial")) {
				_ = child.Remove(pending.Temp)
			}
		}
		_ = child.Close()
	}
}
func safeUUID(value string) bool {
	return len(value) == 36 && strings.Count(value, "-") == 4 && strings.Trim(value, "0123456789abcdef-") == ""
}
