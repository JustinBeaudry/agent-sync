package merge

import (
	"encoding/json"
	"fmt"
)

const (
	generatedJSONMarkerKey    = "_agent_sync_generated"
	generatedJSONMarkerString = "codex-hooks/v1"
)

var generatedJSONMarkerValue = json.RawMessage(`"` + generatedJSONMarkerString + `"`)

func mergeNativeGeneratedJSON(existing []byte, e NativeEntry) ([]byte, error) {
	if e.Locator != "codex-hooks" {
		return nil, fmt.Errorf("merge: unsupported generated JSON locator %q", e.Locator)
	}
	if !json.Valid(e.Content) {
		return nil, fmt.Errorf("%w: generated JSON payload is invalid", ErrMalformedToolOwnedFile)
	}
	if !isBlank(existing) {
		managed, err := isManagedGeneratedJSON(existing)
		if err != nil {
			return nil, err
		}
		if !managed {
			return nil, fmt.Errorf("merge: unmanaged existing JSON at generated native path")
		}
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(e.Content, &doc); err != nil {
		return nil, fmt.Errorf("%w: generated JSON payload must be an object", ErrMalformedToolOwnedFile)
	}
	doc[generatedJSONMarkerKey] = generatedJSONMarkerValue
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("merge: render generated JSON: %w", err)
	}
	out = append(out, '\n')
	return out, nil
}

func isManagedGeneratedJSON(existing []byte) (bool, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(existing, &doc); err != nil {
		return false, fmt.Errorf("%w: existing generated JSON is invalid", ErrMalformedToolOwnedFile)
	}
	raw, ok := doc[generatedJSONMarkerKey]
	if !ok {
		return false, nil
	}
	var marker string
	if err := json.Unmarshal(raw, &marker); err != nil {
		return false, nil
	}
	return marker == generatedJSONMarkerString, nil
}
