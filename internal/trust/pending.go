package trust

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gofrs/flock"
)

// PendingStore owns the pending.jsonl queue described in
// docs/spec/trust-store-v1.md. Unlike Store, pending has no history — it
// is a queue: Append enqueues, List/Latest inspect, Clear removes.
//
// Pending entries arise from sync observing a known URL with a new SHA
// in a non-interactive path (or an interactive path where we've chosen not
// to prompt mid-sync, per plan decision #9). The user reviews via
// `agent-sync trust pending` and promotes via `agent-sync trust promote`.
type PendingStore struct {
	path string
	mu   sync.Mutex
}

// NewPendingStore returns a PendingStore rooted at path.
//
// The path is normalized to absolute via filepath.Abs so Path() always
// returns an absolute path. If Abs fails (extremely rare), the original
// path is retained.
func NewPendingStore(path string) *PendingStore {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return &PendingStore{path: path}
}

// Path returns the absolute path to pending.jsonl.
func (p *PendingStore) Path() string {
	return p.path
}

// Append enqueues e. If the most-recent entry for e.URL already has the
// same NewSHA, the append is a no-op (spec rule: idempotent for the
// latest pair — keeps repeated syncs of the same manifest from ballooning
// the queue).
func (p *PendingStore) Append(e PendingEntry) error {
	if err := validatePendingEntry(e); err != nil {
		return err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	latest, err := p.latestLocked()
	if err != nil {
		return err
	}
	if prev, ok := latest[e.URL]; ok && prev.NewSHA == e.NewSHA {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(p.path), 0o700); err != nil {
		return fmt.Errorf("trust: mkdir pending dir: %w", err)
	}

	buf, err := marshalPendingEntry(e)
	if err != nil {
		return err
	}

	f, err := os.OpenFile(p.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("trust: open pending for append: %w", err)
	}
	defer func() { _ = f.Close() }()

	if _, err := f.Write(buf); err != nil {
		return fmt.Errorf("trust: append pending: %w", err)
	}
	return nil
}

// List returns every pending entry in arrival order.
func (p *PendingStore) List() ([]PendingEntry, error) {
	f, err := os.Open(p.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("trust: open pending: %w", err)
	}
	defer func() { _ = f.Close() }()

	return parsePendingLines(f, p.path)
}

// Latest returns the latest pending entry per URL, keyed by URL.
func (p *PendingStore) Latest() (map[string]PendingEntry, error) {
	entries, err := p.List()
	if err != nil {
		return nil, err
	}
	return foldPendingLatest(entries), nil
}

// Clear removes every pending entry for url.
func (p *PendingStore) Clear(url string) error {
	return p.rewrite(func(e PendingEntry) bool { return e.URL != url })
}

// ClearAll empties the pending queue.
func (p *PendingStore) ClearAll() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := os.Remove(p.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("trust: clear pending: %w", err)
	}
	return nil
}

// --- internals ---

// latestLocked is the fold without re-acquiring the mutex. Caller must
// hold p.mu.
func (p *PendingStore) latestLocked() (map[string]PendingEntry, error) {
	f, err := os.Open(p.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]PendingEntry{}, nil
		}
		return nil, fmt.Errorf("trust: open pending: %w", err)
	}
	defer func() { _ = f.Close() }()

	entries, err := parsePendingLines(f, p.path)
	if err != nil {
		return nil, err
	}
	return foldPendingLatest(entries), nil
}

