// Package watch implements agent-sync's foreground watch mode: a debounced,
// cancellable fsnotify loop that re-runs a sync when the manifest or
// canonical source changes. It is deliberately not a daemon — it runs in
// the foreground, owns a context cancelled by the caller (SIGINT), and
// exits cleanly with no persisted process state (AGENTS / CLAUDE overlay:
// no background daemons).
package watch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// DefaultDebounce is the quiet period after the last event before a sync
// runs, coalescing bursts (editors write several events per save).
const DefaultDebounce = 500 * time.Millisecond

// Config configures a watch session.
type Config struct {
	// Paths are the files/dirs to watch (the manifest, and the local
	// canonical source when configured). Required.
	Paths []string

	// IgnorePrefixes are path prefixes whose events are ignored — the
	// workspace's .agent-sync/state and reserved output prefixes, so a sync's own
	// writes never retrigger the watcher (self-loop guard).
	IgnorePrefixes []string

	// Debounce overrides DefaultDebounce when > 0.
	Debounce time.Duration

	// OnChange runs one sync. It should be idempotent and yield to an
	// in-progress manual sync (the engine's per-target locks handle that).
	OnChange func(context.Context) error

	// Logger receives progress; defaults to a discard logger.
	Logger *slog.Logger
}

func (c Config) debounce() time.Duration {
	if c.Debounce > 0 {
		return c.Debounce
	}
	return DefaultDebounce
}

func (c Config) logger() *slog.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	return slog.New(slog.NewTextHandler(discard{}, &slog.HandlerOptions{Level: slog.LevelError}))
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// Run watches Paths and runs OnChange (debounced) on every relevant
// change until ctx is cancelled, then returns nil. OnChange errors are
// logged and the watch keeps running — a transient sync failure never
// stops the watcher. Run returns a non-nil error only for a watcher setup
// failure; OnChange errors are surfaced through the callback itself (which
// writes the failure marker `status` reads), never via Run's return value.
func Run(ctx context.Context, cfg Config) error {
	if len(cfg.Paths) == 0 {
		return errors.New("watch: no paths to watch")
	}
	if cfg.OnChange == nil {
		return errors.New("watch: OnChange is required")
	}
	log := cfg.logger()

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("watch: new watcher: %w", err)
	}
	defer func() { _ = w.Close() }()

	// Watch each path AND, for a regular file, its parent directory. Many
	// editors save atomically by writing a temp file and renaming it over
	// the target; fsnotify reports that as a CREATE/RENAME on the parent
	// directory, not a WRITE on the original file — so watching the file
	// alone would miss the most common edit pattern. Events on the parent
	// for unrelated siblings are filtered by the debounce + the eventual
	// no-op sync (and by IgnorePrefixes for .agent-sync/*).
	watched := map[string]bool{}
	addWatch := func(p string) {
		if p == "" || watched[p] {
			return
		}
		if err := w.Add(p); err != nil {
			log.Warn("watch: cannot watch path", "path", p, "err", err)
			return
		}
		watched[p] = true
	}
	for _, p := range cfg.Paths {
		addWatch(p)
		if info, statErr := os.Stat(p); statErr == nil && !info.IsDir() {
			addWatch(filepath.Dir(p))
		}
	}

	debounce := cfg.debounce()
	timer := time.NewTimer(debounce)
	timer.Stop()
	pending := false

	for {
		select {
		case <-ctx.Done():
			return nil

		case event, ok := <-w.Events:
			if !ok {
				return nil
			}
			if cfg.ignored(event.Name) {
				continue
			}
			// Coalesce: (re)arm the debounce timer.
			if !timer.Stop() && pending {
				// drain if it already fired
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(debounce)
			pending = true

		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			log.Warn("watch: fsnotify error", "err", err)

		case <-timer.C:
			pending = false
			log.Info("watch: change detected, syncing")
			if err := cfg.OnChange(ctx); err != nil {
				// Transient: log and keep watching. The OnChange callback owns
				// writing the failure marker that `status` surfaces.
				log.Error("watch: sync failed", "err", err)
			}
		}
	}
}

// ignored reports whether an event path falls under an ignore prefix.
func (c Config) ignored(name string) bool {
	clean := filepath.ToSlash(filepath.Clean(name))
	for _, pre := range c.IgnorePrefixes {
		p := filepath.ToSlash(filepath.Clean(pre))
		if clean == p || strings.HasPrefix(clean, p+"/") {
			return true
		}
	}
	return false
}
