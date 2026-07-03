// Package skillmeta renders the YAML frontmatter block emitted at byte 0
// of every agent-sync-owned SKILL.md (plan U4). It is shared by the
// claude, codex, and pi adapters so the rendered bytes are identical by
// construction: codex and pi co-emit the shared .agents/skills tree
// (ADV-1) and the engine fail-closes on divergent co-emission, so their
// SKILL.md bytes must never drift.
//
// Frontmatter must sit at byte 0 — Claude Code (and Agent
// Skills-convention consumers) parse it only there — so for skill files
// the managed header comment renders below this block, unlike every
// other owned markdown kind, which stays header-first.
package skillmeta

import (
	"fmt"
	"strings"
	"unicode"
)

// Frontmatter renders the SKILL.md frontmatter block:
//
//	---
//	name: <name>
//	description: "<escaped>"
//	---
//
// name is the emitted skill directory name (agent-sync-<id>); it is
// already restricted to [a-z0-9-_] by the IR id rules, so it renders
// bare. description is always rendered as a double-quoted YAML scalar
// via QuoteYAML — never bare — so upstream-authored text cannot corrupt
// the block or smuggle additional frontmatter keys (newline injection).
func Frontmatter(name, description string) []byte {
	var b strings.Builder
	b.WriteString("---\nname: ")
	b.WriteString(name)
	b.WriteString("\ndescription: ")
	b.WriteString(QuoteYAML(description))
	b.WriteString("\n---\n\n")
	return []byte(b.String())
}

// FallbackDescription is the deterministic description emitted when the
// canonical skill authors none. Static provenance text derived only from
// the source identity (never the skill body), so codex/pi byte-identity
// holds and it cannot collide with a future body-derived auto-summary.
// The U5 warning makes the missing description visible; this text keeps
// the emitted skill valid for consumers that require the key.
func FallbackDescription(source string) string {
	if source == "" {
		return "Synced by agent-sync (no description authored)."
	}
	return fmt.Sprintf("Synced by agent-sync from %s (no description authored).", source)
}

// QuoteYAML renders s as a double-quoted single-line YAML scalar.
// Backslash and double-quote are escaped; newlines, carriage returns,
// and tabs use their escape forms; any other control character renders
// as \uXXXX. The output never contains a raw newline, so a hostile
// description cannot terminate the scalar and inject frontmatter keys.
func QuoteYAML(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if unicode.IsControl(r) {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
