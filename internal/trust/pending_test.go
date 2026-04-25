package trust

import (
	"path/filepath"
	"testing"
)

func newPendingInDir(t *testing.T) *PendingStore {
	t.Helper()
	return NewPendingStore(filepath.Join(t.TempDir(), "pending.jsonl"))
}

func TestPendingEmpty(t *testing.T) {
	t.Parallel()
	p := newPendingInDir(t)

	entries, err := p.List()
	if err != nil {
		t.Fatalf("List on absent file: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("List returned %d entries, want 0", len(entries))
	}
}

func TestPendingAppendAndList(t *testing.T) {
	t.Parallel()
	p := newPendingInDir(t)

	e := PendingEntry{
		TSRaw:  "2026-05-01T12:00:00Z",
		URL:    urlX,
		NewSHA: shaB,
		OldSHA: shaA,
	}
	if err := p.Append(e); err != nil {
		t.Fatalf("Append: %v", err)
	}

	entries, err := p.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("List returned %d entries, want 1", len(entries))
	}
	if entries[0].URL != urlX || entries[0].NewSHA != shaB {
		t.Errorf("List returned %+v, want url=%q new=%q", entries[0], urlX, shaB)
	}
}

func TestPendingAppendIdempotentForLatestPair(t *testing.T) {
	t.Parallel()
	p := newPendingInDir(t)

	e := PendingEntry{TSRaw: "2026-05-01T12:00:00Z", URL: urlX, NewSHA: shaB, OldSHA: shaA}

	// First append lands.
	if err := p.Append(e); err != nil {
		t.Fatalf("Append 1: %v", err)
	}
	// Repeat for the same (url, new_sha) must NOT append (spec rule:
	// "Idempotent: if the (url, new_sha) pair already appears as the latest
	// entry for that URL, the append is skipped").
	if err := p.Append(e); err != nil {
		t.Fatalf("Append 2: %v", err)
	}

	entries, err := p.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 entry after idempotent append, got %d", len(entries))
	}
}

func TestPendingAppendDifferentSHAsBothRetained(t *testing.T) {
	t.Parallel()
	p := newPendingInDir(t)

	entries := []PendingEntry{
		{TSRaw: "2026-05-01T12:00:00Z", URL: urlX, NewSHA: shaB, OldSHA: shaA},
		{TSRaw: "2026-05-02T12:00:00Z", URL: urlX, NewSHA: shaC, OldSHA: shaA},
	}
	for _, e := range entries {
		if err := p.Append(e); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	got, err := p.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 entries, got %d", len(got))
	}
}

func TestPendingLatestPerURL(t *testing.T) {
	t.Parallel()
	p := newPendingInDir(t)

	entries := []PendingEntry{
		{TSRaw: "2026-05-01T12:00:00Z", URL: urlX, NewSHA: shaB, OldSHA: shaA},
		{TSRaw: "2026-05-02T12:00:00Z", URL: urlX, NewSHA: shaC, OldSHA: shaA},
		{TSRaw: "2026-05-01T13:00:00Z", URL: urlY, NewSHA: shaA, OldSHA: ""},
	}
	for _, e := range entries {
		if err := p.Append(e); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	latest, err := p.Latest()
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if len(latest) != 2 {
		t.Errorf("expected 2 URLs in Latest, got %d", len(latest))
	}
	if latest[urlX].NewSHA != shaC {
		t.Errorf("latest[urlX].NewSHA = %q, want %q", latest[urlX].NewSHA, shaC)
	}
	if latest[urlY].NewSHA != shaA {
		t.Errorf("latest[urlY].NewSHA = %q, want %q", latest[urlY].NewSHA, shaA)
	}
}

func TestPendingClear(t *testing.T) {
	t.Parallel()
	p := newPendingInDir(t)

	entries := []PendingEntry{
		{TSRaw: "2026-05-01T12:00:00Z", URL: urlX, NewSHA: shaB, OldSHA: shaA},
		{TSRaw: "2026-05-01T13:00:00Z", URL: urlY, NewSHA: shaA, OldSHA: ""},
	}
	for _, e := range entries {
		if err := p.Append(e); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	if err := p.Clear(urlX); err != nil {
		t.Fatalf("Clear: %v", err)
	}

	got, err := p.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 entry after Clear(urlX), got %d", len(got))
	}
	if got[0].URL != urlY {
		t.Errorf("got[0].URL = %q, want %q", got[0].URL, urlY)
	}
}

func TestPendingClearAll(t *testing.T) {
	t.Parallel()
	p := newPendingInDir(t)

	for _, e := range []PendingEntry{
		{TSRaw: "2026-05-01T12:00:00Z", URL: urlX, NewSHA: shaB, OldSHA: shaA},
		{TSRaw: "2026-05-01T13:00:00Z", URL: urlY, NewSHA: shaA, OldSHA: ""},
	} {
		if err := p.Append(e); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	if err := p.ClearAll(); err != nil {
		t.Fatalf("ClearAll: %v", err)
	}

	got, err := p.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 entries after ClearAll, got %d", len(got))
	}
}

func TestPendingAppendRejectsInvalid(t *testing.T) {
	t.Parallel()
	p := newPendingInDir(t)

	cases := map[string]PendingEntry{
		"empty":       {},
		"missing_url": {TSRaw: "2026-05-01T12:00:00Z", NewSHA: shaB, OldSHA: shaA},
		"bad_new_sha": {TSRaw: "2026-05-01T12:00:00Z", URL: urlX, NewSHA: "nope", OldSHA: shaA},
		"missing_ts":  {URL: urlX, NewSHA: shaB, OldSHA: shaA},
	}
	for name, e := range cases {
		t.Run(name, func(t *testing.T) {
			if err := p.Append(e); err == nil {
				t.Errorf("Append(%+v) returned nil, want error", e)
			}
		})
	}
}
