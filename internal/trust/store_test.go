package trust

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func mustEntry(t *testing.T, op Op, url, sha, prevSHA string, ts string) LogEntry {
	t.Helper()
	return LogEntry{
		TSRaw:    ts,
		Op:       op,
		URL:      url,
		SHA:      sha,
		PrevSHA:  prevSHA,
		Source:   SourceCLI,
		Actor:    "tester",
		Hostname: "test-host",
	}
}

const (
	shaA = "1111111111111111111111111111111111111111"
	shaB = "2222222222222222222222222222222222222222"
	shaC = "3333333333333333333333333333333333333333"
	urlX = "https://github.com/example/x"
	urlY = "https://github.com/example/y"
)

func newStoreInDir(t *testing.T) *Store {
	t.Helper()
	return NewStore(filepath.Join(t.TempDir(), "trust.jsonl"))
}

func TestStoreEmptyFold(t *testing.T) {
	t.Parallel()
	s := newStoreInDir(t)

	m, err := s.Fold()
	if err != nil {
		t.Fatalf("Fold on absent file: %v", err)
	}
	if len(m) != 0 {
		t.Errorf("Fold on absent file returned %d entries, want 0", len(m))
	}
}

func TestStoreAppendThenFold(t *testing.T) {
	t.Parallel()
	s := newStoreInDir(t)

	e := mustEntry(t, OpTrust, urlX, shaA, "", "2026-05-01T12:00:00Z")
	if err := s.Append(e); err != nil {
		t.Fatalf("Append: %v", err)
	}

	m, err := s.Fold()
	if err != nil {
		t.Fatalf("Fold: %v", err)
	}
	st, ok := m[urlX]
	if !ok {
		t.Fatalf("expected entry for %q, got none", urlX)
	}
	if st.CurrentSHA != shaA {
		t.Errorf("CurrentSHA = %q, want %q", st.CurrentSHA, shaA)
	}
	if st.LastOp != OpTrust {
		t.Errorf("LastOp = %q, want trust", st.LastOp)
	}
	if st.Revoked {
		t.Error("Revoked = true, want false")
	}
}

