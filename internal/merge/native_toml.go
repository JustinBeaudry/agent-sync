package merge

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

var featuresKeyLocator = regexp.MustCompile(`\Afeatures\.[A-Za-z0-9_-]+\z`)

func mergeNativeTOMLKey(existing []byte, e NativeEntry) ([]byte, error) {
	if !featuresKeyLocator.MatchString(e.Locator) {
		return nil, fmt.Errorf("merge: unsupported native TOML locator %q", e.Locator)
	}
	if !isBlank(existing) {
		var parsed map[string]any
		if err := toml.Unmarshal(existing, &parsed); err != nil {
			return nil, fmt.Errorf("%w: malformed TOML", ErrMalformedToolOwnedFile)
		}
	}

	var payload map[string]map[string]any
	if err := toml.Unmarshal(e.Content, &payload); err != nil {
		return nil, fmt.Errorf("%w: malformed native TOML payload", ErrMalformedToolOwnedFile)
	}
	key := strings.TrimPrefix(e.Locator, "features.")
	value, ok := payload["features"][key]
	if !ok {
		return nil, fmt.Errorf("merge: native TOML payload must set [features].%s", key)
	}
	rendered, err := renderNativeTOMLValue(value)
	if err != nil {
		return nil, err
	}
	return spliceFeatureKey(existing, key, rendered), nil
}

func renderNativeTOMLValue(v any) (string, error) {
	var b bytes.Buffer
	if err := toml.NewEncoder(&b).Encode(map[string]any{"v": v}); err != nil {
		return "", fmt.Errorf("merge: render native TOML value: %w", err)
	}
	line := strings.TrimSpace(b.String())
	_, value, ok := strings.Cut(line, "=")
	if !ok {
		return "", fmt.Errorf("merge: render native TOML value produced %q", line)
	}
	return strings.TrimSpace(value), nil
}

func spliceFeatureKey(existing []byte, key, rendered string) []byte {
	lines := splitLinesPreserve(existing)
	inFeatures := false
	featuresStart := -1
	featuresEnd := len(lines)
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if header, ok := tomlHeaderName(trimmed); ok {
			if header == "[features]" {
				inFeatures = true
				featuresStart = i
				continue
			}
			if inFeatures {
				featuresEnd = i
				break
			}
		}
		if inFeatures && tomlKeyLineMatches(trimmed, key) {
			lines[i] = key + " = " + rendered + "\n"
			return []byte(strings.Join(lines, ""))
		}
	}

	insert := key + " = " + rendered + "\n"
	if featuresStart >= 0 {
		out := append([]string{}, lines[:featuresStart+1]...)
		out = append(out, insert)
		out = append(out, lines[featuresStart+1:featuresEnd]...)
		out = append(out, lines[featuresEnd:]...)
		return []byte(strings.Join(out, ""))
	}
	if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
		lines = append(lines, "\n")
	}
	lines = append(lines, "[features]\n", insert)
	return []byte(strings.Join(lines, ""))
}

func tomlHeaderName(trimmed string) (string, bool) {
	if !strings.HasPrefix(trimmed, "[") {
		return "", false
	}
	end := strings.IndexByte(trimmed, ']')
	if end < 0 {
		return "", false
	}
	return strings.TrimSpace(trimmed[:end+1]), true
}

func tomlKeyLineMatches(trimmed, key string) bool {
	if !strings.HasPrefix(trimmed, key) {
		return false
	}
	rest := strings.TrimLeft(trimmed[len(key):], " \t")
	return strings.HasPrefix(rest, "=")
}

func splitLinesPreserve(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	parts := strings.SplitAfter(string(b), "\n")
	if parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}
