package trust

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/gofrs/flock"
)

// CompactThresholdBytes is the file size above which `aienvs trust compact`
// suggests rotating. Compact() itself runs unconditionally; callers decide
// when to invoke it.
const CompactThresholdBytes int64 = 1 << 20 // 1 MiB

// compactLockTimeout bounds how long Compact waits for the advisory lock
// before giving up with ErrLocked.
const compactLockTimeout = 5 * time.Second

// ErrLocked is returned when an advisory-locked operation (Compact on
// trust.jsonl, or PendingStore.rewrite on pending.jsonl) cannot acquire
// the lock within compactLockTimeout. The message is intentionally
// generic so it reads correctly for both stores.
var ErrLocked = errors.New("trust: store or queue busy, try again")

// Store owns the append-only trust.jsonl file described in
// docs/spec/trust-store-v1.md.
//
// A Store value is safe for concurrent use from multiple goroutines. Cross
// process concurrency is handled by atomic single-write appends (for
// Append) and by a gofrs/flock advisory lock on a sibling *.lock file (for
// Compact).
type Store struct {
	path string
	mu   sync.Mutex
}

// NewStore returns a Store rooted at path. The file and its parent
// directory are created lazily on first Append.
//
// The path is normalized to absolute via filepath.Abs so Path() always
// returns an absolute path, matching its doc contract. If Abs fails
// (extremely rare — only on cwd lookup failure), the original path is
// retained and lazy Append will surface any path errors.
func NewStore(path string) *Store {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return &Store{path: path}
}

// Path returns the absolute path to trust.jsonl.
func (s *Store) Path() string {
	return s.path
}

// Append validates and appends e to the log. It creates the parent
// directory (mode 0700) and file (mode 0600) on first use.
//
// Concurrency model:
//   - The in-process mutex guards against intra-process interleaving and
//     also bounds how much filesystem state our own goroutines can churn.
//   - Cross-process safety relies on O_APPEND. On POSIX-conforming local
//     filesystems, a write(2) issued through a file descriptor opened
//     with O_APPEND atomically updates the file offset to the current
//     end-of-file and writes the bytes there in a single operation.
//     Two writers using a single Write call per record cannot interleave
//     bytes within a record. Windows provides the equivalent via
//     FILE_APPEND_DATA semantics through os.O_APPEND.
//   - This guarantee assumes a local POSIX filesystem (ext4/xfs/apfs/zfs)
//     or NTFS. Network filesystems without close-to-open consistency —
//     notably classic NFS without O_APPEND server-side enforcement — are
//     out of scope; users running aienvs over such mounts can see
//     interleaved appends. The trust store is documented as a per-user
//     local artifact, so this is acceptable.
func (s *Store) Append(e LogEntry) error {
	if err := ValidateEntry(e); err != nil {
		return err
	}

	buf, err := marshalEntry(e)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("trust: mkdir store dir: %w", err)
	}

	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("trust: open store for append: %w", err)
	}
	defer func() { _ = f.Close() }()

	if _, err := f.Write(buf); err != nil {
		return fmt.Errorf("trust: append write: %w", err)
	}
	return nil
}

// Fold reads the log in order and returns the current state per URL.
// Absent file yields an empty map, not an error.
//
// Unknown ops are ignored to preserve forward compatibility. Malformed
// lines cause Fold to return an error with a line-number hint.
func (s *Store) Fold() (map[string]State, error) {
	entries, err := s.ReadAll()
	if err != nil {
		return nil, err
	}
	return foldEntries(entries), nil
}

// ReadAll returns every entry in order. Used by Compact and by diagnostic
// callers that want the raw history.
//
// Concurrent appends can cause a reader to observe a partially-written
// final line (the writer has done the open(O_APPEND) but not yet the
// write(2), or — on a non-POSIX-strict path — the bytes are not yet
// fully flushed). When the parse error indicates truncation on the last
// line, ReadAll retries up to readAllMaxAttempts times with a short
// backoff. This implements the "fold re-reads on io.UnexpectedEOF up to
// 3 times" rule in docs/spec/trust-store-v1.md (Concurrency).
func (s *Store) ReadAll() ([]LogEntry, error) {
	const readAllMaxAttempts = 3
	var lastErr error
	for attempt := 0; attempt < readAllMaxAttempts; attempt++ {
		entries, err := s.readAllOnce()
		if err == nil {
			return entries, nil
		}
		// Only retry on truncation-style errors that look like the last
		// line was caught mid-write. Other parse errors (genuine
		// corruption, unknown structural issues) fail fast.
		if !isTruncationError(err) {
			return nil, err
		}
		lastErr = err
		// Short backoff to give the concurrent writer time to finish its
		// single Write call. The writer is doing one syscall so the
		// observable window is microseconds; we use millisecond-scale
		// sleeps to absorb scheduler jitter under CI load.
		time.Sleep(time.Duration(10*(attempt+1)) * time.Millisecond)
	}
	return nil, lastErr
}