func TestStoreRevokeFold(t *testing.T) {
	t.Parallel()
	s := newStoreInDir(t)

	for _, e := range []LogEntry{
		mustEntry(t, OpTrust, urlX, shaA, "", "2026-05-01T12:00:00Z"),
		mustEntry(t, OpRevoke, urlX, "", shaA, "2026-05-02T12:00:00Z"),
	} {
		if err := s.Append(e); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	m, err := s.Fold()
	if err != nil {
		t.Fatalf("Fold: %v", err)
	}
	st := m[urlX]
	if !st.Revoked {
		t.Errorf("Revoked = false, want true after revoke op")
	}
	if st.CurrentSHA != "" {
		t.Errorf("CurrentSHA = %q, want empty after revoke", st.CurrentSHA)
	}
}

func TestStorePromoteUpdatesCurrentSHA(t *testing.T) {
	t.Parallel()
	s := newStoreInDir(t)

	for _, e := range []LogEntry{
		mustEntry(t, OpTrust, urlX, shaA, "", "2026-05-01T12:00:00Z"),
		mustEntry(t, OpPromote, urlX, shaB, shaA, "2026-05-02T12:00:00Z"),
	} {
		if err := s.Append(e); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	m, err := s.Fold()
	if err != nil {
		t.Fatalf("Fold: %v", err)
	}
	st := m[urlX]
	if st.CurrentSHA != shaB {
		t.Errorf("CurrentSHA = %q, want %q after promote", st.CurrentSHA, shaB)
	}
	if st.LastOp != OpPromote {
		t.Errorf("LastOp = %q, want promote", st.LastOp)
	}
}

func TestStoreAllowNewSHAsFlag(t *testing.T) {
	t.Parallel()
	s := newStoreInDir(t)

	on := mustEntry(t, OpAllowNewSHAsOn, urlX, "", "", "2026-05-01T12:00:00Z")
	on.AllowNewSHAsCooldownSeconds = 7 * 24 * 3600
	off := mustEntry(t, OpAllowNewSHAsOff, urlX, "", "", "2026-05-09T12:00:00Z")

	if err := s.Append(on); err != nil {
		t.Fatalf("Append on: %v", err)
	}

	m, err := s.Fold()
	if err != nil {
		t.Fatalf("Fold: %v", err)
	}
	st := m[urlX]
	if !st.AllowNewSHAsOn {
		t.Error("AllowNewSHAsOn = false, want true")
	}
	cooldown := st.AllowNewSHAsCooldownUntil.Sub(time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC))
	wantCooldown := 7 * 24 * time.Hour
	if cooldown != wantCooldown {
		t.Errorf("cooldown window = %v, want %v", cooldown, wantCooldown)
	}

	if err := s.Append(off); err != nil {
		t.Fatalf("Append off: %v", err)
	}
	m, err = s.Fold()
	if err != nil {
		t.Fatalf("Fold: %v", err)
	}
	if m[urlX].AllowNewSHAsOn {
		t.Error("AllowNewSHAsOn = true, want false after off op")
	}
}

func TestStoreUnknownOpIgnored(t *testing.T) {
	t.Parallel()
	s := newStoreInDir(t)

	e := mustEntry(t, OpTrust, urlX, shaA, "", "2026-05-01T12:00:00Z")
	if err := s.Append(e); err != nil {
		t.Fatalf("Append: %v", err)
	}

	// Write a line with an unknown op directly — simulates a forward-compat
	// record from a newer binary. Fold must ignore it, not error.
	if err := appendRawLine(s.Path(), `{"ts":"2026-05-01T13:00:00Z","op":"future-op","url":"`+urlX+`","sha":"","prev_sha":"","source":"cli","actor":"a","hostname":"h"}`); err != nil {
		t.Fatalf("appendRawLine: %v", err)
	}

	m, err := s.Fold()
	if err != nil {
		t.Fatalf("Fold: %v", err)
	}
	if m[urlX].CurrentSHA != shaA {
		t.Errorf("unknown op mutated state: CurrentSHA = %q, want %q", m[urlX].CurrentSHA, shaA)
	}
}

func TestStoreConcurrentAppend(t *testing.T) {
	t.Parallel()
	s := newStoreInDir(t)

	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			e := mustEntry(t, OpTrust, urlX, shaA, "", "2026-05-01T12:00:00Z")
			// Serialize appends via the store's own mutex — we're testing
			// that concurrent callers don't corrupt the file.
			if err := s.Append(e); err != nil {
				t.Errorf("Append %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	// Every line must parse cleanly — fold is the strictest reader.
	m, err := s.Fold()
	if err != nil {
		t.Fatalf("Fold after concurrent appends: %v", err)
	}
	if m[urlX].CurrentSHA != shaA {
		t.Errorf("CurrentSHA = %q, want %q", m[urlX].CurrentSHA, shaA)
	}
}

func TestStoreCompactPreservesState(t *testing.T) {
	t.Parallel()
	s := newStoreInDir(t)

	// Write enough history that compaction has something to collapse.
	entries := []LogEntry{
		mustEntry(t, OpTrust, urlX, shaA, "", "2026-05-01T12:00:00Z"),
		mustEntry(t, OpPromote, urlX, shaB, shaA, "2026-05-02T12:00:00Z"),
		mustEntry(t, OpPromote, urlX, shaC, shaB, "2026-05-03T12:00:00Z"),
		mustEntry(t, OpTrust, urlY, shaA, "", "2026-05-01T13:00:00Z"),
		mustEntry(t, OpRevoke, urlY, "", shaA, "2026-05-02T13:00:00Z"),
	}
	for _, e := range entries {
		if err := s.Append(e); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	before, err := s.Fold()
	if err != nil {
		t.Fatalf("Fold before: %v", err)
	}

	if err := s.Compact(); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	after, err := s.Fold()
	if err != nil {
		t.Fatalf("Fold after: %v", err)
	}

	if len(before) != len(after) {
		t.Errorf("url count drifted: before=%d after=%d", len(before), len(after))
	}
	for url, b := range before {
		a, ok := after[url]
		if !ok {
			t.Errorf("url %q dropped by compact", url)
			continue
		}
		if a.CurrentSHA != b.CurrentSHA {
			t.Errorf("url %q CurrentSHA drift: before=%q after=%q", url, b.CurrentSHA, a.CurrentSHA)
		}
		if a.Revoked != b.Revoked {
			t.Errorf("url %q Revoked drift: before=%v after=%v", url, b.Revoked, a.Revoked)
		}
		if a.LastOp != b.LastOp {
			t.Errorf("url %q LastOp drift: before=%q after=%q", url, b.LastOp, a.LastOp)
		}
	}
}

func TestStoreCompactKeepsAllRevokes(t *testing.T) {
	t.Parallel()
	s := newStoreInDir(t)

	// Revoke history is audit-grade and must survive compaction.
	entries := []LogEntry{
		mustEntry(t, OpTrust, urlX, shaA, "", "2026-05-01T12:00:00Z"),
		mustEntry(t, OpRevoke, urlX, "", shaA, "2026-05-02T12:00:00Z"),
		mustEntry(t, OpTrust, urlX, shaB, "", "2026-05-03T12:00:00Z"),
		mustEntry(t, OpRevoke, urlX, "", shaB, "2026-05-04T12:00:00Z"),
	}
	for _, e := range entries {
		if err := s.Append(e); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	if err := s.Compact(); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	all, err := s.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	revokes := 0
	for _, e := range all {
		if e.Op == OpRevoke {
			revokes++
		}
	}
	if revokes != 2 {
		t.Errorf("revoke count after compact = %d, want 2", revokes)
	}
}

func TestStoreAppendRejectsInvalidEntry(t *testing.T) {
	t.Parallel()
	s := newStoreInDir(t)

	bad := LogEntry{
		// All required fields empty.
	}
	if err := s.Append(bad); err == nil {
		t.Error("Append(empty entry) returned nil, want error")
	}
}

// TestStoreReadAllRetriesOnTruncatedLastLine simulates the race where a
// reader observes the file mid-write: a complete line followed by a
// truncated trailing fragment. ReadAll must succeed by re-reading once
// the writer (here, our test goroutine) has appended the missing
// newline, per docs/spec/trust-store-v1.md (Concurrency: "fold re-reads
// on io.UnexpectedEOF up to 3 times").
func TestStoreReadAllRetriesOnTruncatedLastLine(t *testing.T) {
	t.Parallel()
	s := newStoreInDir(t)

	// Seed a clean record.
	if err := s.Append(mustEntry(t, OpTrust, urlX, shaA, "", "2026-05-01T12:00:00Z")); err != nil {
		t.Fatalf("Append: %v", err)
	}

	// Write a truncated trailing fragment (no closing brace, no
	// newline). On the first ReadAll attempt this should fail with a
	// json.SyntaxError; the retry will see it again. We close the gap
	// concurrently so a later attempt succeeds.
	f, err := os.OpenFile(s.Path(), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open for truncation injection: %v", err)
	}
	if _, err := f.WriteString(`{"ts":"2026-05-02T12:00:00Z","op":"trust","url"`); err != nil {
		_ = f.Close()
		t.Fatalf("write truncated fragment: %v", err)
	}

	// Concurrent goroutine completes the line shortly after ReadAll
	// starts retrying. The retry backoffs are 10ms/20ms/30ms; we
	// complete the line at ~5ms so the second attempt sees the
	// completed file.
	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(5 * time.Millisecond)
		_, _ = f.WriteString(`:"` + urlX + `","sha":"` + shaB + `","prev_sha":"","source":"cli","actor":"a","hostname":"h"}` + "\n")
		_ = f.Close()
	}()

	entries, err := s.ReadAll()
	<-done
	if err != nil {
		t.Fatalf("ReadAll did not retry past truncation: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("entries=%d, want 2 (one seed + one completed)", len(entries))
	}
}
