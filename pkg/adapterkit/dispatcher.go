package adapterkit

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"strconv"
	"strings"
)

const (
	mediaType          = "application/aienvs-v1+json"
	maxHeaderLineBytes = 8 * 1024
	maxHeaderLines     = 32
)

type inboundKind uint8

const (
	inboundKindUnknown inboundKind = iota
	inboundKindRequest
	inboundKindNotification
)

type inboundMessage struct {
	kind   inboundKind
	id     json.RawMessage
	method string
	params json.RawMessage
}

func (s *Server) serve(ctx context.Context) error {
	reader := newFrameReader(s.stdin)
	for {
		payload, err := reader.Read(DefaultMaxFrameBytes)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		msg, err := parseInboundMessage(payload)
		if err != nil {
			if writeErr := writeErrorResponse(s.stdout, nil, &Error{Code: CodeInvalidRequest, Message: err.Error()}); writeErr != nil {
				return writeErr
			}
			continue
		}

		switch msg.kind {
		case inboundKindNotification:
			if msg.method == MethodInitialized {
				if rpcErr := s.handleInitialized(); rpcErr != nil {
					_, _ = fmt.Fprintf(s.stderr, "adapterkit: initialized handler error: %s\n", rpcErr.Error())
					return rpcErr
				}
				continue
			}
			continue
		case inboundKindRequest:
			if err := s.dispatchRequest(ctx, msg); err != nil {
				return err
			}
		default:
			return fmt.Errorf("adapterkit: unexpected inbound message kind %d", msg.kind)
		}
	}
}

func (s *Server) dispatchRequest(ctx context.Context, msg inboundMessage) error {
	switch msg.method {
	case MethodInitialize:
		handler := s.initializeHandler()
		var params InitializeParams
		if err := decodeParams(msg.params, &params); err != nil {
			return writeErrorResponse(s.stdout, msg.id, &Error{Code: CodeInvalidParams, Message: err.Error()})
		}
		result, rpcErr := s.handleInitialize(ctx, params, handler)
		if rpcErr != nil {
			return writeErrorResponse(s.stdout, msg.id, rpcErr)
		}
		return writeInitializeResponse(s.stdout, msg.id, result, s.cookie)
	case MethodEmit:
		handler := s.emitHandler()
		var params EmitParams
		if err := decodeParams(msg.params, &params); err != nil {
			return writeErrorResponse(s.stdout, msg.id, &Error{Code: CodeInvalidParams, Message: err.Error()})
		}
		result, rpcErr := s.handleEmit(ctx, params, handler)
		if rpcErr != nil {
			return writeErrorResponse(s.stdout, msg.id, rpcErr)
		}
		return writeResultResponse(s.stdout, msg.id, result)
	case MethodShutdown:
		handler := s.shutdownHandler()
		if rpcErr := s.handleShutdown(ctx, handler); rpcErr != nil {
			return writeErrorResponse(s.stdout, msg.id, rpcErr)
		}
		if err := writeResultResponse(s.stdout, msg.id, ShutdownResult{}); err != nil {
			return err
		}
		s.setProtocolShutdownAcked()
		return nil
	default:
		return writeErrorResponse(s.stdout, msg.id, &Error{
			Code:    CodeMethodNotFound,
			Message: "unknown method: " + msg.method,
		})
	}
}

func decodeParams(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("adapterkit: decode params: %w", err)
	}
	return nil
}

func parseInboundMessage(raw []byte) (inboundMessage, error) {
	var fields map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&fields); err != nil {
		return inboundMessage{}, fmt.Errorf("adapterkit: parse envelope: %w", err)
	}
	// One frame must carry exactly one JSON value. Issue a second Decode
	// against the same stream and require io.EOF — anything else (a
	// successful decode of a concatenated value, or a parse error on
	// trailing garbage) is a smuggled or malformed frame. This matches
	// contract.ParseMessage so adapterkit and the runtime agree on what
	// is a valid envelope.
	var trailing json.RawMessage
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		return inboundMessage{}, errors.New("adapterkit: trailing bytes after envelope")
	}

	versionRaw, ok := fields["jsonrpc"]
	if !ok {
		return inboundMessage{}, errors.New("adapterkit: jsonrpc field missing")
	}
	var version string
	if err := json.Unmarshal(versionRaw, &version); err != nil {
		return inboundMessage{}, fmt.Errorf("adapterkit: decode jsonrpc version: %w", err)
	}
	if version != JSONRPCVersion {
		return inboundMessage{}, fmt.Errorf("adapterkit: unsupported jsonrpc version %q", version)
	}

	methodRaw, ok := fields["method"]
	if !ok {
		return inboundMessage{}, errors.New("adapterkit: method field missing")
	}
	var method string
	if err := json.Unmarshal(methodRaw, &method); err != nil {
		return inboundMessage{}, fmt.Errorf("adapterkit: decode method: %w", err)
	}
	if method == "" {
		return inboundMessage{}, errors.New("adapterkit: method must be non-empty")
	}
	if _, ok := fields["result"]; ok {
		return inboundMessage{}, errors.New("adapterkit: client envelope must not contain result")
	}
	if _, ok := fields["error"]; ok {
		return inboundMessage{}, errors.New("adapterkit: client envelope must not contain error")
	}

	msg := inboundMessage{method: method, params: fields["params"]}
	if idRaw, ok := fields["id"]; ok {
		msg.kind = inboundKindRequest
		msg.id = idRaw
	} else {
		msg.kind = inboundKindNotification
	}
	return msg, nil
}

func writeInitializeResponse(w io.Writer, id json.RawMessage, result InitializeResult, cookie string) error {
	result.Cookie = cookie
	return writeResponse(w, id, result, nil)
}