// readAllOnce performs a single open+parse pass without retry.
func (s *Store) readAllOnce() ([]LogEntry, error) {
	f, err := os.Open(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("trust: open store: %w", err)
	}
	defer func() { _ = f.Close() }()

	return parseLines(f, s.path)
}

// isTruncationError reports whether err indicates a JSON parse failed
// because the input was cut off mid-record — the signature of a reader
// that observed a writer's open() but not its write() yet. We accept
// io.ErrUnexpectedEOF directly and json.SyntaxError as the typical shape
// from json.Unmarshal on a truncated object.
func isTruncationError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var syntaxErr *json.SyntaxError
	return errors.As(err, &syntaxErr)
}

// Compact rewrites the log, retaining the most-recent trust / promote /
// allow-new-shas-* record per URL plus every revoke record. It holds a
// gofrs/flock advisory lock on a sibling *.lock file for the duration of
// the rewrite.
//
// Concurrent Appends race harmlessly: an append to the pre-rename file is
// lost after rename, but callers at compact time are interactive by
// construction and can retry. See docs/spec/trust-store-v1.md.
func (s *Store) Compact() error {
	entries, err := s.ReadAll()
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}

	lockPath := s.path + ".lock"
	lk := flock.New(lockPath)
	ctx, cancel := context.WithTimeout(context.Background(), compactLockTimeout)
	defer cancel()

	locked, err := lk.TryLockContext(ctx, 50*time.Millisecond)
	if err != nil {
		return fmt.Errorf("trust: acquire compact lock: %w", err)
	}
	if !locked {
		return ErrLocked
	}
	defer func() { _ = lk.Unlock() }()

	kept := compactEntries(entries)

	// Write to sibling tmp, fsync, rename atomically over the original.
	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, "trust-*.jsonl.tmp")
	if err != nil {
		return fmt.Errorf("trust: create compact tmp: %w", err)
	}
	tmpPath := tmp.Name()
	// Best-effort cleanup if we return early.
	removeTmp := true
	defer func() {
		if removeTmp {
			_ = os.Remove(tmpPath)
		}
	}()

	w := bufio.NewWriter(tmp)
	for _, e := range kept {
		buf, err := marshalEntry(e)
		if err != nil {
			_ = tmp.Close()
			return fmt.Errorf("trust: marshal during compact: %w", err)
		}
		if _, err := w.Write(buf); err != nil {
			_ = tmp.Close()
			return fmt.Errorf("trust: write during compact: %w", err)
		}
	}
	if err := w.Flush(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("trust: flush compact tmp: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("trust: chmod compact tmp: %w", err)
	}
	// fsync the temp file before rename so a crash between rename and
	// the next reboot cannot resurrect the old log over a half-written
	// new one. This matches the StagedWrite contract in
	// internal/fsroot/safewrite.go and the spec's "fsync-before-rename"
	// requirement (docs/spec/trust-store-v1.md, Compaction).
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("trust: fsync compact tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("trust: close compact tmp: %w", err)
	}

	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("trust: rename compact tmp: %w", err)
	}
	removeTmp = false

	// Best-effort directory fsync to durably record the rename. Windows
	// and many network filesystems refuse to fsync a directory handle;
	// errors are non-fatal here because the file data itself is already
	// durable from the tmp.Sync() above.
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// Size returns the current size of the store file in bytes. Absent file
// returns 0, nil.
func (s *Store) Size() (int64, error) {
	info, err := os.Stat(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	return info.Size(), nil
}

// NeedsCompact reports whether the store size exceeds CompactThresholdBytes.
func (s *Store) NeedsCompact() (bool, error) {
	n, err := s.Size()
	if err != nil {
		return false, err
	}
	return n > CompactThresholdBytes, nil
}

// --- package-level helpers ---

// marshalEntry serializes e to a single JSONL line (including trailing
// newline) suitable for a single write(2) call.
func marshalEntry(e LogEntry) ([]byte, error) {
	if e.TSRaw == "" && !e.TS.IsZero() {
		e.TSRaw = e.TS.UTC().Format(time.RFC3339)
	}
	b, err := json.Marshal(e)
	if err != nil {
		return nil, fmt.Errorf("trust: marshal entry: %w", err)
	}
	b = append(b, '\n')
	return b, nil
}

// parseLines decodes one LogEntry per non-empty line. Malformed lines are
// rejected with a line-number hint; lines with unknown ops are retained
// (foldEntries applies the ignore rule later).
func parseLines(r io.Reader, path string) ([]LogEntry, error) {
	sc := bufio.NewScanner(r)
	// 1 MiB scanner buffer — trust records are short but we allow headroom.
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)

	var out []LogEntry
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e LogEntry
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("trust: parse %s line %d: %w", path, lineNo, err)
		}
		if e.TSRaw != "" {
			if t, err := time.Parse(time.RFC3339, e.TSRaw); err == nil {
				e.TS = t
			}
		}
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("trust: scan %s: %w", path, err)
	}
	return out, nil
}

