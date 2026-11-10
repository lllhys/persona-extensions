package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
)

// writeReceipt is one gated write, kept so a repeated request_id does not make the change twice.
type writeReceipt struct {
	Key       string    `json:"key"`
	Status    string    `json:"status"`
	StartedAt time.Time `json:"started_at"`
	Text      string    `json:"text,omitempty"`
}

type writeStore struct {
	mu      sync.Mutex
	path    string
	entries map[string]writeReceipt
}

func newWriteStore(path string) *writeStore {
	return &writeStore{path: path, entries: map[string]writeReceipt{}}
}

func openWriteStore(dir string) (*writeStore, error) {
	if dir == "" {
		return newWriteStore(""), nil
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	s := newWriteStore(filepath.Join(dir, "write-receipts.json"))
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if len(data) > 2<<20 || json.Unmarshal(data, &s.entries) != nil || s.entries == nil {
		return nil, errors.New("music write receipts are invalid; preserve and inspect the extension data")
	}
	for id, r := range s.entries {
		if len(id) == 0 || len(id) > 128 || (r.Status != "pending" && r.Status != "done") {
			return nil, errors.New("invalid music write receipt")
		}
	}
	return s, nil
}

func (s *writeStore) commit(id, key string, fn func() (string, error)) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.entries[id]; ok {
		if old.Key != key {
			return "", errors.New("request_id was already used for different music parameters")
		}
		if old.Status == "done" {
			return old.Text, nil
		}
		return "", errors.New("the previous music change has an unknown outcome; inspect it before retrying")
	}
	if len(s.entries) >= 256 {
		s.pruneLocked()
		if len(s.entries) >= 256 {
			return "", errors.New("music write receipt history is full; preserve it before more changes")
		}
	}
	s.entries[id] = writeReceipt{Key: key, Status: "pending", StartedAt: time.Now().UTC()}
	if err := s.write(); err != nil {
		delete(s.entries, id)
		return "", err
	}
	text, err := fn()
	if err != nil {
		var written *writtenError
		if errors.As(err, &written) {
			// The write happened; only a follow-up step (playing the new playlist) failed. Keep the receipt as done
			// with that outcome, so the same request_id answers it instead of creating a second playlist.
			s.entries[id] = writeReceipt{Key: key, Status: "done", StartedAt: time.Now().UTC(), Text: written.text}
			if werr := s.write(); werr != nil {
				return "", fmt.Errorf("the music change may have completed; inspect it before retrying: %w", werr)
			}
			return "", written
		}
		if !writeOutcomeUnknown(err) {
			// A refused or failed call that provably wrote nothing: keep no pending receipt, so a retry can run.
			delete(s.entries, id)
			_ = s.write()
			return "", err
		}
		return "", fmt.Errorf("the music change outcome is unknown; inspect it before retrying: %w", err)
	}
	s.entries[id] = writeReceipt{Key: key, Status: "done", StartedAt: time.Now().UTC(), Text: text}
	if err := s.write(); err != nil {
		return "", fmt.Errorf("the music change may have completed; inspect it before retrying: %w", err)
	}
	return text, nil
}

// writtenError is a write that completed but whose follow-up step failed (a created playlist that would not start).
// Its text is what the request_id answers on a retry; it is never treated as "wrote nothing".
type writtenError struct {
	text string
	err  error
}

func (e *writtenError) Error() string { return e.err.Error() }
func (e *writtenError) Unwrap() error { return e.err }

// writeOutcomeUnknown says whether a failed write may have happened: a timeout or a lost connection might have
// completed it, while these errors mean the service refused before changing anything.
func writeOutcomeUnknown(err error) bool {
	var e *ext.Error
	if errors.As(err, &e) {
		switch e.Code {
		case "macos_privacy", "music_not_running", "invalid", "not_found", "denied", "unsupported", "too_large",
			// The connector protocol and cloud services say "nothing was written" in these terms too.
			"not_signed_in", "login_required", "not_configured", "rate_limited", "offline", "no_permission",
			"expired", "unauthorized":
			return false
		}
	}
	return true
}

// pruneLocked drops the oldest finished receipts, keeping pending ones and the newest 128.
func (s *writeStore) pruneLocked() {
	type entry struct {
		id string
		at time.Time
	}
	var done []entry
	for id, r := range s.entries {
		if r.Status == "done" {
			done = append(done, entry{id: id, at: r.StartedAt})
		}
	}
	sort.Slice(done, func(i, j int) bool { return done[i].at.Before(done[j].at) })
	for _, e := range done {
		if len(s.entries) <= 128 {
			break
		}
		delete(s.entries, e.id)
	}
}

func (s *writeStore) write() error {
	if s.path == "" {
		return nil
	}
	data, err := json.Marshal(s.entries)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".receipts-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, s.path); err != nil {
		return err
	}
	if d, err := os.Open(filepath.Dir(s.path)); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