// rewrite filters pending entries through keep and rewrites the file.
// Advisory-locked via gofrs/flock; on absent file, returns nil.
func (p *PendingStore) rewrite(keep func(PendingEntry) bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	entries, err := p.List()
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}

	lockPath := p.path + ".lock"
	lk := flock.New(lockPath)
	ctx, cancel := context.WithTimeout(context.Background(), compactLockTimeout)
	defer cancel()
	locked, err := lk.TryLockContext(ctx, 50*time.Millisecond)
	if err != nil {
		return fmt.Errorf("trust: acquire pending lock: %w", err)
	}
	if !locked {
		return ErrLocked
	}
	defer func() { _ = lk.Unlock() }()

	dir := filepath.Dir(p.path)
	tmp, err := os.CreateTemp(dir, "pending-*.jsonl.tmp")
	if err != nil {
		return fmt.Errorf("trust: create pending tmp: %w", err)
	}
	tmpPath := tmp.Name()
	removeTmp := true
	defer func() {
		if removeTmp {
			_ = os.Remove(tmpPath)
		}
	}()

	w := bufio.NewWriter(tmp)
	kept := 0
	for _, e := range entries {
		if !keep(e) {
			continue
		}
		buf, err := marshalPendingEntry(e)
		if err != nil {
			_ = tmp.Close()
			return err
		}
		if _, err := w.Write(buf); err != nil {
			_ = tmp.Close()
			return fmt.Errorf("trust: write pending tmp: %w", err)
		}
		kept++
	}
	if err := w.Flush(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("trust: flush pending tmp: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("trust: chmod pending tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("trust: close pending tmp: %w", err)
	}

	if kept == 0 {
		// No survivors — remove the original rather than leaving an empty file.
		if err := os.Remove(p.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("trust: remove empty pending: %w", err)
		}
		return nil
	}

	if err := os.Rename(tmpPath, p.path); err != nil {
		return fmt.Errorf("trust: rename pending tmp: %w", err)
	}
	removeTmp = false
	return nil
}

// validatePendingEntry checks structural invariants.
func validatePendingEntry(e PendingEntry) error {
	if e.URL == "" {
		return errors.New("trust: pending: url is required")
	}
	if e.TSRaw == "" {
		return errors.New("trust: pending: ts is required")
	}
	// ts must parse as RFC3339; pending entries are sorted/folded by ts so a
	// zero TS would collide with every other zero-TS entry.
	if _, err := time.Parse(time.RFC3339, e.TSRaw); err != nil {
		return fmt.Errorf("trust: pending: ts %q is not RFC3339: %w", e.TSRaw, err)
	}
	if !IsSHA40(e.NewSHA) {
		return fmt.Errorf("trust: pending: new_sha must be 40-hex, got %q", e.NewSHA)
	}
	// Per docs/spec/trust-store-v1.md: a pending entry represents a transition
	// from the currently trusted SHA (old_sha) to a newly observed SHA
	// (new_sha). An empty old_sha would mean there's nothing to promote
	// against, so it's invalid by construction.
	if !IsSHA40(e.OldSHA) {
		return fmt.Errorf("trust: pending: old_sha must be 40-hex, got %q", e.OldSHA)
	}
	return nil
}

// marshalPendingEntry serializes e to one JSONL line including trailing
// newline.
func marshalPendingEntry(e PendingEntry) ([]byte, error) {
	if e.TSRaw == "" && !e.TS.IsZero() {
		e.TSRaw = e.TS.UTC().Format(time.RFC3339)
	}
	b, err := json.Marshal(e)
	if err != nil {
		return nil, fmt.Errorf("trust: marshal pending: %w", err)
	}
	return append(b, '\n'), nil
}

// parsePendingLines decodes pending.jsonl.
func parsePendingLines(r *os.File, path string) ([]PendingEntry, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)

	var out []PendingEntry
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e PendingEntry
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("trust: parse pending %s line %d: %w", path, lineNo, err)
		}
		if e.TSRaw != "" {
			if t, err := time.Parse(time.RFC3339, e.TSRaw); err == nil {
				e.TS = t
			}
		}
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("trust: scan pending %s: %w", path, err)
	}
	return out, nil
}

// foldPendingLatest returns the latest entry per URL.
func foldPendingLatest(entries []PendingEntry) map[string]PendingEntry {
	latest := make(map[string]PendingEntry)
	for _, e := range entries {
		prev, ok := latest[e.URL]
		if !ok || e.TS.After(prev.TS) || (e.TS.Equal(prev.TS) && e.NewSHA != prev.NewSHA) {
			latest[e.URL] = e
		}
	}
	return latest
}
