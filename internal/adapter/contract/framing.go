// Package contract owns the on-the-wire types and parsers for the
// aienvs/v1 adapter protocol. Anything that touches bytes flowing between
// the CLI and an adapter belongs here; anything above the wire (process
// management, lifecycle orchestration) lives in higher-level packages.
//
// The protocol is LSP-style Content-Length-framed JSON-RPC 2.0 over
// stdio. The authoritative wire spec ships with PR 3 of Unit 8 at
// docs/spec/adapter-protocol-v1.md; until then this package's tests
// and the IR-v1 plan section are the executable contract.
package contract

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"mime"
	"strconv"
	"strings"
)

// MediaType is the Content-Type value WriteFrame emits and ReadFrame
// accepts. Adapters MUST use UTF-8 JSON; charset overrides are rejected.
const MediaType = "application/aienvs-v1+json"

// DefaultMaxFrameBytes caps a single frame body. Adapters can negotiate
// per-op caps via capabilities, but a transport-level ceiling guards
// against a hostile or buggy peer announcing a multi-gigabyte frame.
//
// Sized at 16 MiB: 2x the default 8 MiB write_file payload cap (decision
// in Unit 8 plan), with headroom for envelope + base64 expansion.
const DefaultMaxFrameBytes = 16 * 1024 * 1024

// maxHeaderLineBytes caps the bytes a single header line may consume
// before the LSP CRLF terminator. Without this, a peer that never sends
// a newline could exhaust memory inside bufio.ReadString long before
// DefaultMaxFrameBytes fires (which only checks the body length, not
// the header block).
//
// 8 KiB is generous: the only headers we care about are Content-Length
// and Content-Type, both of which fit in well under 200 bytes. The cap
// exists as a defense-in-depth measure for hostile peers, not a real
// constraint on cooperative peers.
const maxHeaderLineBytes = 8 * 1024

// maxHeaderLines caps the total number of header lines (including
// unknowns) in a single frame's header block. Without this, a peer
// could send millions of short unknown headers and exhaust memory via
// the per-line allocations in the parsing loop.
const maxHeaderLines = 32

// Wire-level sentinel errors. Callers branch with errors.Is.
var (
	// ErrMissingContentLength is returned when a frame's header block has
	// no Content-Length field. Per LSP base protocol the field is required.
	ErrMissingContentLength = errors.New("contract: frame missing Content-Length header")

	// ErrMalformedHeader is returned when a header line is not of the
	// form "Name: Value" or when Content-Length is non-numeric / negative.
	ErrMalformedHeader = errors.New("contract: malformed frame header")

	// ErrUnsupportedMediaType is returned when Content-Type is present
	// and names a media type other than application/aienvs-v1+json.
	ErrUnsupportedMediaType = errors.New("contract: unsupported Content-Type")

	// ErrUnsupportedCharset is returned when Content-Type's charset
	// parameter is present and is not utf-8.
	ErrUnsupportedCharset = errors.New("contract: unsupported charset, only utf-8 is supported")

	// ErrFrameTooLarge is returned when Content-Length exceeds the
	// caller-supplied maxBytes ceiling.
	ErrFrameTooLarge = errors.New("contract: frame exceeds maximum allowed size")

	// ErrHeaderLineTooLong is returned when a header line exceeds
	// maxHeaderLineBytes before its CRLF terminator is seen. Defends
	// against memory exhaustion from a peer that never sends '\n'.
	ErrHeaderLineTooLong = errors.New("contract: frame header line exceeds maximum length")

	// ErrTooManyHeaders is returned when a frame's header block contains
	// more lines than maxHeaderLines. Defends against memory exhaustion
	// from a peer that floods the header block with short unknown lines.
	ErrTooManyHeaders = errors.New("contract: frame contains too many header lines")
)

// WriteFrame emits one LSP-framed message: a header block followed by
// CRLFCRLF and the raw payload bytes. The caller owns serialization;
// payload is treated as opaque bytes (UTF-8 JSON in practice).
//
// The header block is fixed-shape so a peer can decode it without a full
// MIME parser:
//
//	Content-Length: <n>\r\n
//	Content-Type: application/aienvs-v1+json; charset=utf-8\r\n
//	\r\n
//	<n bytes of payload>
func WriteFrame(w io.Writer, payload []byte) error {
	header := fmt.Sprintf(
		"Content-Length: %d\r\nContent-Type: %s; charset=utf-8\r\n\r\n",
		len(payload), MediaType,
	)
	if _, err := io.WriteString(w, header); err != nil {
		return fmt.Errorf("contract: write frame header: %w", err)
	}
	if _, err := w.Write(payload); err != nil {
		return fmt.Errorf("contract: write frame body: %w", err)
	}
	return nil
}

// ReadFrame is a one-shot convenience for callers that only need a
// single frame (e.g., tests, the initialize handshake before a long-lived
// FrameReader is wired up). Long-lived stdio loops should use
// NewFrameReader to avoid losing buffered bytes between calls.
func ReadFrame(r io.Reader, maxBytes int64) ([]byte, error) {
	return NewFrameReader(r).Read(maxBytes)
}

