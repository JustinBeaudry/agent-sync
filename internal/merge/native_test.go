package merge

import (
	"errors"
	"strings"
	"testing"
)

func TestMergeNativeTOMLKey_UpsertsFeatureWithoutRewritingOtherContent(t *testing.T) {
	base := []byte("# user comment\nmodel = \"gpt-5\"\n\n[features]\nfast_mode = false\n")
	entry := NativeEntry{Kind: NativeKindTOMLKey, Locator: "features.hooks", Content: []byte("[features]\nhooks = true\n")}
	out, hash, err := mergeNative(base, []NativeEntry{entry})
	if err != nil {
		t.Fatalf("mergeNative: %v", err)
	}
	text := string(out)
	for _, want := range []string{"# user comment\n", "model = \"gpt-5\"\n", "fast_mode = false\n", "hooks = true\n"} {
		if !strings.Contains(text, want) {
			t.Fatalf("output missing %q:\n%s", want, text)
		}
	}
	if hash == "" {
		t.Fatal("hash is empty")
	}
}

func TestMergeNativeTOMLKey_ReplacesExistingKey(t *testing.T) {
	base := []byte("[features]\nhooks = false\n")
	entry := NativeEntry{Kind: NativeKindTOMLKey, Locator: "features.hooks", Content: []byte("[features]\nhooks = true\n")}
	out, _, err := mergeNative(base, []NativeEntry{entry})
	if err != nil {
		t.Fatalf("mergeNative: %v", err)
	}
	if string(out) != "[features]\nhooks = true\n" {
		t.Fatalf("out = %q", out)
	}
}

func TestMergeNativeTOMLKey_InsertsIntoFeaturesTableWithInlineComment(t *testing.T) {
	base := []byte("[features] # Codex feature flags\nfast_mode = false\n\n[other]\nvalue = 1\n")
	entry := NativeEntry{Kind: NativeKindTOMLKey, Locator: "features.hooks", Content: []byte("[features]\nhooks = true\n")}
	out, _, err := mergeNative(base, []NativeEntry{entry})
	if err != nil {
		t.Fatalf("mergeNative: %v", err)
	}
	text := string(out)
	if strings.Count(text, "[features]") != 1 {
		t.Fatalf("output has duplicate [features] table:\n%s", text)
	}
	if !strings.Contains(text, "[features] # Codex feature flags\nhooks = true\nfast_mode = false\n") {
		t.Fatalf("output did not insert under existing features table:\n%s", text)
	}
}

func TestMergeNativeTOMLKey_RejectsNonScalarFeatureValue(t *testing.T) {
	tests := []struct {
		name    string
		payload string
	}{
		{name: "inline table", payload: "[features]\nhooks = { enabled = true }\n"},
		{name: "array", payload: "[features]\nhooks = [true]\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			entry := NativeEntry{Kind: NativeKindTOMLKey, Locator: "features.hooks", Content: []byte(tc.payload)}
			_, _, err := mergeNative(nil, []NativeEntry{entry})
			if !errors.Is(err, ErrMalformedToolOwnedFile) {
				t.Fatalf("err = %v, want ErrMalformedToolOwnedFile", err)
			}
		})
	}
}

func TestMergeNativeGeneratedJSON_RefusesUnmanagedExistingFile(t *testing.T) {
	entry := NativeEntry{Kind: NativeKindGeneratedJSON, Locator: "codex-hooks", Content: []byte(`{"hooks":{"PreToolUse":[]}}`)}
	_, _, err := mergeNative([]byte(`{"hooks":{"Stop":[]}}`), []NativeEntry{entry})
	if !errors.Is(err, ErrUnmanagedGeneratedFile) {
		t.Fatalf("err = %v, want ErrUnmanagedGeneratedFile", err)
	}
}

func TestMergeNativeGeneratedJSON_RefusesNestedMarkerOnly(t *testing.T) {
	entry := NativeEntry{Kind: NativeKindGeneratedJSON, Locator: "codex-hooks", Content: []byte(`{"hooks":{"PreToolUse":[]}}`)}
	_, _, err := mergeNative([]byte(`{"nested":{"_agent_sync_generated":"codex-hooks/v1"}}`), []NativeEntry{entry})
	if !errors.Is(err, ErrUnmanagedGeneratedFile) {
		t.Fatalf("err = %v, want ErrUnmanagedGeneratedFile", err)
	}
}

func TestMergeNativeGeneratedJSON_RendersCodexSchemaOnly(t *testing.T) {
	entry := NativeEntry{Kind: NativeKindGeneratedJSON, Locator: "codex-hooks", Content: []byte(`{"hooks":{"PreToolUse":[]}}`)}
	out, _, err := mergeNative(nil, []NativeEntry{entry})
	if err != nil {
		t.Fatalf("mergeNative create: %v", err)
	}
	if strings.Contains(string(out), "_agent_sync_generated") {
		t.Fatalf("generated JSON should not include agent-sync marker:\n%s", out)
	}
}

func TestMergeNativeGeneratedJSON_AllowsIdempotentManagedRewrite(t *testing.T) {
	entry := NativeEntry{Kind: NativeKindGeneratedJSON, Locator: "codex-hooks", Content: []byte(`{"hooks":{"PreToolUse":[{"command":"printf '<done>' && echo ok"}]}}`)}
	out, _, err := mergeNative(nil, []NativeEntry{entry})
	if err != nil {
		t.Fatalf("mergeNative create: %v", err)
	}
	out2, _, err := mergeNativeWithOptions(out, []NativeEntry{entry}, NativeMergeOptions{AllowExistingGeneratedJSON: true})
	if err != nil {
		t.Fatalf("mergeNative rewrite: %v", err)
	}
	if string(out) != string(out2) {
		t.Fatalf("rewrite changed bytes:\n%s\n---\n%s", out, out2)
	}
	text := string(out)
	if !strings.Contains(text, "\n  \"hooks\": {") {
		t.Fatalf("generated JSON should be indented:\n%s", text)
	}
	if !strings.Contains(text, "printf '<done>' && echo ok") {
		t.Fatalf("generated JSON should not HTML-escape command characters:\n%s", text)
	}
	if strings.HasSuffix(text, "\n") {
		t.Fatalf("generated JSON should not have encoder trailing newline:\n%s", text)
	}
}
