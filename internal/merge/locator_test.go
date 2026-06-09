package merge

import (
	"testing"

	"github.com/agent-sync/agent-sync/pkg/adapterkit"
)

func TestEntryID_PerKind(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		kind    adapterkit.ToolOwnedKind
		locator string
		wantID  string
		wantErr bool
	}{
		{"json happy", adapterkit.ToolOwnedKindJSONPointer, "/mcpServers/aienvs_foo", "foo", false},
		{"json underscore id", adapterkit.ToolOwnedKindJSONPointer, "/mcpServers/aienvs_foo_bar", "foo_bar", false},
		{"json no leading slash", adapterkit.ToolOwnedKindJSONPointer, "mcpServers/aienvs_foo", "", true},
		{"json no agent-sync prefix", adapterkit.ToolOwnedKindJSONPointer, "/mcpServers/user_foo", "", true},
		{"toml happy", adapterkit.ToolOwnedKindTOMLPath, "mcp_servers.aienvs_foo", "foo", false},
		{"toml wrong prefix", adapterkit.ToolOwnedKindTOMLPath, "mcp_servers.user_foo", "", true},
		{"markdown happy", adapterkit.ToolOwnedKindMarkdownSection, "aienvs:foo", "foo", false},
		{"markdown no prefix", adapterkit.ToolOwnedKindMarkdownSection, "foo", "", true},
		{"markdown invalid id", adapterkit.ToolOwnedKindMarkdownSection, "aienvs:../escape", "", true},
		{"unknown kind", adapterkit.ToolOwnedKind("bogus"), "x", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := entryID(MergeEntry{Kind: tc.kind, Locator: tc.locator})
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tc.locator)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.wantID {
				t.Errorf("id=%q want %q", got, tc.wantID)
			}
		})
	}
}

func TestMergeEntry_RemoveIsExplicit(t *testing.T) {
	t.Parallel()
	// Remove is a field, independent of Content emptiness — pin that the
	// type carries it explicitly (no inference from empty Content).
	upsert := MergeEntry{Kind: adapterkit.ToolOwnedKindMarkdownSection, Locator: "aienvs:foo", Content: nil, Remove: false}
	del := MergeEntry{Kind: adapterkit.ToolOwnedKindMarkdownSection, Locator: "aienvs:foo", Content: []byte("body"), Remove: true}
	if upsert.Remove {
		t.Error("empty-content upsert must not be a remove")
	}
	if !del.Remove {
		t.Error("Remove must be honored independent of Content")
	}
}
