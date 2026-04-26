package adapter

import (
	"bytes"
	"strings"
	"testing"
)

// TestRingBuffer_Write_SmallFits writes less than the cap; Bytes returns
// the prefix in chronological order with no wrap.
func TestRingBuffer_Write_SmallFits(t *testing.T) {
	t.Parallel()

	r := newRingBuffer(16)
	n, err := r.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != 5 {
		t.Errorf("Write returned n=%d, want 5", n)
	}
	if r.full {
		t.Error("buffer should not be full after partial write")
	}
	if got := string(r.Bytes()); got != "hello" {
		t.Errorf("Bytes = %q, want %q", got, "hello")
	}
}

// TestRingBuffer_Write_ExactFill writes exactly the capacity in one call;
// the buffer becomes full and pos resets to 0.
func TestRingBuffer_Write_ExactFill(t *testing.T) {
	t.Parallel()

	r := newRingBuffer(8)
	in := []byte("abcdefgh")
	n, err := r.Write(in)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != 8 {
		t.Errorf("Write returned n=%d, want 8", n)
	}
	if !r.full {
		t.Error("buffer should be full after exact-cap write")
	}
	if r.pos != 0 {
		t.Errorf("pos = %d, want 0 after wrap", r.pos)
	}
	if got := string(r.Bytes()); got != "abcdefgh" {
		t.Errorf("Bytes = %q, want %q", got, "abcdefgh")
	}
}

// TestRingBuffer_Write_OverflowWrap writes more than the remaining tail
// in a partially-filled buffer, forcing a wrap. Verifies the most recent
// `cap` bytes are preserved in chronological order.
func TestRingBuffer_Write_OverflowWrap(t *testing.T) {
	t.Parallel()

	r := newRingBuffer(8)
	// Fill 6 bytes; pos=6, full=false.
	if _, err := r.Write([]byte("abcdef")); err != nil {
		t.Fatalf("first Write: %v", err)
	}
	// Write 5 bytes — only 2 fit before wrap; remaining 3 wrap to head.
	// After: ring contents in chronological order = "defghijk" (last 8 of "abcdef" + "ghijk").
	if _, err := r.Write([]byte("ghijk")); err != nil {
		t.Fatalf("second Write: %v", err)
	}
	if !r.full {
		t.Error("buffer should be full after wrap")
	}
	if got := string(r.Bytes()); got != "defghijk" {
		t.Errorf("Bytes = %q, want %q", got, "defghijk")
	}
}

// TestRingBuffer_Write_LargerThanCap writes more than the buffer's
// capacity in one call. Only the trailing `cap` bytes are kept.
func TestRingBuffer_Write_LargerThanCap(t *testing.T) {
	t.Parallel()

	r := newRingBuffer(4)
	in := []byte("abcdefghij")
	n, err := r.Write(in)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != len(in) {
		t.Errorf("Write returned n=%d, want %d", n, len(in))
	}
	if !r.full {
		t.Error("buffer should be full after oversized write")
	}
	if got := string(r.Bytes()); got != "ghij" {
		t.Errorf("Bytes = %q, want %q", got, "ghij")
	}
}

// TestRingBuffer_Write_LargerThanCap_AfterPartial mixes a partial fill
// with a write larger than capacity to confirm pos and full get reset
// correctly regardless of prior state.
func TestRingBuffer_Write_LargerThanCap_AfterPartial(t *testing.T) {
	t.Parallel()

	r := newRingBuffer(4)
	if _, err := r.Write([]byte("xy")); err != nil {
		t.Fatalf("first Write: %v", err)
	}
	if _, err := r.Write([]byte("abcdefghij")); err != nil {
		t.Fatalf("second Write: %v", err)
	}
	if !r.full {
		t.Error("buffer should be full")
	}
	if r.pos != 0 {
		t.Errorf("pos = %d, want 0 after oversized write", r.pos)
	}
	if got := string(r.Bytes()); got != "ghij" {
		t.Errorf("Bytes = %q, want %q", got, "ghij")
	}
}

// TestRingBuffer_Write_ManyChunkedWrites confirms behavior parity with
// the reference (byte-by-byte) ring under arbitrary chunked input. We
// run a deterministic stream of writes through both implementations
// and assert the snapshots agree.
func TestRingBuffer_Write_ManyChunkedWrites(t *testing.T) {
	t.Parallel()

	cap := 17
	r := newRingBuffer(cap)
	ref := newReferenceRing(cap)

	chunks := []string{
		"a", "bcdef", "", strings.Repeat("z", 50),
		"hello", strings.Repeat("Q", cap), "tail",
	}
	for _, c := range chunks {
		_, _ = r.Write([]byte(c))
		ref.write([]byte(c))
		if !bytes.Equal(r.Bytes(), ref.bytes()) {
			t.Fatalf("after chunk %q: chunked=%q, reference=%q",
				c, r.Bytes(), ref.bytes())
		}
	}
}

// referenceRing is a byte-by-byte ring used as an oracle in the
// chunked-Write parity test. Mirrors the original (pre-optimization)
// implementation so the new copy-based loop is verifiable against a
// known-good reference.
type referenceRing struct {
	data []byte
	full bool
	pos  int
}

func newReferenceRing(size int) *referenceRing {
	return &referenceRing{data: make([]byte, size)}
}

func (r *referenceRing) write(p []byte) {
	for _, b := range p {
		r.data[r.pos] = b
		r.pos++
		if r.pos == len(r.data) {
			r.pos = 0
			r.full = true
		}
	}
}

func (r *referenceRing) bytes() []byte {
	if !r.full {
		out := make([]byte, r.pos)
		copy(out, r.data[:r.pos])
		return out
	}
	out := make([]byte, len(r.data))
	n := copy(out, r.data[r.pos:])
	copy(out[n:], r.data[:r.pos])
	return out
}
