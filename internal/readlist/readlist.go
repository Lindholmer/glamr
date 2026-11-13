// Package readlist stores which mentions the user has marked as read.
//
// The list is a JSON file of mention IDs, each with the time after which it can be
// dropped: once a mention is older than the mentions window it isn't shown anyway,
// so its entry is no longer needed. Expired entries are pruned on every refresh.
package readlist

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Entry is one read mention.
type Entry struct {
	ID        string    `json:"id"`
	ExpiresAt time.Time `json:"expires_at"` // when the entry can be removed
}

type file struct {
	Read []Entry `json:"read"`
}

// List is the set of read mentions, backed by a file. It's not safe for concurrent use.
type List struct {
	path    string
	entries map[string]time.Time
}

// DefaultPath is $XDG_STATE_HOME/glamr/read_mentions.json, or ~/.local/state/glamr/read_mentions.json.
func DefaultPath() (string, error) {
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot find home directory: %w", err)
		}
		dir = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(dir, "glamr", "read_mentions.json"), nil
}

// New returns an empty list that is only kept in memory (Save is a no-op).
func New() *List {
	return &List{entries: map[string]time.Time{}}
}

// Load reads the list at path. A missing file is an empty list.
func Load(path string) (*List, error) {
	l := &List{path: path, entries: map[string]time.Time{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return l, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}

	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", path, err)
	}
	for _, e := range f.Read {
		l.entries[e.ID] = e.ExpiresAt
	}
	return l, nil
}

// IsRead reports whether the mention has been marked read.
func (l *List) IsRead(id string) bool {
	_, ok := l.entries[id]
	return ok
}

// Len is the number of entries.
func (l *List) Len() int { return len(l.entries) }

// MarkRead adds a mention, kept until expiresAt, and saves the list.
func (l *List) MarkRead(id string, expiresAt time.Time) error {
	l.entries[id] = expiresAt
	return l.Save()
}

// Prune removes entries that expired before now and saves the list if anything changed.
func (l *List) Prune(now time.Time) error {
	changed := false
	for id, expires := range l.entries {
		if expires.Before(now) {
			delete(l.entries, id)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return l.Save()
}

// Save writes the list atomically (write to a temp file, then rename).
func (l *List) Save() error {
	if l.path == "" {
		return nil // in-memory list
	}
	f := file{Read: make([]Entry, 0, len(l.entries))}
	for id, expires := range l.entries {
		f.Read = append(f.Read, Entry{ID: id, ExpiresAt: expires.UTC()})
	}
	sort.Slice(f.Read, func(i, j int) bool { return f.Read[i].ID < f.Read[j].ID }) // stable file content

	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		return fmt.Errorf("failed to create %s: %w", filepath.Dir(l.path), err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(l.path), ".read_mentions-*.json")
	if err != nil {
		return fmt.Errorf("failed to save read mentions: %w", err)
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to save read mentions: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to save read mentions: %w", err)
	}
	if err := os.Rename(tmp.Name(), l.path); err != nil {
		return fmt.Errorf("failed to save read mentions: %w", err)
	}
	return nil
}