// appendRawLine writes a raw line to path, used by tests to simulate
// forward-compat records from a newer binary.
func appendRawLine(path, line string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(line + "\n"); err != nil {
		return err
	}
	return nil
}

// foldEntries reduces the ordered log to per-URL state.
func foldEntries(entries []LogEntry) map[string]State {
	m := make(map[string]State)
	for _, e := range entries {
		if e.URL == "" {
			continue
		}
		st := m[e.URL]
		switch e.Op {
		case OpTrust, OpPromote:
			st.CurrentSHA = e.SHA
			st.LastOp = e.Op
			st.LastOpTS = e.TS
			st.Revoked = false
		case OpRevoke:
			st.Revoked = true
			st.CurrentSHA = ""
			st.LastOp = e.Op
			st.LastOpTS = e.TS
		case OpAllowNewSHAsOn:
			st.AllowNewSHAsOn = true
			if e.AllowNewSHAsCooldownSeconds > 0 && !e.TS.IsZero() {
				st.AllowNewSHAsCooldownUntil = e.TS.Add(time.Duration(e.AllowNewSHAsCooldownSeconds) * time.Second)
			} else {
				st.AllowNewSHAsCooldownUntil = time.Time{} // indefinite
			}
		case OpAllowNewSHAsOff:
			st.AllowNewSHAsOn = false
			st.AllowNewSHAsCooldownUntil = time.Time{}
		default:
			// Unknown op — forward-compat: skip.
			continue
		}
		m[e.URL] = st
	}
	return m
}

// compactEntries applies the compaction policy:
//
//   - Keep the most-recent trust / promote record per URL (a later record
//     supersedes it in fold terms; we keep the latest).
//   - Keep the most-recent allow-new-shas-on / allow-new-shas-off record
//     per URL.
//   - Keep every revoke record (audit-grade).
//
// The output is sorted by (TS ascending, URL ascending) to give a stable
// fold order.
func compactEntries(entries []LogEntry) []LogEntry {
	latestTrust := make(map[string]LogEntry)
	latestAllow := make(map[string]LogEntry)
	var revokes []LogEntry

	for _, e := range entries {
		switch e.Op {
		case OpTrust, OpPromote:
			latestTrust[e.URL] = e
		case OpAllowNewSHAsOn, OpAllowNewSHAsOff:
			latestAllow[e.URL] = e
		case OpRevoke:
			revokes = append(revokes, e)
		default:
			// Drop unknown ops on compact — they had no effect on fold and
			// carrying them forward would confuse tooling.
		}
	}

	out := make([]LogEntry, 0, len(latestTrust)+len(latestAllow)+len(revokes))
	for _, e := range latestTrust {
		out = append(out, e)
	}
	for _, e := range latestAllow {
		out = append(out, e)
	}
	out = append(out, revokes...)

	slices.SortFunc(out, func(a, b LogEntry) int {
		if c := a.TS.Compare(b.TS); c != 0 {
			return c
		}
		if c := cmp.Compare(a.URL, b.URL); c != 0 {
			return c
		}
		return cmp.Compare(string(a.Op), string(b.Op))
	})
	return out
}
