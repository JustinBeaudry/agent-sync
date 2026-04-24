package git

import (
	"context"
	"fmt"
	"strings"
)

// ResolveRef resolves ref to a 40-char lowercase hex SHA against the
// remote at canonicalURL. It is the user-facing entry point used at
// `init` time and by floating-mode re-resolution.
//
// Preconditions:
//   - canonicalURL has already been normalized by cache.Canonicalize;
//     defense-in-depth is applied via checkInlineCredential inside
//     LsRemote, so feeding an un-canonicalized URL will fail fast on
//     inline credentials but will not stop at other canonicalization
//     violations. Callers should canonicalize first.
//   - ref is a non-empty branch or tag name, or a 40-char SHA. A SHA
//     short-circuits the ls-remote call.
//
// ResolveRef is deliberately thin; its job is to (a) short-circuit when
// the caller already has a SHA, (b) normalize whitespace, and (c) wrap
// the LsRemote result with a caller-friendly error.
func ResolveRef(ctx context.Context, canonicalURL, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("git: resolve: empty ref")
	}
	// If the caller already passed a SHA, accept it without a network
	// round-trip. Floating-mode callers never reach this branch because
	// they always pass a ref name.
	if shaPattern.MatchString(ref) {
		return ref, nil
	}

	sha, err := LsRemote(ctx, canonicalURL, ref)
	if err != nil {
		return "", fmt.Errorf("git: resolve %q on %q: %w", ref, canonicalURL, err)
	}
	return sha, nil
}
