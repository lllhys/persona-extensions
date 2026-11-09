package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// saveReceipt records the request's lifecycle, not the access token. An uncertain external outcome remains blocked
// until the user inspects Raindrop: it must not turn a process restart into an automatic duplicate write.
type saveReceipt struct {
	Key       string    `json:"key"`
	Status    string    `json:"status"`
	StartedAt time.Time `json:"started_at"`
	Item      raindrop  `json:"item,omitempty"`
}

type saveStore struct {
	mu      sync.Mutex
	path    string
	entries map[string]saveReceipt
}

func newSaveStore(path string) *saveStore {
	return &saveStore{path: path, entries: map[string]saveReceipt{}}
}

func openSaveStore(dir string) (*saveStore, error) {
	if dir == "" {
		return newSaveStore(""), nil
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	s := newSaveStore(filepath.Join(dir, "save-receipts.json"))
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if len(data) > 2<<20 {
		return nil, errors.New("save receipt file is oversized")
	}
	if err := json.Unmarshal(data, &s.entries); err != nil || s.entries == nil {
		return nil, errors.New("save receipt file is invalid; preserve it and inspect the extension data")
	}
	for id, r := range s.entries {
		if len(id) == 0 || len(id) > 128 || (r.Status != "pending" && r.Status != "saved") {
			return nil, errors.New("save receipt file has an invalid entry")
		}
	}
	return s, nil
}

// commit runs fn while serializing writes in this process. A newly loaded store cannot claim another live process's
// result; the hub owns one extension process and restarts stop the old one before starting another.
func (s *saveStore) commit(requestID, key string, fn func() (raindrop, error)) (raindrop, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if previous, ok := s.entries[requestID]; ok {
		if previous.Key != key {
			return raindrop{}, errors.New("request_id was already used for different save parameters")
		}
		if previous.Status == "saved" {
			return previous.Item, nil
		}
		return raindrop{}, errors.New("the previous save's outcome is unknown; check Raindrop before retrying")
	}
	if len(s.entries) >= 256 {
		return raindrop{}, errors.New("the save receipt history is full; preserve it before starting new writes")
	}
	s.entries[requestID] = saveReceipt{Key: key, Status: "pending", StartedAt: time.Now().UTC()}
	if err := s.write(); err != nil {
		delete(s.entries, requestID)
		return raindrop{}, err
	}
	item, err := fn()
	if err != nil {
		return raindrop{}, fmt.Errorf("Raindrop save outcome is unknown; check the library before retrying: %w", err)
	}
	if item.ID <= 0 || item.Link == "" {
		return raindrop{}, errors.New("Raindrop did not return the saved bookmark; check the library before retrying")
	}
	s.entries[requestID] = saveReceipt{Key: key, Status: "saved", StartedAt: time.Now().UTC(), Item: item}
	if err := s.write(); err != nil {
		return raindrop{}, fmt.Errorf("the bookmark may have been saved, but its receipt could not be stored; check Raindrop before retrying: %w", err)
	}
	return item, nil
}

func (s *saveStore) write() error {
	if s.path == "" {
		return nil
	}
	data, err := json.Marshal(s.entries)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".save-receipts-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, s.path); err != nil {
		return err
	}
	if dir, err := os.Open(filepath.Dir(s.path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}
