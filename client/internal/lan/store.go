package lan

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/endorses/psst.zip/client"
)

// memoryStore intentionally never serializes private keys to the session disk.
// JSON snapshots preserve the SDK Store's copy-on-write semantics.
type memoryStore struct {
	mu      sync.Mutex
	records map[string][]byte
}

func newMemoryStore() *memoryStore { return &memoryStore{records: make(map[string][]byte)} }
func (s *memoryStore) Get(key string, value any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.records[key]
	if !ok {
		return client.ErrNotFound
	}
	return json.Unmarshal(b, value)
}
func (s *memoryStore) Put(key string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.records[key])
	s.records[key] = b
	return nil
}
func (s *memoryStore) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.records[key])
	delete(s.records, key)
	return nil
}
func (s *memoryStore) Keys(prefix string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var keys []string
	for key := range s.records {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	return keys, nil
}
func (s *memoryStore) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, b := range s.records {
		clear(b)
		delete(s.records, key)
	}
}
