package merge

import (
	"strings"
	"testing"
)

// The fuzz targets assert the data-loss invariant's floor: arbitrary
// user content never panics the engine, and a successful upsert always
// produces a result the engine can re-parse (no self-corrupting output).

func FuzzMergeMarkdown(f *testing.F) {
	f.Add("# doc\n\nhello\n")
	f.Add("<!-- aienvs:begin id=foo -->\nold\n<!-- aienvs:end id=foo -->\n")
	f.Add("  <!-- aienvs:begin id=other -->\nuser\n")
	f.Fuzz(func(t *testing.T, existing string) {
		out, _, _, err := mergeMarkdown([]byte(existing), mdUpsert("foo", "BODY\n"))
		if err != nil {
			return // refusal is fine; corruption/panic is not
		}
		// A successful upsert must yield a re-parseable file containing foo.
		if !strings.Contains(string(out), "aienvs:begin id=foo") {
			t.Fatalf("upsert succeeded but foo section absent:\n%s", out)
		}
		if _, _, _, err := mergeMarkdown(out, mdUpsert("foo", "BODY2\n")); err != nil {
			t.Fatalf("engine produced output it cannot re-parse: %v", err)
		}
	})
}

func FuzzMergeJSON(f *testing.F) {
	f.Add(`{"mcpServers":{"u":{"command":"x"}}}`)
	f.Add(`{}`)
	f.Add(`{"mcpServers":[1,2]}`)
	f.Fuzz(func(t *testing.T, existing string) {
		out, _, err := mergeJSON([]byte(existing), jsonUpsert("/mcpServers/aienvs_foo", `{"command":"node"}`))
		if err != nil {
			return
		}
		// Successful merge must be valid JSON the engine can round-trip.
		if _, _, err := mergeJSON(out, jsonRemove("/mcpServers/aienvs_foo")); err != nil {
			t.Fatalf("engine produced JSON it cannot re-merge: %v", err)
		}
	})
}

func FuzzMergeTOML(f *testing.F) {
	f.Add("[general]\nname = \"x\"\n")
	f.Add("note = \"\"\"\n[mcp_servers.aienvs_foo]\n\"\"\"\n")
	f.Add("")
	f.Fuzz(func(t *testing.T, existing string) {
		out, _, err := mergeTOML([]byte(existing), tomlUpsert("foo", "command = \"node\"\n"))
		if err != nil {
			return
		}
		// Successful merge must remain valid, re-mergeable TOML.
		if _, _, err := mergeTOML(out, tomlRemove("foo")); err != nil {
			t.Fatalf("engine produced TOML it cannot re-merge: %v", err)
		}
	})
}