// FrameReader consumes one or more LSP-framed messages from a single
// underlying reader. The buffered reader is held across calls so multiple
// frames can be read from the same stream — the typical case for an
// adapter connection that carries many requests.
type FrameReader struct {
	br *bufio.Reader
}

// NewFrameReader wraps r. If r is already a *bufio.Reader, it is reused
// so callers can layer FrameReader on top of an existing buffered stream
// without double-buffering.
func NewFrameReader(r io.Reader) *FrameReader {
	if br, ok := r.(*bufio.Reader); ok {
		return &FrameReader{br: br}
	}
	return &FrameReader{br: bufio.NewReader(r)}
}

// Read consumes one frame. The header block is terminated by CRLFCRLF;
// Content-Length names exactly the number of payload bytes that follow.
//
// maxBytes is a defense-in-depth ceiling on the declared Content-Length;
// a peer announcing a frame larger than maxBytes is rejected before any
// body bytes are read.
//
// On clean EOF before the first byte of any header, Read returns io.EOF
// unwrapped so callers can detect end-of-stream. A truncated body is
// reported as io.ErrUnexpectedEOF.
func (fr *FrameReader) Read(maxBytes int64) ([]byte, error) {
	br := fr.br

	if _, err := br.Peek(1); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, io.EOF
		}
		return nil, fmt.Errorf("contract: peek frame: %w", err)
	}

	contentLength := int64(-1)
	headerCount := 0

	for {
		if headerCount >= maxHeaderLines {
			return nil, fmt.Errorf("%w: %d", ErrTooManyHeaders, maxHeaderLines)
		}
		line, err := readBoundedLine(br, maxHeaderLineBytes)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil, fmt.Errorf("contract: truncated header: %w", io.ErrUnexpectedEOF)
			}
			return nil, err
		}
		// Headers must end CRLF; tolerate bare LF for lenient peers but
		// require at least the trailing newline.
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break // end of header block
		}
		headerCount++

		colon := strings.IndexByte(line, ':')
		if colon <= 0 {
			return nil, fmt.Errorf("%w: %q", ErrMalformedHeader, line)
		}
		name := strings.ToLower(strings.TrimSpace(line[:colon]))
		value := strings.TrimSpace(line[colon+1:])

		switch name {
		case "content-length":
			n, parseErr := strconv.ParseInt(value, 10, 64)
			if parseErr != nil || n < 0 {
				return nil, fmt.Errorf("%w: Content-Length=%q", ErrMalformedHeader, value)
			}
			contentLength = n
		case "content-type":
			mediaType, params, parseErr := mime.ParseMediaType(value)
			if parseErr != nil {
				return nil, fmt.Errorf("%w: Content-Type=%q", ErrMalformedHeader, value)
			}
			if !strings.EqualFold(mediaType, MediaType) {
				return nil, fmt.Errorf("%w: %q", ErrUnsupportedMediaType, mediaType)
			}
			if charset, ok := params["charset"]; ok && !strings.EqualFold(charset, "utf-8") {
				return nil, fmt.Errorf("%w: charset=%q", ErrUnsupportedCharset, charset)
			}
		default:
			// Unknown headers are ignored, matching LSP base-protocol
			// laxity. Future headers can be introduced additively.
		}
	}

	if contentLength < 0 {
		return nil, ErrMissingContentLength
	}
	if contentLength > maxBytes {
		return nil, fmt.Errorf("%w: declared %d > max %d", ErrFrameTooLarge, contentLength, maxBytes)
	}

	body := make([]byte, contentLength)
	if _, err := io.ReadFull(br, body); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, fmt.Errorf("contract: truncated body: %w", io.ErrUnexpectedEOF)
		}
		return nil, fmt.Errorf("contract: read body: %w", err)
	}
	return body, nil
}

// readBoundedLine reads up to maxBytes bytes from br, returning the
// line including its trailing '\n'. Returns ErrHeaderLineTooLong when
// no '\n' is seen within the cap — defends against memory exhaustion
// by a peer that buffers without ever terminating a line.
//
// Returns io.EOF when nothing has been read; io.ErrUnexpectedEOF when
// the reader closed mid-line.
func readBoundedLine(br *bufio.Reader, maxBytes int) (string, error) {
	buf := make([]byte, 0, 128)
	for i := 0; i <= maxBytes; i++ {
		b, err := br.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) {
				if len(buf) == 0 {
					return "", io.EOF
				}
				return "", io.ErrUnexpectedEOF
			}
			return "", fmt.Errorf("contract: read header line: %w", err)
		}
		buf = append(buf, b)
		if b == '\n' {
			return string(buf), nil
		}
	}
	return "", fmt.Errorf("%w: %d bytes without terminator", ErrHeaderLineTooLong, maxBytes)
}
