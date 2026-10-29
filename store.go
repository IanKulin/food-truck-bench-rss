package main

import (
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type Entry struct {
	Slug       string    `json:"slug"`
	URL        string    `json:"url"`
	Title      string    `json:"title"`
	Summary    string    `json:"summary"`
	Author     string    `json:"author"`
	Tag        string    `json:"tag"`
	Published  time.Time `json:"published"`
	Modified   time.Time `json:"modified"`
	FetchedAt  time.Time `json:"fetched_at"`
	DateApprox bool      `json:"date_approx,omitempty"` // dates came from the page header (month precision), not JSON-LD
}

type Store struct {
	path string

	mu       sync.RWMutex
	entries  []Entry
	gen      uint64 // bumped on every successful Replace
	lastPoll time.Time
}

func sortEntries(es []Entry) {
	sort.Slice(es, func(i, j int) bool {
		if !es[i].Published.Equal(es[j].Published) {
			return es[i].Published.After(es[j].Published)
		}
		return es[i].Slug < es[j].Slug
	})
}

// OpenStore loads path if present. A missing or corrupt file yields an empty store.
func OpenStore(path string) *Store {
	s := &Store{path: path}
	b, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			slog.Error("reading data file, starting empty", "err", err)
		}
		return s
	}
	var es []Entry
	if err := json.Unmarshal(b, &es); err != nil {
		slog.Error("corrupt data file, starting empty", "err", err)
		return s
	}
	sortEntries(es)
	s.entries = es
	return s
}

// Snapshot returns a copy of the current entries.
func (s *Store) Snapshot() []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Entry(nil), s.entries...)
}

// Generation changes whenever the stored entries do.
func (s *Store) Generation() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.gen
}

func (s *Store) LastPoll() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastPoll
}

func (s *Store) MarkPolled(t time.Time) {
	s.mu.Lock()
	s.lastPoll = t
	s.mu.Unlock()
}

// Replace writes es to disk atomically and, only on success, swaps the snapshot.
func (s *Store) Replace(es []Entry) error {
	es = append([]Entry(nil), es...)
	sortEntries(es)
	if err := writeFileAtomic(s.path, es); err != nil {
		return err
	}
	s.mu.Lock()
	s.entries = es
	s.gen++
	s.mu.Unlock()
	return nil
}

func writeFileAtomic(path string, es []Entry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(es, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
