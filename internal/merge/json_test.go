package merge

import (
	"bytes"
	"errors"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/agent-sync/agent-sync/pkg/adapterkit"
)

func jsonUpsert(locator string, content string) MergeEntry {
	return MergeEntry{Kind: adapterkit.ToolOwnedKindJSONPointer, Locator: locator, Content: []byte(content)}
}
func jsonRemove(locator string) MergeEntry {
	return MergeEntry{Kind: adapterkit.ToolOwnedKindJSONPointer, Locator: locator, Remove: true}
}

const userMCP = `{
  "mcpServers": {
    "userA": {
      "command": "nodeA",
      "args": ["a.js"]
    },
    "userB": {
      "command": "nodeB"
    }
  }
}
`

func TestMergeJSON_UpsertPreservesUserServers(t *testing.T) {
	t.Parallel()
	out, hash, err := mergeJSON([]byte(userMCP), jsonUpsert("/mcpServers/aienvs_foo", `{"command":"node","args":["server.js"]}`))
	if err != nil {
		t.Fatalf("mergeJSON: %v", err)
	}
	if hash == "" {
		t.Error("upsert should return a non-empty slice hash")
	}
	// agent-sync entry present and correct.
	if gjson.GetBytes(out, "mcpServers.aienvs_foo.command").String() != "node" {
		t.Errorf("aienvs_foo not set correctly: %s", out)
	}
	// User servers byte-identical at the value level.
	for _, k := range []string{"userA", "userB"} {
		got := gjson.GetBytes(out, "mcpServers."+k).Raw
		want := gjson.GetBytes([]byte(userMCP), "mcpServers."+k).Raw
		if got != want {
			t.Errorf("user server %q changed:\n got: %s\nwant: %s", k, got, want)
		}
	}
	// Trailing newline preserved.
	if !bytes.HasSuffix(out, []byte("\n")) {
		t.Error("trailing newline not preserved")
	}
}

func TestMergeJSON_UpsertThenRemoveIsIdentity(t *testing.T) {
	t.Parallel()
	// The strongest no-corruption proof: insert then delete the agent-sync
	// key must restore the original bytes exactly.
	withFoo, _, err := mergeJSON([]byte(userMCP), jsonUpsert("/mcpServers/aienvs_foo", `{"command":"node"}`))
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	back, _, err := mergeJSON(withFoo, jsonRemove("/mcpServers/aienvs_foo"))
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if string(back) != userMCP {
		t.Errorf("upsert+remove not identity:\n got: %q\nwant: %q", back, userMCP)
	}
}

func TestMergeJSON_NoOpUpsertIsByteIdentical(t *testing.T) {
	t.Parallel()
	once, _, err := mergeJSON([]byte(userMCP), jsonUpsert("/mcpServers/aienvs_foo", `{"command":"node"}`))
	if err != nil {
		t.Fatalf("upsert 1: %v", err)
	}
	twice, _, err := mergeJSON(once, jsonUpsert("/mcpServers/aienvs_foo", `{"command":"node"}`))
	if err != nil {
		t.Fatalf("upsert 2: %v", err)
	}
	if !bytes.Equal(once, twice) {
		t.Errorf("re-upserting identical content changed bytes:\n once: %q\ntwice: %q", once, twice)
	}
}

func TestMergeJSON_NewFile(t *testing.T) {
	t.Parallel()
	out, _, err := mergeJSON(nil, jsonUpsert("/mcpServers/aienvs_foo", `{"command":"node"}`))
	if err != nil {
		t.Fatalf("mergeJSON new file: %v", err)
	}
	if gjson.GetBytes(out, "mcpServers.aienvs_foo.command").String() != "node" {
		t.Errorf("new-file merge wrong: %s", out)
	}
}

func TestMergeJSON_MinifiedUserContentPreserved(t *testing.T) {
	t.Parallel()
	min := `{"mcpServers":{"userA":{"command":"nodeA"}}}`
	out, _, err := mergeJSON([]byte(min), jsonUpsert("/mcpServers/aienvs_foo", `{"command":"node"}`))
	if err != nil {
		t.Fatalf("mergeJSON: %v", err)
	}
	if gjson.GetBytes(out, "mcpServers.userA").Raw != `{"command":"nodeA"}` {
		t.Errorf("minified user server not preserved: %s", out)
	}
	// Round-trip identity on a minified file too.
	back, _, err := mergeJSON(out, jsonRemove("/mcpServers/aienvs_foo"))
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if string(back) != min {
		t.Errorf("minified upsert+remove not identity:\n got: %q\nwant: %q", back, min)
	}
}

func TestMergeJSON_EmptyParentKeptAfterRemove(t *testing.T) {
	t.Parallel()
	in := `{"mcpServers":{"aienvs_foo":{"command":"node"}}}`
	out, _, err := mergeJSON([]byte(in), jsonRemove("/mcpServers/aienvs_foo"))
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	// Decision (KTD/U2): keep the empty parent object, do not prune.
	if !gjson.GetBytes(out, "mcpServers").Exists() || !gjson.GetBytes(out, "mcpServers").IsObject() {
		t.Errorf("mcpServers parent should remain an (empty) object; got %s", out)
	}
	if len(gjson.GetBytes(out, "mcpServers").Map()) != 0 {
		t.Errorf("mcpServers should be empty after removing the sole agent-sync entry; got %s", out)
	}
}

func TestMergeJSON_Errors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		base  string
		entry MergeEntry
	}{
		{"invalid json", `{not json`, jsonUpsert("/mcpServers/aienvs_foo", `{"command":"node"}`)},
		{"non-object parent", `{"mcpServers":[1,2,3]}`, jsonUpsert("/mcpServers/aienvs_foo", `{"command":"node"}`)},
		{"invalid content value", `{}`, jsonUpsert("/mcpServers/aienvs_foo", `{not json`)},
		{"duplicate key", `{"mcpServers":{"aienvs_foo":1,"aienvs_foo":2}}`, jsonUpsert("/mcpServers/aienvs_foo", `{"command":"node"}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := mergeJSON([]byte(tc.base), tc.entry)
			if !errors.Is(err, ErrMalformedToolOwnedFile) {
				t.Errorf("err=%v want ErrMalformedToolOwnedFile", err)
			}
		})
	}
}
