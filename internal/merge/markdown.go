package merge

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// markerOpen is the literal opener every aienvs marker shares. A body
// containing it means a caller passed pre-wrapped content (the engine
// owns markers) — rejected loudly so a Unit 13 double-wrap mistake
// fails in tests, not in production.
const markerOpen = "<!-- aienvs:"

// mergeMarkdown upserts or removes the aienvs managed section for the
// entry's id. It owns the begin/end markers (KTD 5): the caller passes
// the INNER body. Recovery is refuse-don't-guess — any malformed marker
// state returns ErrMalformedManagedSection naming the line, with no
// write. Returns the merged bytes, the slice hash, and an optional
// warning (set when an indented marker for a non-target id is treated
// as user content).
func mergeMarkdown(existing []byte, e MergeEntry) (result []byte, sliceHash, warning string, err error) {
	id, err := entryID(e)
	if err != nil {
		return nil, "", "", err
	}
	if !e.Remove && strings.Contains(string(e.Content), markerOpen) {
		return nil, "", "", fmt.Errorf("merge: markdown body contains aienvs marker %q (the engine owns markers; pass inner body only)", markerOpen)
	}

	nl := detectNewline(existing)
	lines := splitLinesKeepEnding(existing)

	pair, warn, perr := scanMarkers(lines, id)
	if perr != nil {
		return nil, "", "", perr
	}

	if e.Remove {
		if pair.found {
			lines = append(lines[:pair.begin], lines[pair.end+1:]...)
		}
		return joinLines(lines), "", warn, nil
	}

	block := renderManagedBlock(id, e.Source, e.Content, nl)
	blockLines := splitLinesKeepEnding([]byte(block))
	sum := sha256.Sum256([]byte(block))
	sliceHash = hex.EncodeToString(sum[:])

	if pair.found {
		tail := append([]string{}, lines[pair.end+1:]...)
		lines = append(lines[:pair.begin], blockLines...)
		lines = append(lines, tail...)
		return joinLines(lines), sliceHash, warn, nil
	}

	// Append a new section at EOF (with a top-of-file header for a new file).
	if len(strings.TrimSpace(string(existing))) == 0 {
		header := "<!-- Partially managed by aienvs — edit outside the aienvs:begin / aienvs:end markers only. -->" + nl + nl
		return []byte(header + block), sliceHash, warn, nil
	}
	if n := len(lines); n > 0 && !strings.HasSuffix(lines[n-1], "\n") {
		lines[n-1] += nl
	}
	lines = append(lines, nl) // one blank separator before the appended section
	lines = append(lines, blockLines...)
	return joinLines(lines), sliceHash, warn, nil
}

type markerPair struct {
	found      bool
	begin, end int // inclusive line indices of the begin/end marker lines
}

// scanMarkers walks the lines applying the recovery state machine for
// the target id. It returns the matched pair (if any), a warning, or an
// ErrMalformedManagedSection on any bad marker state.
func scanMarkers(lines []string, targetID string) (markerPair, string, error) {
	var pair markerPair
	openID := ""
	openLine := -1
	seen := map[string]bool{}
	warning := ""

	for i, raw := range lines {
		core := strings.TrimRight(raw, "\r\n")
		trimmed := strings.TrimLeft(core, " \t")
		indented := trimmed != core

		bid, _, isBegin := parseBeginMarker(trimmed)
		eid, isEnd := parseEndMarker(trimmed)
		if !isBegin && !isEnd {
			continue
		}

		if indented {
			// An indented marker is user content, EXCEPT an indented copy
			// of the id we manage — that signals a re-indented managed
			// section and silently appending a duplicate would grow stale
			// copies, so refuse.
			mid := bid
			if isEnd {
				mid = eid
			}
			if mid == targetID {
				return markerPair{}, "", fmt.Errorf("%w: indented marker for managed id %q at line %d", ErrMalformedManagedSection, targetID, i+1)
			}
			warning = fmt.Sprintf("indented aienvs marker at line %d treated as user content", i+1)
			continue
		}

		switch {
		case isBegin:
			if openLine >= 0 {
				return markerPair{}, "", fmt.Errorf("%w: nested begin (id %q) at line %d before end of id %q", ErrMalformedManagedSection, bid, i+1, openID)
			}
			if seen[bid] {
				return markerPair{}, "", fmt.Errorf("%w: duplicate begin id %q at line %d", ErrMalformedManagedSection, bid, i+1)
			}
			openID, openLine = bid, i
		case isEnd:
			if openLine < 0 {
				return markerPair{}, "", fmt.Errorf("%w: end id %q at line %d without a matching begin", ErrMalformedManagedSection, eid, i+1)
			}
			if eid != openID {
				return markerPair{}, "", fmt.Errorf("%w: end id %q at line %d does not match open begin id %q", ErrMalformedManagedSection, eid, i+1, openID)
			}
			if openID == targetID {
				pair = markerPair{found: true, begin: openLine, end: i}
			}
			seen[openID] = true
			openID, openLine = "", -1
		}
	}
	if openLine >= 0 {
		return markerPair{}, "", fmt.Errorf("%w: begin id %q is never closed", ErrMalformedManagedSection, openID)
	}
	return pair, warning, nil
}

// parseBeginMarker parses `<!-- aienvs:begin id=ID -->` or
// `<!-- aienvs:begin id=ID source=SRC -->`. Returns id, source, ok.
func parseBeginMarker(core string) (id, source string, ok bool) {
	inner, ok := cutMarker(core, "begin")
	if !ok {
		return "", "", false
	}
	rest, ok := strings.CutPrefix(inner, "id=")
	if !ok {
		return "", "", false
	}
	if i := strings.Index(rest, " source="); i >= 0 {
		return rest[:i], rest[i+len(" source="):], true
	}
	if strings.ContainsAny(rest, " ") {
		return "", "", false // unexpected trailing tokens
	}
	return rest, "", true
}

// parseEndMarker parses `<!-- aienvs:end id=ID -->`.
func parseEndMarker(core string) (id string, ok bool) {
	inner, ok := cutMarker(core, "end")
	if !ok {
		return "", false
	}
	rest, ok := strings.CutPrefix(inner, "id=")
	if !ok || strings.ContainsAny(rest, " ") {
		return "", false
	}
	return rest, true
}

// cutMarker strips `<!-- aienvs:<verb> ` ... ` -->` and returns the inner.
func cutMarker(core, verb string) (string, bool) {
	prefix := markerOpen + verb + " "
	inner, ok := strings.CutPrefix(core, prefix)
	if !ok {
		return "", false
	}
	return strings.CutSuffix(inner, " -->")
}

func renderManagedBlock(id, source string, body []byte, nl string) string {
	var b strings.Builder
	b.WriteString(markerOpen + "begin id=" + id)
	if source != "" {
		b.WriteString(" source=" + source)
	}
	b.WriteString(" -->")
	b.WriteString(nl)
	bs := string(body)
	b.WriteString(bs)
	if len(bs) == 0 || !strings.HasSuffix(bs, "\n") {
		b.WriteString(nl)
	}
	b.WriteString(markerOpen + "end id=" + id + " -->")
	b.WriteString(nl)
	return b.String()
}

// detectNewline returns the dominant newline style of data, defaulting
// to "\n" for empty input or a tie.
func detectNewline(data []byte) string {
	crlf := strings.Count(string(data), "\r\n")
	lf := strings.Count(string(data), "\n") - crlf
	if crlf > lf {
		return "\r\n"
	}
	return "\n"
}
