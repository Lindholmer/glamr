package readlist

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadMissingFileIsEmpty(t *testing.T) {
	l, err := Load(filepath.Join(t.TempDir(), "nope", "read.json"))
	if err != nil || l.Len() != 0 {
		t.Fatalf("Load = (%v, %v), want empty list", l, err)
	}
}

func TestMarkReadPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "glamr", "read_mentions.json") // dir created on save
	l, _ := Load(path)

	expires := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	if err := l.MarkRead("gitlab:gid://gitlab/Todo/1", expires); err != nil {
		t.Fatal(err)
	}
	if !l.IsRead("gitlab:gid://gitlab/Todo/1") || l.IsRead("github:other") {
		t.Error("IsRead wrong after MarkRead")
	}

	reloaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.IsRead("gitlab:gid://gitlab/Todo/1") {
		t.Error("read mark not persisted")
	}

	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"expires_at": "2026-11-20T10:00:00Z"`) {
		t.Errorf("file should store the expiry timestamp, got:\n%s", data)
	}
}

func TestPruneRemovesExpired(t *testing.T) {
	path := filepath.Join(t.TempDir(), "read.json")
	l, _ := Load(path)
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	_ = l.MarkRead("old", now.Add(-time.Minute))
	_ = l.MarkRead("current", now.Add(time.Hour))

	if err := l.Prune(now); err != nil {
		t.Fatal(err)
	}
	if l.IsRead("old") || !l.IsRead("current") {
		t.Error("Prune should drop only expired entries")
	}

	reloaded, _ := Load(path)
	if reloaded.IsRead("old") || !reloaded.IsRead("current") {
		t.Error("pruned list should be saved")
	}
}

func TestPruneWithoutChangesDoesNotWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "read.json")
	l, _ := Load(path)
	if err := l.Prune(time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("pruning an empty list should not create the file")
	}
}

func TestLoadCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "read.json")
	_ = os.WriteFile(path, []byte("{not json"), 0o644)
	if _, err := Load(path); err == nil {
		t.Error("expected an error for a corrupt file")
	}
}

func TestDefaultPath(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/tmp/state")
	if p, _ := DefaultPath(); p != "/tmp/state/glamr/read_mentions.json" {
		t.Errorf("DefaultPath = %q", p)
	}
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "/home/someone")
	if p, _ := DefaultPath(); p != "/home/someone/.local/state/glamr/read_mentions.json" {
		t.Errorf("DefaultPath = %q", p)
	}
}

func TestInMemoryList(t *testing.T) {
	l := New()
	if err := l.MarkRead("x", time.Now().Add(time.Hour)); err != nil || !l.IsRead("x") {
		t.Errorf("in-memory MarkRead = %v, IsRead = %v", err, l.IsRead("x"))
	}
}

func TestEntriesSortedByExpiry(t *testing.T) {
	l := New()
	base := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	_ = l.MarkRead("late", base.Add(48*time.Hour))
	_ = l.MarkRead("soon", base)
	_ = l.MarkRead("b-mid", base.Add(time.Hour))
	_ = l.MarkRead("a-mid", base.Add(time.Hour))

	var ids []string
	for _, e := range l.Entries() {
		ids = append(ids, e.ID)
	}
	if got := strings.Join(ids, ","); got != "soon,a-mid,b-mid,late" {
		t.Errorf("Entries order = %s", got)
	}
}

func TestUnmark(t *testing.T) {
	path := filepath.Join(t.TempDir(), "read.json")
	l, _ := Load(path)
	_ = l.MarkRead("x", time.Now().Add(time.Hour))
	if err := l.Unmark("x"); err != nil || l.IsRead("x") {
		t.Fatalf("Unmark: err=%v IsRead=%v", err, l.IsRead("x"))
	}
	if reloaded, _ := Load(path); reloaded.IsRead("x") {
		t.Error("Unmark should be saved")
	}
	if err := l.Unmark("never-marked"); err != nil {
		t.Error("unmarking an unknown ID is a no-op")
	}
}
