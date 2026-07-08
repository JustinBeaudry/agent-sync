package merge

import (
	"bytes"
	"encoding/json"
	"fmt"
)

func mergeNativeGeneratedJSON(existing []byte, e NativeEntry, allowExisting bool) ([]byte, error) {
	if e.Locator != "codex-hooks" {
		return nil, fmt.Errorf("merge: unsupported generated JSON locator %q", e.Locator)
	}
	if !json.Valid(e.Content) {
		return nil, fmt.Errorf("%w: generated JSON payload is invalid", ErrMalformedToolOwnedFile)
	}
	if !isBlank(existing) && !allowExisting {
		return nil, ErrUnmanagedGeneratedFile
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(e.Content, &doc); err != nil {
		return nil, fmt.Errorf("%w: generated JSON payload must be an object", ErrMalformedToolOwnedFile)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("merge: render generated JSON: %w", err)
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}