func writeResultResponse(w io.Writer, id json.RawMessage, result any) error {
	return writeResponse(w, id, result, nil)
}

func writeErrorResponse(w io.Writer, id json.RawMessage, rpcErr *Error) error {
	return writeResponse(w, id, nil, rpcErr)
}

func writeResponse(w io.Writer, id json.RawMessage, result any, rpcErr *Error) error {
	type envelope struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  json.RawMessage `json:"result,omitempty"`
		Error   *Error          `json:"error,omitempty"`
	}

	resp := envelope{
		JSONRPC: JSONRPCVersion,
		ID:      id,
		Error:   rpcErr,
	}
	if result != nil {
		payload, err := json.Marshal(result)
		if err != nil {
			return fmt.Errorf("adapterkit: marshal response result: %w", err)
		}
		resp.Result = payload
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("adapterkit: marshal response envelope: %w", err)
	}
	return writeFrame(w, data)
}

func writeFrame(w io.Writer, payload []byte) error {
	// Build the entire frame (header + CRLFCRLF + body) into a single
	// buffer and emit one Write call. This avoids per-frame syscall
	// overhead on the hot path and makes the frame an atomic write —
	// concurrent writers sharing the underlying io.Writer (a shared
	// stdout, in practice) cannot interleave header and body bytes.
	var buf bytes.Buffer
	buf.Grow(64 + len(payload))
	fmt.Fprintf(&buf,
		"Content-Length: %d\r\nContent-Type: %s; charset=utf-8\r\n\r\n",
		len(payload), mediaType,
	)
	buf.Write(payload)
	if _, err := w.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("adapterkit: write frame: %w", err)
	}
	return nil
}

type frameReader struct {
	br *bufio.Reader
}

func newFrameReader(r io.Reader) *frameReader {
	if br, ok := r.(*bufio.Reader); ok {
		return &frameReader{br: br}
	}
	return &frameReader{br: bufio.NewReader(r)}
}

func (fr *frameReader) Read(maxBytes int64) ([]byte, error) {
	if _, err := fr.br.Peek(1); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, io.EOF
		}
		return nil, fmt.Errorf("adapterkit: peek frame: %w", err)
	}

	contentLength := int64(-1)
	headerCount := 0
	for {
		if headerCount >= maxHeaderLines {
			return nil, errors.New("adapterkit: frame contains too many headers")
		}
		line, err := readBoundedLine(fr.br, maxHeaderLineBytes)
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		headerCount++

		colon := strings.IndexByte(line, ':')
		if colon <= 0 {
			return nil, errors.New("adapterkit: malformed frame header")
		}
		name := strings.ToLower(strings.TrimSpace(line[:colon]))
		value := strings.TrimSpace(line[colon+1:])
		switch name {
		case "content-length":
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil || n < 0 {
				return nil, errors.New("adapterkit: malformed Content-Length header")
			}
			contentLength = n
		case "content-type":
			media, params, err := mime.ParseMediaType(value)
			if err != nil {
				return nil, errors.New("adapterkit: malformed Content-Type header")
			}
			if !strings.EqualFold(media, mediaType) {
				return nil, fmt.Errorf("adapterkit: unsupported Content-Type %q", media)
			}
			if charset, ok := params["charset"]; ok && !strings.EqualFold(charset, "utf-8") {
				return nil, fmt.Errorf("adapterkit: unsupported charset %q", charset)
			}
		}
	}

	if contentLength < 0 {
		return nil, errors.New("adapterkit: frame missing Content-Length header")
	}
	if contentLength > maxBytes {
		return nil, fmt.Errorf("adapterkit: frame exceeds max size (%d > %d)", contentLength, maxBytes)
	}

	body := make([]byte, contentLength)
	if _, err := io.ReadFull(fr.br, body); err != nil {
		return nil, fmt.Errorf("adapterkit: read frame body: %w", err)
	}
	return body, nil
}

func readBoundedLine(br *bufio.Reader, maxBytes int) (string, error) {
	// ReadSlice scans the buffered reader's internal slab in one shot,
	// avoiding the per-byte ReadByte overhead of the previous
	// implementation. ReadSlice can return bufio.ErrBufferFull when '\n'
	// is not seen within the reader's buffer; in that case we copy out
	// the partial chunk and resume scanning. The maxBytes cap stops a
	// peer that buffers without ever sending '\n'. Mirrors the same
	// implementation in internal/adapter/contract/framing.go.
	var buf []byte
	for {
		chunk, err := br.ReadSlice('\n')

		if err == nil {
			// Hot path: complete line in one ReadSlice call. Cap allows
			// maxBytes content + 1 terminator byte to match the original
			// byte-by-byte loop's iteration budget.
			if len(buf)+len(chunk) > maxBytes+1 {
				return "", errors.New("adapterkit: frame header line exceeds maximum length")
			}
			if len(buf) == 0 {
				return string(chunk), nil
			}
			buf = append(buf, chunk...)
			return string(buf), nil
		}

		// On ErrBufferFull or EOF, chunk contains zero '\n'. If we've
		// accumulated more than maxBytes bytes without a terminator,
		// the line is too long.
		accumulated := len(buf) + len(chunk)

		if errors.Is(err, bufio.ErrBufferFull) {
			if accumulated > maxBytes {
				return "", errors.New("adapterkit: frame header line exceeds maximum length")
			}
			buf = append(buf, chunk...)
			continue
		}

		if errors.Is(err, io.EOF) {
			if accumulated == 0 {
				return "", io.EOF
			}
			if accumulated > maxBytes {
				return "", errors.New("adapterkit: frame header line exceeds maximum length")
			}
			return "", fmt.Errorf("truncated header: %w", io.ErrUnexpectedEOF)
		}

		return "", err
	}
}
