package contract

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestWriteFrame_RoundTrips(t *testing.T) {
	t.Parallel()

	payload := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`)

	var buf bytes.Buffer
	if err := WriteFrame(&buf, payload); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	got, err := ReadFrame(&buf, DefaultMaxFrameBytes)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch: want %q got %q", payload, got)
	}
}

func TestWriteFrame_HeaderShape(t *testing.T) {
	t.Parallel()

	payload := []byte(`{}`)

	var buf bytes.Buffer
	if err := WriteFrame(&buf, payload); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	frame := buf.String()
	wantPrefix := "Content-Length: 2\r\nContent-Type: application/aienvs-v1+json; charset=utf-8\r\n\r\n"
	if !strings.HasPrefix(frame, wantPrefix) {
		t.Fatalf("frame header mismatch:\nwant prefix %q\ngot         %q", wantPrefix, frame)
	}
	if got := frame[len(wantPrefix):]; got != "{}" {
		t.Fatalf("frame body mismatch: want %q got %q", "{}", got)
	}
}

func TestReadFrame_AcceptsCaseInsensitiveHeaders(t *testing.T) {
	t.Parallel()

	body := "null"
	raw := "content-length: 4\r\nCONTENT-TYPE: application/aienvs-v1+json; charset=utf-8\r\n\r\n" + body

	got, err := ReadFrame(strings.NewReader(raw), DefaultMaxFrameBytes)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if string(got) != body {
		t.Fatalf("body mismatch: want %q got %q", body, got)
	}
}

func TestReadFrame_AcceptsMissingContentTypeForBackwardCompat(t *testing.T) {
	// LSP base protocol allows omitting Content-Type. We mirror that to
	// stay forward-compatible with stricter LSP toolchains, while
	// WriteFrame always emits the aienvs Content-Type for clarity.
	t.Parallel()

	raw := "Content-Length: 5\r\n\r\nhello"
	got, err := ReadFrame(strings.NewReader(raw), DefaultMaxFrameBytes)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("body mismatch: want %q got %q", "hello", got)
	}
}

func TestReadFrame_RejectsMissingContentLength(t *testing.T) {
	t.Parallel()

	raw := "Content-Type: application/aienvs-v1+json; charset=utf-8\r\n\r\nhi"
	_, err := ReadFrame(strings.NewReader(raw), DefaultMaxFrameBytes)
	if !errors.Is(err, ErrMissingContentLength) {
		t.Fatalf("want ErrMissingContentLength, got %v", err)
	}
}

func TestReadFrame_RejectsNonNumericContentLength(t *testing.T) {
	t.Parallel()

	raw := "Content-Length: notanumber\r\n\r\n"
	_, err := ReadFrame(strings.NewReader(raw), DefaultMaxFrameBytes)
	if !errors.Is(err, ErrMalformedHeader) {
		t.Fatalf("want ErrMalformedHeader, got %v", err)
	}
}

func TestReadFrame_RejectsNegativeContentLength(t *testing.T) {
	t.Parallel()

	raw := "Content-Length: -1\r\n\r\n"
	_, err := ReadFrame(strings.NewReader(raw), DefaultMaxFrameBytes)
	if !errors.Is(err, ErrMalformedHeader) {
		t.Fatalf("want ErrMalformedHeader, got %v", err)
	}
}

func TestReadFrame_RejectsUnsupportedCharset(t *testing.T) {
	t.Parallel()

	raw := "Content-Length: 0\r\nContent-Type: application/aienvs-v1+json; charset=utf-16\r\n\r\n"
	_, err := ReadFrame(strings.NewReader(raw), DefaultMaxFrameBytes)
	if !errors.Is(err, ErrUnsupportedCharset) {
		t.Fatalf("want ErrUnsupportedCharset, got %v", err)
	}
}

func TestReadFrame_RejectsUnsupportedMediaType(t *testing.T) {
	t.Parallel()

	raw := "Content-Length: 0\r\nContent-Type: text/plain\r\n\r\n"
	_, err := ReadFrame(strings.NewReader(raw), DefaultMaxFrameBytes)
	if !errors.Is(err, ErrUnsupportedMediaType) {
		t.Fatalf("want ErrUnsupportedMediaType, got %v", err)
	}
}

func TestReadFrame_RejectsOversizedFrame(t *testing.T) {
	t.Parallel()

	raw := "Content-Length: 999999999\r\n\r\n"
	_, err := ReadFrame(strings.NewReader(raw), 1024)
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("want ErrFrameTooLarge, got %v", err)
	}
}

func TestReadFrame_RejectsHeaderLineWithoutColon(t *testing.T) {
	t.Parallel()

	raw := "ContentLength 5\r\n\r\nhello"
	_, err := ReadFrame(strings.NewReader(raw), DefaultMaxFrameBytes)
	if !errors.Is(err, ErrMalformedHeader) {
		t.Fatalf("want ErrMalformedHeader, got %v", err)
	}
}

func TestReadFrame_PropagatesEOFOnPartialBody(t *testing.T) {
	t.Parallel()

	raw := "Content-Length: 100\r\n\r\nshort"
	_, err := ReadFrame(strings.NewReader(raw), DefaultMaxFrameBytes)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("want io.ErrUnexpectedEOF, got %v", err)
	}
}

func TestReadFrame_PropagatesCleanEOFBeforeAnyHeaders(t *testing.T) {
	t.Parallel()

	_, err := ReadFrame(strings.NewReader(""), DefaultMaxFrameBytes)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("want io.EOF, got %v", err)
	}
}

func TestFrameReader_HandlesMultipleFramesInStream(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	for _, payload := range [][]byte{[]byte("one"), []byte("two"), []byte("three")} {
		if err := WriteFrame(&buf, payload); err != nil {
			t.Fatalf("WriteFrame: %v", err)
		}
	}

	fr := NewFrameReader(&buf)
	for _, want := range []string{"one", "two", "three"} {
		got, err := fr.Read(DefaultMaxFrameBytes)
		if err != nil {
			t.Fatalf("FrameReader.Read %q: %v", want, err)
		}
		if string(got) != want {
			t.Fatalf("payload mismatch: want %q got %q", want, got)
		}
	}

	// Stream is now drained; next read should be clean EOF.
	if _, err := fr.Read(DefaultMaxFrameBytes); !errors.Is(err, io.EOF) {
		t.Fatalf("want io.EOF after drain, got %v", err)
	}
}

func TestReadFrame_ToleratesHeaderWhitespace(t *testing.T) {
	t.Parallel()

	raw := "Content-Length:    7   \r\n\r\npayload"
	got, err := ReadFrame(strings.NewReader(raw), DefaultMaxFrameBytes)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if string(got) != "payload" {
		t.Fatalf("body mismatch: want %q got %q", "payload", got)
	}
}

func TestReadFrame_RejectsOverlongHeaderLine(t *testing.T) {
	// A peer that buffers without sending '\n' must not exhaust memory
	// in the parser. The cap fires well before any frame-size check.
	t.Parallel()

	overlongValue := strings.Repeat("x", 10*1024) // > maxHeaderLineBytes (8 KiB)
	raw := "Content-Length: 0\r\nX-Garbage: " + overlongValue + "\r\n\r\n"
	_, err := ReadFrame(strings.NewReader(raw), DefaultMaxFrameBytes)
	if !errors.Is(err, ErrHeaderLineTooLong) {
		t.Fatalf("want ErrHeaderLineTooLong, got %v", err)
	}
}

func TestReadFrame_RejectsHeaderWithoutNewline(t *testing.T) {
	// A peer that sends a long header line and never terminates it must
	// be rejected before memory is exhausted.
	t.Parallel()

	overlong := strings.Repeat("x", 10*1024)
	_, err := ReadFrame(strings.NewReader(overlong), DefaultMaxFrameBytes)
	if !errors.Is(err, ErrHeaderLineTooLong) {
		t.Fatalf("want ErrHeaderLineTooLong, got %v", err)
	}
}

func TestReadFrame_RejectsTooManyHeaders(t *testing.T) {
	// A peer that floods the header block with short unknown headers
	// must be rejected before the per-line allocations exhaust memory.
	t.Parallel()

	var b strings.Builder
	b.WriteString("Content-Length: 0\r\n")
	for i := 0; i < 100; i++ { // > maxHeaderLines (32)
		fmt.Fprintf(&b, "X-Filler-%d: y\r\n", i)
	}
	b.WriteString("\r\n")
	_, err := ReadFrame(strings.NewReader(b.String()), DefaultMaxFrameBytes)
	if !errors.Is(err, ErrTooManyHeaders) {
		t.Fatalf("want ErrTooManyHeaders, got %v", err)
	}
}

func TestWriteFrame_RejectsZeroWriter(t *testing.T) {
	// Sanity: WriteFrame surfaces writer errors via fmt.Errorf wrap.
	t.Parallel()

	err := WriteFrame(failingWriter{}, []byte("x"))
	if err == nil {
		t.Fatal("want error from failing writer, got nil")
	}
}

type failingWriter struct{}

func (failingWriter) Write(_ []byte) (int, error) { return 0, errors.New("disk on fire") }
