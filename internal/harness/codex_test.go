package harness

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/agent-sync/agent-sync/internal/merge"
)

func TestCodexNativeOperations_FeatureFlag(t *testing.T) {
	frag := Fragment{ID: "hooks", Target: "codex", Path: ".codex/config.toml", Merge: MergeTOMLKey, Locator: "features.hooks", Payload: []byte("[features]\nhooks = true\n")}
	ops, warnings := NativeOperationsForTarget([]Fragment{frag}, "codex")
	if len(warnings) != 0 {
		t.Fatalf("warnings = %+v", warnings)
	}
	if len(ops) != 1 {
		t.Fatalf("ops = %+v, want 1", ops)
	}
	if ops[0].Path != ".codex/config.toml" || ops[0].Entries[0].Kind != merge.NativeKindTOMLKey {
		t.Fatalf("op = %+v", ops[0])
	}
}

func TestCodexNativeOperations_HooksJSON(t *testing.T) {
	payload := []byte(`{"matcher":"Bash","hooks":[{"type":"command","command":"python3 .codex/hooks/check.py && printf '<done>'","statusMessage":"Checking Bash command"}]}`)
	frag := Fragment{ID: "pre-tool-policy", Target: "codex", Path: ".codex/hooks.json", Merge: MergeCodexHooks, Locator: "PreToolUse/pre-tool-policy", Payload: payload}
	ops, warnings := NativeOperationsForTarget([]Fragment{frag}, "codex")
	if len(warnings) != 0 {
		t.Fatalf("warnings = %+v", warnings)
	}
	if len(ops) != 1 {
		t.Fatalf("ops = %+v, want 1", ops)
	}
	var doc map[string]any
	if err := json.Unmarshal(ops[0].Entries[0].Content, &doc); err != nil {
		t.Fatalf("unmarshal generated hooks: %v", err)
	}
	if _, ok := doc["_agent_sync_generated"]; ok {
		t.Fatalf("generated hooks should not include agent-sync marker: %#v", doc)
	}
	if _, ok := doc["hooks"].(map[string]any)["PreToolUse"]; !ok {
		t.Fatalf("generated hooks missing PreToolUse: %#v", doc)
	}
	text := string(ops[0].Entries[0].Content)
	if !strings.Contains(text, "\n  \"hooks\": {") {
		t.Fatalf("generated hooks should be indented:\n%s", text)
	}
	if !strings.Contains(text, "python3 .codex/hooks/check.py && printf '<done>'") {
		t.Fatalf("generated hooks should not HTML-escape command characters:\n%s", text)
	}
}

func TestCodexNativeOperations_UnsupportedTargetWarns(t *testing.T) {
	frag := Fragment{ID: "hooks", Target: "claude", Path: ".claude/settings.json", Merge: MergeTOMLKey, Locator: "x.y", Payload: []byte("x = 1\n")}
	ops, warnings := NativeOperationsForTarget([]Fragment{frag}, "claude")
	if len(ops) != 0 {
		t.Fatalf("ops = %+v, want none", ops)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %+v, want 1", warnings)
	}
}
