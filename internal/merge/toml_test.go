package merge

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/agent-sync/agent-sync/pkg/adapterkit"
)

func tomlUpsert(id, body string) MergeEntry {
	return MergeEntry{Kind: adapterkit.ToolOwnedKindTOMLPath, Locator: "mcp_servers.aienvs_" + id, Content: []byte(body)}
}
func tomlRemove(id string) MergeEntry {
	return MergeEntry{Kind: adapterkit.ToolOwnedKindTOMLPath, Locator: "mcp_servers.aienvs_" + id, Remove: true}
}

const userTOML = `# user comment
[general]
name = "x" # inline comment

[mcp_servers.user_one]
command = "node"
`

func TestMergeTOML_UpsertPreservesUserCommentsAndOrder(t *testing.T) {
	t.Parallel()
	out, hash, err := mergeTOML([]byte(userTOML), tomlUpsert("foo", "command = \"node\"\nargs = [\"server.js\"]\n"))
	if err != nil {
		t.Fatalf("mergeTOML: %v", err)
	}
	if hash == "" {
		t.Error("upsert should return a slice hash")
	}
	// User content is byte-identical and comes first (we append).
	if !bytes.HasPrefix(out, []byte(userTOML)) {
		t.Errorf("user content not preserved as a prefix:\n%s", out)
	}
	if !strings.Contains(string(out), "[mcp_servers.aienvs_foo]") {
		t.Errorf("agent-sync table not appended:\n%s", out)
	}
	if !strings.Contains(string(out), "# user comment") || !strings.Contains(string(out), "# inline comment") {
		t.Errorf("user comments dropped:\n%s", out)
	}
}

func TestMergeTOML_UpsertThenRemoveIsIdentity(t *testing.T) {
	t.Parallel()
	withFoo, _, err := mergeTOML([]byte(userTOML), tomlUpsert("foo", "command = \"node\"\n"))
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	back, _, err := mergeTOML(withFoo, tomlRemove("foo"))
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if string(back) != userTOML {
		t.Errorf("upsert+remove not identity:\n got: %q\nwant: %q", back, userTOML)
	}
}

func TestMergeTOML_NoOpUpsertIsByteIdentical(t *testing.T) {
	t.Parallel()
	once, _, err := mergeTOML([]byte(userTOML), tomlUpsert("foo", "command = \"node\"\n"))
	if err != nil {
		t.Fatalf("upsert 1: %v", err)
	}
	twice, _, err := mergeTOML(once, tomlUpsert("foo", "command = \"node\"\n"))
	if err != nil {
		t.Fatalf("upsert 2: %v", err)
	}
	if !bytes.Equal(once, twice) {
		t.Errorf("re-upsert changed bytes:\n once: %q\ntwice: %q", once, twice)
	}
}

// TestMergeTOML_StringAwareLocator is the headline safety test: a user
// table contains a multiline string whose body has a line that looks
// like the agent-sync table header. The locator must NOT treat it as a real
// header (which would splice across the user table and eat bytes).
func TestMergeTOML_StringAwareLocator(t *testing.T) {
	t.Parallel()
	tricky := "[general]\nnote = \"\"\"\n[mcp_servers.aienvs_foo]\nnot a real header\n\"\"\"\n"
	// Upsert a real aienvs_foo table; the in-string header must be left
	// untouched and the real table appended after the user table.
	out, _, err := mergeTOML([]byte(tricky), tomlUpsert("foo", "command = \"node\"\n"))
	if err != nil {
		t.Fatalf("mergeTOML: %v", err)
	}
	if !bytes.HasPrefix(out, []byte(tricky)) {
		t.Errorf("in-string header was mistaken for a table; user bytes changed:\n%s", out)
	}
	// Remove must also not touch the in-string occurrence: round-trip.
	back, _, err := mergeTOML(out, tomlRemove("foo"))
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if string(back) != tricky {
		t.Errorf("string-aware remove not identity:\n got: %q\nwant: %q", back, tricky)
	}
}

func TestMergeTOML_HeaderLineCommentWithTripleQuote(t *testing.T) {
	t.Parallel()
	// A user table header carrying an inline comment containing """ must
	// not flip the scanner into multiline-string state and swallow the
	// following user table.
	in := "[general] # see \"\"\"docs\"\"\"\nname = \"x\"\n\n[mcp_servers.user_one]\ncommand = \"node\"\n"
	out, _, err := mergeTOML([]byte(in), tomlUpsert("foo", "command = \"node\"\n"))
	if err != nil {
		t.Fatalf("mergeTOML: %v", err)
	}
	if !bytes.HasPrefix(out, []byte(in)) {
		t.Errorf("user tables not preserved (comment-triple-quote tripped the scanner):\n%s", out)
	}
	back, _, err := mergeTOML(out, tomlRemove("foo"))
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if string(back) != in {
		t.Errorf("upsert+remove not identity:\n got: %q\nwant: %q", back, in)
	}
}

func TestMergeTOML_Errors(t *testing.T) {
	t.Parallel()
	if _, _, err := mergeTOML([]byte("[broken\nno = "), tomlUpsert("foo", "command = \"node\"\n")); !errors.Is(err, ErrMalformedToolOwnedFile) {
		t.Errorf("invalid TOML: err=%v want ErrMalformedToolOwnedFile", err)
	}
	dup := "[mcp_servers.aienvs_foo]\ncommand = \"a\"\n\n[mcp_servers.aienvs_foo]\ncommand = \"b\"\n"
	if _, _, err := mergeTOML([]byte(dup), tomlUpsert("foo", "command = \"node\"\n")); !errors.Is(err, ErrMalformedToolOwnedFile) {
		// dup TOML may already fail to parse (duplicate table) — either way it must be ErrMalformedToolOwnedFile.
		t.Errorf("duplicate agent-sync table: err=%v want ErrMalformedToolOwnedFile", err)
	}
	if _, _, err := mergeTOML([]byte(""), tomlUpsert("foo", "command = \"\"\"unterminated\n")); !errors.Is(err, ErrMalformedToolOwnedFile) {
		t.Errorf("invalid agent-sync body: err=%v want ErrMalformedToolOwnedFile", err)
	}
}
