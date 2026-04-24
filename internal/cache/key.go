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
