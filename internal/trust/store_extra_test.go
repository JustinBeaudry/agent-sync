package trust

import "testing"

func TestStore_PathSizeNeedsCompact(t *testing.T) {
	t.Parallel()
	s := newStoreInDir(t)

	if s.Path() == "" {
		t.Fatal("Path should be non-empty")
	}

	// Empty (nonexistent) store reports size 0 and no compaction needed.
	n, err := s.Size()
	if err != nil {
		t.Fatalf("Size: %v", err)
	}
	if n != 0 {
		t.Fatalf("Size of empty store = %d, want 0", n)
	}
	need, err := s.NeedsCompact()
	if err != nil {
		t.Fatalf("NeedsCompact: %v", err)
	}
	if need {
		t.Fatal("empty store should not need compaction")
	}

	// After an append, size grows but stays well under the 1 MiB threshold.
	if err := s.Append(mustEntry(t, OpTrust, urlX, shaA, "", "2026-05-01T12:00:00Z")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	n, err = s.Size()
	if err != nil {
		t.Fatalf("Size: %v", err)
	}
	if n <= 0 {
		t.Fatalf("Size after append = %d, want > 0", n)
	}
	if n >= CompactThresholdBytes {
		t.Fatalf("unexpectedly large store: %d", n)
	}
}

func TestStore_CompactPreservesFold(t *testing.T) {
	t.Parallel()
	s := newStoreInDir(t)
	for i := 0; i < 3; i++ {
		if err := s.Append(mustEntry(t, OpTrust, urlX, shaA, "", "2026-05-01T12:00:00Z")); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if err := s.Compact(); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	m, err := s.Fold()
	if err != nil {
		t.Fatalf("Fold: %v", err)
	}
	if _, ok := m[urlX]; !ok {
		t.Fatalf("compaction dropped the trusted entry for %q", urlX)
	}
}

func TestPendingEntry_IsZero(t *testing.T) {
	if !(PendingEntry{}).IsZero() {
		t.Fatal("zero-value PendingEntry should be IsZero")
	}
	if (PendingEntry{URL: "https://example/x"}).IsZero() {
		t.Fatal("PendingEntry with a URL should not be IsZero")
	}
}
