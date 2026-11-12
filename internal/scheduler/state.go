package scheduler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"dev.kaesebrot.eu/go/feedrrr/internal/rss"
)

// prepareStateDir creates dir and checks it's writable, so a bad mount fails
// at startup instead of every job silently losing its state
func prepareStateDir(dir string) error {
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("state_dir %s: %w", dir, err)
	}
	f, err := os.CreateTemp(dir, ".probe-*")
	if err != nil {
		return fmt.Errorf("state_dir %s is not writable (owned by another user? the container runs as uid 10021): %w", dir, err)
	}
	f.Close()
	return os.Remove(f.Name())
}

// stateFile persists one job's state; a nil stateFile does nothing
type stateFile struct {
	path   string
	source string
	last   []byte
	logger *slog.Logger
}

type savedState struct {
	Source string `json:"source"`
	rss.JobState
}

func newStateFile(dir, name, source string, logger *slog.Logger) *stateFile {
	if dir == "" {
		return nil
	}
	return &stateFile{path: filepath.Join(dir, name+".json"), source: source, logger: logger}
}

// load restores the job, unless there's no usable state for its current source
func (f *stateFile) load(job rss.FeedPoller) bool {
	if f == nil {
		return false
	}
	info, err := os.Stat(f.path)
	if err == nil {
		var data []byte
		if data, err = os.ReadFile(f.path); err == nil {
			return f.restore(job, data, info.ModTime())
		}
	}
	if errors.Is(err, fs.ErrNotExist) {
		f.logger.Info("No saved state, seeding from the feed", "path", f.path)
	} else {
		f.logger.Warn("Couldn't read saved state, seeding from the feed", "path", f.path, "err", err)
	}
	return false
}

func (f *stateFile) restore(job rss.FeedPoller, data []byte, saved time.Time) bool {
	var s savedState
	if err := json.Unmarshal(data, &s); err != nil {
		f.logger.Warn("Saved state is corrupt, seeding from the feed", "path", f.path, "err", err)
		return false
	}
	if s.Source != f.source {
		f.logger.Info("Source changed since state was saved, seeding from the feed", "path", f.path, "saved_source", s.Source)
		return false
	}
	if !job.Restore(s.JobState) {
		f.logger.Warn("Saved state doesn't fit this job (change_mode changed?), seeding from the feed", "path", f.path)
		return false
	}
	f.last = data
	args := []any{"saved", saved.Format(time.RFC3339), "age", time.Since(saved).Round(time.Second)}
	if s.Seen != nil {
		args = append(args, "seen", len(s.Seen))
	}
	if s.LastPubDate != nil {
		args = append(args, "last_pub_date", s.LastPubDate.Format(time.RFC3339))
	}
	f.logger.Info("Restored job state, items published since will be sent", args...)
	return true
}

func (f *stateFile) save(job rss.FeedPoller) {
	if f == nil {
		return
	}
	data, err := json.Marshal(savedState{f.source, job.State()})
	if err != nil {
		f.logger.Warn("Couldn't encode job state", "err", err)
		return
	}
	if bytes.Equal(data, f.last) {
		return
	}
	tmp := f.path + ".tmp"
	if err = os.WriteFile(tmp, data, 0o600); err == nil {
		err = os.Rename(tmp, f.path)
	}
	if err != nil {
		f.logger.Warn("Couldn't save job state, a restart may resend or skip items", "path", f.path, "err", err)
		return
	}
	f.logger.Debug("Saved job state", "path", f.path, "bytes", len(data))
	f.last = data
}
