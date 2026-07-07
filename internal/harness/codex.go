package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/agent-sync/agent-sync/internal/merge"
)

func NativeOperationsForTarget(fragments []Fragment, target string) ([]NativeOperation, []Warning) {
	if target != "codex" {
		var warnings []Warning
		for _, f := range fragments {
			if f.Target == target {
				warnings = append(warnings, Warning{Code: "unsupported-native-fragment", Message: "target has no native fragment allowlist: " + target, Path: f.Provenance.Path})
			}
		}
		return nil, warnings
	}
	return codexOperations(fragments)
}

func codexOperations(fragments []Fragment) ([]NativeOperation, []Warning) {
	var warnings []Warning
	var configEntries []merge.NativeEntry
	hooksByEvent := map[string][]json.RawMessage{}
	for _, f := range fragments {
		if f.Target != "codex" {
			continue
		}
		switch {
		case f.Path == ".codex/config.toml" && f.Merge == MergeTOMLKey && strings.HasPrefix(f.Locator, "features."):
			configEntries = append(configEntries, merge.NativeEntry{Kind: merge.NativeKindTOMLKey, Locator: f.Locator, Content: f.Payload})
		case f.Path == ".codex/hooks.json" && f.Merge == MergeCodexHooks:
			event, err := codexHookEvent(f.Locator)
			if err != nil {
				warnings = append(warnings, Warning{Code: "invalid-codex-hook-locator", Message: err.Error(), Path: f.Provenance.Path})
				continue
			}
			if !json.Valid(f.Payload) {
				warnings = append(warnings, Warning{Code: "invalid-codex-hook-payload", Message: "payload is not valid JSON", Path: f.Provenance.Path})
				continue
			}
			hooksByEvent[event] = append(hooksByEvent[event], append([]byte(nil), f.Payload...))
		default:
			warnings = append(warnings, Warning{Code: "unsupported-codex-native-fragment", Message: fmt.Sprintf("unsupported codex native fragment path=%s merge=%s locator=%s", f.Path, f.Merge, f.Locator), Path: f.Provenance.Path})
		}
	}

	var ops []NativeOperation
	if len(configEntries) > 0 {
		ops = append(ops, NativeOperation{Target: "codex", Path: ".codex/config.toml", Entries: configEntries})
	}
	if len(hooksByEvent) > 0 {
		content, err := renderCodexHooks(hooksByEvent)
		if err != nil {
			warnings = append(warnings, Warning{Code: "invalid-codex-hooks", Message: err.Error()})
		} else {
			ops = append(ops, NativeOperation{Target: "codex", Path: ".codex/hooks.json", Entries: []merge.NativeEntry{{Kind: merge.NativeKindGeneratedJSON, Locator: "codex-hooks", Content: content}}})
		}
	}
	return ops, warnings
}

func codexHookEvent(locator string) (string, error) {
	event, id, ok := strings.Cut(locator, "/")
	if !ok || event == "" || id == "" {
		return "", fmt.Errorf("codex hook locator must be <Event>/<id>, got %q", locator)
	}
	return event, nil
}

func renderCodexHooks(byEvent map[string][]json.RawMessage) ([]byte, error) {
	events := make([]string, 0, len(byEvent))
	for event := range byEvent {
		events = append(events, event)
	}
	sort.Strings(events)

	hooks := make(map[string][]json.RawMessage, len(events))
	for _, event := range events {
		hooks[event] = byEvent[event]
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(map[string]any{
		"hooks": hooks,
	}); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}
