package cache

import (
	"crypto/sha256"
	"encoding/hex"
)

// KeyPrefix is the fixed prefix hashed alongside the canonical URL to
// derive the cache key. Kept explicit so future cache shapes (mirrors,
// worktree snapshots) can use distinct prefixes without colliding with
// the Git-URL keyspace.
const KeyPrefix = "git:"

// Key returns the deterministic cache-directory name for a canonical
// URL. It is hex(SHA-256(KeyPrefix + canonical)).
//
// The canonical form MUST come from Canonicalize. Hashing an uncleaned
// URL silently forks the keyspace (same repo, different cache dirs).
//
// Pattern: mirrors Go cmd/go's modfetch/codehost.WorkDir.
func Key(canonical string) string {
	sum := sha256.Sum256([]byte(KeyPrefix + canonical))
	return hex.EncodeToString(sum[:])
}

// KeyFromRaw is a convenience wrapper around Canonicalize + Key. The
// two-step form is exposed separately because most callers have the
// canonical form already (manifest load, audit file).
func KeyFromRaw(rawURL string) (string, error) {
	c, err := Canonicalize(rawURL)
	if err != nil {
		return "", err
	}
	return Key(c), nil
}
