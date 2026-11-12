package scheduler

import (
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"dev.kaesebrot.eu/go/feedrrr/internal/rss"
)

type nopSender struct{}

func (nopSender) Enqueue(string, rss.RSSItem) error { return nil }
func (nopSender) Flush() ([]rss.RSSItem, error)     { return nil, nil }

func guidJob() rss.FeedPoller {
	u, _ := url.Parse("http://example.invalid/feed")
	return rss.NewGUIDJob(*slog.Default(), *u, rss.JobOptions{FetchTimeout: time.Second}, nopSender{})
}

func TestStateEmptySeenRestores(t *testing.T) {
	dir := t.TempDir()
	job := guidJob()
	if !job.Restore(rss.JobState{Seen: map[string]time.Time{}}) {
		t.Fatal("empty seen should restore")
	}
	newStateFile(dir, "j", "src", slog.Default()).save(job)

	if !newStateFile(dir, "j", "src", slog.Default()).load(guidJob()) {
		t.Fatal("state of a job seeded from an empty feed should restore")
	}
}

func TestStateRejectsCorruptAndChangedSource(t *testing.T) {
	dir := t.TempDir()
	job := guidJob()
	job.Restore(rss.JobState{Seen: map[string]time.Time{"a": time.Now()}})
	newStateFile(dir, "j", "src", slog.Default()).save(job)

	if newStateFile(dir, "j", "other", slog.Default()).load(guidJob()) {
		t.Error("state for another source should not restore")
	}
	if err := os.WriteFile(filepath.Join(dir, "j.json"), []byte("{trunc"), 0o600); err != nil {
		t.Fatal(err)
	}
	if newStateFile(dir, "j", "src", slog.Default()).load(guidJob()) {
		t.Error("corrupt state should not restore")
	}
	if newStateFile(dir, "missing", "src", slog.Default()).load(guidJob()) {
		t.Error("missing state should not restore")
	}
}

func TestPrepareStateDir(t *testing.T) {
	if err := prepareStateDir(""); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "a", "b")
	if err := prepareStateDir(dir); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("probe left behind: %v", entries)
	}
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	ro := t.TempDir()
	if err := os.Chmod(ro, 0o500); err != nil {
		t.Fatal(err)
	}
	if err := prepareStateDir(ro); err == nil {
		t.Error("read-only state_dir should fail")
	}
}
