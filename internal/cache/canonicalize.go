// Package cache computes deterministic filesystem locations for
// materialized canonical-source clones.
//
// The package has three concerns, each in its own file:
//
//   - canonicalize.go — turn arbitrary user-supplied Git URLs into a
//     stable, credential-stripped, hash-friendly canonical form.
//   - key.go — hash the canonical form into a hex SHA-256 used as the
//     cache directory name.
//   - location.go — resolve the directory on disk, honoring manifest
//     overrides and XDG conventions.
//
// Canonicalization rules are documented on Canonicalize and covered by
// golden fixtures in testdata/urls. Changing any rule is a breaking
// change to cache-key stability and must be done alongside a migration
// strategy for already-populated caches.
package cache

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// ErrUnsupportedURL is returned when a URL is syntactically unparseable
// or uses a scheme agent-sync does not support.
var ErrUnsupportedURL = errors.New("unsupported canonical URL")

// Canonicalize returns the stable canonical form of a Git URL.
//
// Rules (order matters):
//
//  1. scp-style SSH (`git@host:owner/repo.git`) is rewritten to
//     `ssh://git@host/owner/repo.git`.
//  2. The URL is parsed. Unsupported schemes return ErrUnsupportedURL.
//  3. Host is lowercased.
//  4. Default ports are stripped (443 for https, 22 for ssh, 80 for
//     http, 9418 for git).
//  5. Userinfo handling depends on scheme:
//     - https/http: userinfo is stripped entirely (embedded credentials
//     are a supply-chain hazard; unit 5 rejects them at init with a
//     specific error; the canonicalizer is defense-in-depth).
//     - ssh/git: userinfo is kept (conventional `git@` user is
//     expected). Password is stripped regardless of scheme.
//  6. Path case is preserved (GitHub paths are case-sensitive).
//  7. A trailing `.git` suffix is normalized onto the path so that
//     `foo/bar` and `foo/bar.git` map to the same canonical form.
//  8. Fragment and query are dropped.
//
// The returned string is safe to hash and safe to write into an audit
// trail (no secrets).
func Canonicalize(rawURL string) (string, error) {
	raw := strings.TrimSpace(rawURL)
	if raw == "" {
		return "", fmt.Errorf("%w: empty", ErrUnsupportedURL)
	}

	if scp := scpToSSH(raw); scp != "" {
		raw = scp
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("%w: parse %q: %w", ErrUnsupportedURL, rawURL, err)
	}

	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "https", "http", "ssh", "git":
	default:
		return "", fmt.Errorf("%w: scheme %q", ErrUnsupportedURL, u.Scheme)
	}
	u.Scheme = scheme

	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", fmt.Errorf("%w: missing host: %q", ErrUnsupportedURL, rawURL)
	}
	// Wrap IPv6 literals in brackets so the URL remains valid after
	// reassigning u.Host. u.Hostname() strips the brackets; we must
	// restore them when the host contains a colon.
	hostForAssign := host
	if strings.Contains(host, ":") {
		hostForAssign = "[" + host + "]"
	}
	if port := u.Port(); port != "" && !isDefaultPort(scheme, port) {
		u.Host = hostForAssign + ":" + port
	} else {
		u.Host = hostForAssign
	}

	switch scheme {
	case "https", "http":
		u.User = nil
	case "ssh", "git":
		if u.User != nil {
			// Preserve username, drop password.
			u.User = url.User(u.User.Username())
		}
	}

	u.Fragment = ""
	u.RawQuery = ""
	u.RawFragment = ""

	// Reject empty or root-only paths — there is no repository component.
	if u.Path == "" || u.Path == "/" {
		return "", fmt.Errorf("%w: URL has no repository path", ErrUnsupportedURL)
	}

	// Reject path traversal segments.
	for _, seg := range strings.Split(strings.Trim(u.Path, "/"), "/") {
		if seg == ".." || seg == "." {
			return "", fmt.Errorf("%w: path contains traversal segment", ErrUnsupportedURL)
		}
	}

	u.Path = normalizeGitPath(u.Path)

	return u.String(), nil
}

// scpPattern matches scp-style Git URLs: user@host:path. The path must
// be non-empty, must not start with '/' (scp-style paths are relative),
// and must not contain ':' (a second colon would be ambiguous).
// We intentionally do not match `host:path` without a user (too
// ambiguous against drive letters on Windows).
var scpPattern = regexp.MustCompile(`^([A-Za-z0-9._-]+)@([A-Za-z0-9.-]+):([^:/][^:]*)$`)

// scpToSSH rewrites scp-style SSH into a proper ssh:// URL. Returns
// "" for inputs that aren't scp-style. See canonicalize rule #1.
//
// Only relative paths are accepted (scpPattern rejects any path that
// starts with '/'), so no leading-slash stripping is needed here.
func scpToSSH(raw string) string {
	m := scpPattern.FindStringSubmatch(raw)
	if m == nil {
		return ""
	}
	user, host, path := m[1], m[2], m[3]
	return "ssh://" + user + "@" + host + "/" + path
}

// isDefaultPort returns true when port is the registered default for
// scheme. Default ports must be stripped so `https://h:443/x` and
// `https://h/x` hash identically.
func isDefaultPort(scheme, port string) bool {
	switch {
	case scheme == "https" && port == "443":
		return true
	case scheme == "http" && port == "80":
		return true
	case scheme == "ssh" && port == "22":
		return true
	case scheme == "git" && port == "9418":
		return true
	}
	return false
}

// normalizeGitPath enforces a trailing `.git` on the repo component
// and strips redundant slashes. The leading "/" is preserved; the
// overall case is not touched.
func normalizeGitPath(p string) string {
	if p == "" || p == "/" {
		return p
	}
	// Collapse consecutive slashes so `foo//bar` and `foo/bar` agree.
	for strings.Contains(p, "//") {
		p = strings.ReplaceAll(p, "//", "/")
	}
	p = strings.TrimSuffix(p, "/")
	if !strings.HasSuffix(p, ".git") {
		p += ".git"
	}
	return p
}
