package merge

import (
	"fmt"
	"strings"

	"github.com/aienvs/aienvs/internal/ir"
	"github.com/aienvs/aienvs/pkg/adapterkit"
)

// aienvsKeyPrefix is the key/table-name prefix that marks an entry as
// aienvs-owned. The id follows it (e.g. aienvs_foo -> id "foo").
const aienvsKeyPrefix = "aienvs_"

// MergeEntry is one merge operation against a tool-owned file. Op is
// explicit via Remove (NOT inferred from empty Content): for markdown a
// legitimately-empty managed section is byte-distinct from a removal.
type MergeEntry struct {
	Kind    adapterkit.ToolOwnedKind // json-pointer | toml-path | markdown-section
	Locator string                   // /mcpServers/aienvs_<id> | mcp_servers.aienvs_<id> | aienvs:<id>
	Content []byte                   // inner body / JSON value / TOML table body (upsert only)
	Source  string                   // optional marker provenance (markdown only)
	Remove  bool                     // true = delete the entry/table/section
}

// entryID extracts and validates the aienvs id from the entry's
// locator, per kind. Extraction is by exact `aienvs_` / `aienvs:`
// prefix strip — never by splitting on the last separator — because
// ids may contain underscores and hyphens (aienvs_foo_bar -> foo_bar).
func entryID(e MergeEntry) (string, error) {
	switch e.Kind {
	case adapterkit.ToolOwnedKindJSONPointer:
		if !strings.HasPrefix(e.Locator, "/") {
			return "", fmt.Errorf("merge: json-pointer locator must start with '/': %q", e.Locator)
		}
		seg := e.Locator[strings.LastIndex(e.Locator, "/")+1:]
		return validateAienvsSeg(seg)
	case adapterkit.ToolOwnedKindTOMLPath:
		seg := e.Locator[strings.LastIndex(e.Locator, ".")+1:]
		return validateAienvsSeg(seg)
	case adapterkit.ToolOwnedKindMarkdownSection:
		id, ok := strings.CutPrefix(e.Locator, "aienvs:")
		if !ok {
			return "", fmt.Errorf("merge: markdown locator must be aienvs:<id>: %q", e.Locator)
		}
		if !ir.IsValidID(id) {
			return "", fmt.Errorf("merge: invalid id %q", id)
		}
		return id, nil
	default:
		return "", fmt.Errorf("merge: unknown locator kind %q", e.Kind)
	}
}

// isBlank reports whether data is empty or contains only ASCII
// whitespace (space, tab, newline, CR). Used to decide "new/empty
// file" — deliberately NOT strings.TrimSpace, which also strips
// control chars like form-feed (\f) and vertical-tab (\v); a file
// containing only such junk is NOT empty and must fail to parse rather
// than be silently appended to (a real fuzz finding).
func isBlank(data []byte) bool {
	for _, b := range data {
		switch b {
		case ' ', '\t', '\n', '\r':
		default:
			return false
		}
	}
	return true
}

// validateAienvsSeg strips the aienvs_ prefix from a key/table segment
// and validates the remaining id against the IR id grammar.
func validateAienvsSeg(seg string) (string, error) {
	id, ok := strings.CutPrefix(seg, aienvsKeyPrefix)
	if !ok {
		return "", fmt.Errorf("merge: segment must be %s<id>: %q", aienvsKeyPrefix, seg)
	}
	if !ir.IsValidID(id) {
		return "", fmt.Errorf("merge: invalid id %q", id)
	}
	return id, nil
}
