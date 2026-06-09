package merge

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// mergeJSON upserts or removes the agent-sync entry at the entry's
// JSON-pointer locator inside existing, preserving user keys, ordering,
// and formatting. It strict-validates existing first; an unparseable
// file or a non-object parent is ErrMalformedToolOwnedFile (we never
// rewrite a file we cannot parse). Returns the merged bytes and the
// SHA-256 of the rendered agent-sync slice (empty on remove).
func mergeJSON(existing []byte, e MergeEntry) (result []byte, sliceHash string, err error) {
	if _, err := entryID(e); err != nil {
		return nil, "", err
	}

	base := existing
	if isBlank(base) {
		base = []byte("{}")
	} else if !json.Valid(base) {
		return nil, "", fmt.Errorf("%w: not valid JSON", ErrMalformedToolOwnedFile)
	}

	path, err := pointerToSjsonPath(e.Locator)
	if err != nil {
		return nil, "", err
	}
	parentPath := parentSjsonPath(path)

	// The parent (e.g. mcpServers), if present, must be an object — we
	// will not set a key under an array/scalar.
	if parentPath != "" {
		if parent := gjson.GetBytes(base, parentPath); parent.Exists() && !parent.IsObject() {
			return nil, "", fmt.Errorf("%w: %q is not a JSON object", ErrMalformedToolOwnedFile, parentPath)
		}
		// Reject a pre-existing duplicate of the agent-sync key under the
		// parent (raw duplicate keys = ledger drift; JSON semantics make
		// them degenerate so refuse rather than silently last-wins).
		if dupCount(base, parentPath, lastSegment(path)) > 1 {
			return nil, "", fmt.Errorf("%w: duplicate key %q under %q", ErrMalformedToolOwnedFile, lastSegment(path), parentPath)
		}
	}

	hadTrailingNewline := bytes.HasSuffix(existing, []byte("\n"))

	if e.Remove {
		result, err = sjson.DeleteBytes(base, path)
		if err != nil {
			return nil, "", fmt.Errorf("merge: json delete %q: %w", path, err)
		}
		sliceHash = ""
	} else {
		if !json.Valid(e.Content) {
			return nil, "", fmt.Errorf("%w: entry content is not a valid JSON value", ErrMalformedToolOwnedFile)
		}
		result, err = sjson.SetRawBytes(base, path, e.Content)
		if err != nil {
			return nil, "", fmt.Errorf("merge: json set %q: %w", path, err)
		}
		sum := sha256.Sum256([]byte(gjson.GetBytes(result, path).Raw))
		sliceHash = hex.EncodeToString(sum[:])
	}

	// Preserve trailing-newline convention of the input.
	resultHasNewline := bytes.HasSuffix(result, []byte("\n"))
	switch {
	case hadTrailingNewline && !resultHasNewline:
		result = append(result, '\n')
	case !hadTrailingNewline && resultHasNewline:
		result = bytes.TrimRight(result, "\n")
	}
	return result, sliceHash, nil
}

// pointerToSjsonPath converts a JSON pointer (/mcpServers/aienvs_foo)
// to an sjson dot path (mcpServers.aienvs_foo), escaping sjson-special
// characters (. * ? \) in each segment so a key that contains them is
// treated literally.
func pointerToSjsonPath(pointer string) (string, error) {
	if !strings.HasPrefix(pointer, "/") {
		return "", fmt.Errorf("merge: json-pointer must start with '/': %q", pointer)
	}
	segs := strings.Split(pointer[1:], "/")
	for i, s := range segs {
		if s == "" {
			return "", fmt.Errorf("merge: empty segment in json-pointer %q", pointer)
		}
		// JSON pointer unescaping (~1 -> /, ~0 -> ~) then sjson escaping.
		s = strings.ReplaceAll(s, "~1", "/")
		s = strings.ReplaceAll(s, "~0", "~")
		s = sjsonEscapeSegment(s)
		segs[i] = s
	}
	return strings.Join(segs, "."), nil
}

func sjsonEscapeSegment(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '.', '*', '?', '\\':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func parentSjsonPath(path string) string {
	i := strings.LastIndex(path, ".")
	if i < 0 {
		return ""
	}
	return path[:i]
}

func lastSegment(path string) string {
	i := strings.LastIndex(path, ".")
	if i < 0 {
		return path
	}
	return path[i+1:]
}

// dupCount counts how many times key appears directly under parentPath
// in the raw JSON (gjson visits each raw key, so duplicates are seen
// more than once). parentPath "" means the document root.
func dupCount(data []byte, parentPath, key string) int {
	obj := gjson.ParseBytes(data)
	if parentPath != "" {
		obj = gjson.GetBytes(data, parentPath)
	}
	if !obj.IsObject() {
		return 0
	}
	// Unescape the sjson-escaped key for comparison against raw keys.
	want := strings.NewReplacer(`\.`, ".", `\*`, "*", `\?`, "?", `\\`, `\`).Replace(key)
	n := 0
	obj.ForEach(func(k, _ gjson.Result) bool {
		if k.String() == want {
			n++
		}
		return true
	})
	return n
}
