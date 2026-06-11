package merge

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// mergeTOML upserts or removes the agent-sync table at the entry's
// toml-path locator inside existing.
//
// go-toml/v2's decode→encode does NOT preserve user comments or
// formatting (verified by spike: it drops `#` comments and rewrites
// quoting), so a full re-encode would destroy user content. Instead
// this is a STRING-AWARE LINE-SPLICE: the user's bytes are never
// re-rendered; only the agent-sync table's line span is replaced/inserted/
// removed. The span locator tracks TOML multiline-string state so a
// header-shaped line inside a user's """...""" / ”'...”' string is
// never mistaken for a table header.
//
// Content is the raw TOML table body (key = value lines, no header);
// the engine renders the `[mcp_servers.agentsync_<id>]` header + a managed
// comment + Content, and validates the whole renders as parseable TOML
// before splicing.
func mergeTOML(existing []byte, e MergeEntry) (result []byte, sliceHash string, err error) {
	id, err := entryID(e)
	if err != nil {
		return nil, "", err
	}
	// A blank (whitespace-only) file has no user content; start fresh
	// from empty so we don't preserve junk like a bare \r (invalid in
	// TOML) and then produce un-reparseable output. A non-blank file
	// must parse strictly — we never rewrite a file we cannot parse.
	work := existing
	if isBlank(existing) {
		work = nil
	} else {
		var probe map[string]any
		if uerr := toml.Unmarshal(existing, &probe); uerr != nil {
			return nil, "", fmt.Errorf("%w: invalid TOML: %s", ErrMalformedToolOwnedFile, uerr.Error())
		}
	}

	header := "[mcp_servers." + agentsyncKeyPrefix + id + "]"
	spans := tomlTableSpans(work)

	// Reject a pre-existing duplicate agent-sync table.
	matches := 0
	for _, s := range spans {
		if s.header == header {
			matches++
		}
	}
	if matches > 1 {
		return nil, "", fmt.Errorf("%w: duplicate table %s", ErrMalformedToolOwnedFile, header)
	}

	lines := splitLinesKeepEnding(work)

	if e.Remove {
		for _, s := range spans {
			if s.header == header {
				lines = append(lines[:s.start], lines[s.end:]...)
				break
			}
		}
		return joinLines(lines), "", nil
	}

	// Upsert: render and validate the agent-sync table.
	rendered := renderAgentsyncTable(header, e.Content)
	if verr := validateTOMLFragment(rendered); verr != nil {
		return nil, "", fmt.Errorf("%w: agent-sync table body is not valid TOML: %s", ErrMalformedToolOwnedFile, verr.Error())
	}
	renderedLines := splitLinesKeepEnding([]byte(rendered))

	replaced := false
	for _, s := range spans {
		if s.header == header {
			tail := append([]string{}, lines[s.end:]...)
			lines = append(lines[:s.start], renderedLines...)
			lines = append(lines, tail...)
			replaced = true
			break
		}
	}
	if !replaced {
		// Append at end. Ensure the preceding content ends with a
		// newline so the header starts its own line, but add no extra
		// blank line — that keeps remove (which drops exactly the
		// header→EOF span) a clean inverse of append.
		if n := len(lines); n > 0 && !strings.HasSuffix(lines[n-1], "\n") {
			lines[n-1] += "\n"
		}
		lines = append(lines, renderedLines...)
	}

	sum := sha256.Sum256([]byte(rendered))
	return joinLines(lines), hex.EncodeToString(sum[:]), nil
}

type tomlSpan struct {
	header     string // trimmed header line, e.g. "[mcp_servers.agentsync_foo]"
	start, end int    // [start,end) line indices, start = header line
}

// tomlTableSpans returns the line span of every top-level table header,
// skipping headers that appear inside a multiline string. A table's
// span runs from its header line to the line before the next table
// header (or EOF).
func tomlTableSpans(data []byte) []tomlSpan {
	lines := splitLinesKeepEnding(data)
	var spans []tomlSpan
	inString := false
	var delim string
	for i, raw := range lines {
		line := strings.TrimRight(raw, "\r\n")
		if inString {
			if strings.Contains(line, delim) {
				inString = false
			}
			continue
		}
		trimmed := strings.TrimSpace(line)
		// A header line (outside a string) starts a new table. A header
		// line cannot legitimately open a multiline string, so once a
		// line is classified as a header we do NOT evaluate it for an
		// open delimiter — that avoids a `[table] # """` inline comment
		// being mistaken for a multiline-string opener (which would
		// wrongly swallow the following tables into "in-string" state).
		if strings.HasPrefix(trimmed, "[") {
			if n := len(spans); n > 0 {
				spans[n-1].end = i
			}
			spans = append(spans, tomlSpan{header: trimmed, start: i, end: len(lines)})
			continue
		}
		// Detect entering a multiline string (odd count of a triple
		// delimiter on this line, not closed on the same line).
		if d := opensMultiline(line); d != "" {
			inString = true
			delim = d
		}
	}
	return spans
}

// opensMultiline reports the triple-quote delimiter the line leaves
// open (or "" if none). A delimiter whose count on the line is odd
// leaves a multiline string open.
func opensMultiline(line string) string {
	for _, d := range []string{`"""`, `'''`} {
		if strings.Count(line, d)%2 == 1 {
			return d
		}
	}
	return ""
}

func renderAgentsyncTable(header string, body []byte) string {
	var b strings.Builder
	b.WriteString(header)
	b.WriteString("\n")
	b.WriteString("# Managed by agent-sync — do not edit. Regenerate: agent-sync sync\n")
	bs := string(body)
	b.WriteString(bs)
	if !strings.HasSuffix(bs, "\n") {
		b.WriteString("\n")
	}
	return b.String()
}

func validateTOMLFragment(fragment string) error {
	var m map[string]any
	return toml.Unmarshal([]byte(fragment), &m)
}

// splitLinesKeepEnding splits into lines preserving each line's newline
// (so joining is lossless). A final line without a newline is kept as-is.
func splitLinesKeepEnding(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	var lines []string
	s := string(data)
	for len(s) > 0 {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			lines = append(lines, s)
			break
		}
		lines = append(lines, s[:i+1])
		s = s[i+1:]
	}
	return lines
}

func joinLines(lines []string) []byte {
	return []byte(strings.Join(lines, ""))
}
