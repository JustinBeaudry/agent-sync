package cache_test

import (
	"bufio"
	"errors"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aienvs/aienvs/internal/cache"
)

func loadPairs(t *testing.T, name string) [][2]string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	f, err := os.Open(filepath.Join(wd, "..", "..", "testdata", "urls", name))
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	defer func() { _ = f.Close() }()

	var out [][2]string
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := s.Text()
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 2 {
			t.Fatalf("bad fixture line (need exactly one tab): %q", line)
		}
		out = append(out, [2]string{parts[0], parts[1]})
	}
	if err := s.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	return out
}

func loadEquivalenceClasses(t *testing.T, name string) [][]string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(wd, "..", "..", "testdata", "urls", name))
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var classes [][]string
	var current []string
	for _, ln := range strings.Split(string(b), "\n") {
		trimmed := strings.TrimSpace(ln)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if trimmed == "" {
			if len(current) > 0 {
				classes = append(classes, current)
				current = nil
			}
			continue
		}
		current = append(current, trimmed)
	}
	if len(current) > 0 {
		classes = append(classes, current)
	}
	return classes
}

func loadLines(t *testing.T, name string) []string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(wd, "..", "..", "testdata", "urls", name))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var out []string
	for _, ln := range strings.Split(string(b), "\n") {
		trimmed := strings.TrimSpace(ln)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}

func TestCanonicalize_Golden(t *testing.T) {
	for _, p := range loadPairs(t, "golden.txt") {
		in, want := p[0], p[1]
		t.Run(in, func(t *testing.T) {
			got, err := cache.Canonicalize(in)
			if err != nil {
				t.Fatalf("canonicalize: %v", err)
			}
			if got != want {
				t.Errorf("Canonicalize(%q) = %q, want %q", in, got, want)
			}
		})
	}
}

func TestCanonicalize_Equivalence(t *testing.T) {
	// Every URL in a class must canonicalize and hash identically.
	for i, class := range loadEquivalenceClasses(t, "equivalent-sets.txt") {
		if len(class) < 2 {
			continue
		}
		t.Run(class[0], func(t *testing.T) {
			first, err := cache.Canonicalize(class[0])
			if err != nil {
				t.Fatalf("class %d: first URL %q: %v", i, class[0], err)
			}
			firstKey := cache.Key(first)

			for _, u := range class[1:] {
				got, err := cache.Canonicalize(u)
				if err != nil {
					t.Fatalf("class %d: %q: %v", i, u, err)
				}
				if got != first {
					t.Errorf("class %d: %q -> %q, want %q (equivalence broken)", i, u, got, first)
				}
				if k := cache.Key(got); k != firstKey {
					t.Errorf("class %d: %q cache key %s != %s", i, u, k, firstKey)
				}
			}
		})
	}
}

func TestCanonicalize_Unsupported(t *testing.T) {
	for _, u := range loadLines(t, "unsupported.txt") {
		t.Run(u, func(t *testing.T) {
			_, err := cache.Canonicalize(u)
			if err == nil {
				t.Fatalf("expected rejection of %q", u)
			}
			if !errors.Is(err, cache.ErrUnsupportedURL) {
				t.Errorf("want ErrUnsupportedURL, got %v", err)
			}
		})
	}
}

func TestCanonicalize_RejectsEmpty(t *testing.T) {
	_, err := cache.Canonicalize("")
	if !errors.Is(err, cache.ErrUnsupportedURL) {
		t.Errorf("empty input: want ErrUnsupportedURL, got %v", err)
	}
	// Whitespace-only should also reject.
	_, err = cache.Canonicalize("   \t  ")
	if !errors.Is(err, cache.ErrUnsupportedURL) {
		t.Errorf("whitespace input: want ErrUnsupportedURL, got %v", err)
	}
}

func TestCanonicalize_PathCasePreserved(t *testing.T) {
	got, err := cache.Canonicalize("https://github.com/Foo/Bar")
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	if !strings.Contains(got, "/Foo/Bar") {
		t.Errorf("path case not preserved: got %q", got)
	}

	// A differently-cased path must produce a different cache key.
	c1, _ := cache.Canonicalize("https://github.com/Foo/Bar")
	c2, _ := cache.Canonicalize("https://github.com/foo/bar")
	k1 := cache.Key(c1)
	k2 := cache.Key(c2)
	if k1 == k2 {
		t.Errorf("expected distinct keys for case-different paths (GitHub is case-sensitive)")
	}
}

func TestCanonicalize_DropsCredentials(t *testing.T) {
	// https-with-userinfo must produce no `@` in canonical form —
	// defense-in-depth atop unit 5's explicit rejection.
	got, err := cache.Canonicalize("https://user:token@github.com/foo/bar.git")
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	if strings.Contains(got, "@") {
		t.Errorf("credentials leaked into canonical form: %q", got)
	}
	if strings.Contains(got, "user") || strings.Contains(got, "token") {
		t.Errorf("credentials leaked: %q", got)
	}
}

func TestCanonicalize_SSHKeepsUser(t *testing.T) {
	got, err := cache.Canonicalize("ssh://git@github.com/foo/bar.git")
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	if !strings.Contains(got, "git@github.com") {
		t.Errorf("ssh canonical should keep git@: %q", got)
	}
}

func TestCanonicalize_SSHStripsPassword(t *testing.T) {
	got, err := cache.Canonicalize("ssh://git:secret@github.com/foo/bar.git")
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	if strings.Contains(got, "secret") {
		t.Errorf("ssh password leaked into canonical: %q", got)
	}
	if !strings.Contains(got, "git@github.com") {
		t.Errorf("ssh user stripped unexpectedly: %q", got)
	}
}

func TestCanonicalize_DeterministicFuzz(t *testing.T) {
	// Randomized inputs must produce deterministic output (no panic,
	// no time-dependent variation). Loop twice and compare.
	r := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // not security-sensitive
	for i := 0; i < 200; i++ {
		raw := fuzzURL(r)
		g1, e1 := cache.Canonicalize(raw)
		g2, e2 := cache.Canonicalize(raw)
		if (e1 == nil) != (e2 == nil) {
			t.Errorf("non-deterministic error for %q: %v vs %v", raw, e1, e2)
			continue
		}
		if g1 != g2 {
			t.Errorf("non-deterministic output for %q: %q vs %q", raw, g1, g2)
		}
	}
}

func fuzzURL(r *rand.Rand) string {
	schemes := []string{"https", "http", "ssh", "git", "ftp", ""}
	hosts := []string{"github.com", "GITHUB.com", "gitlab.example.internal", "", "::bogus::"}
	paths := []string{"/foo/bar", "/Foo/Bar", "//foo//bar//", "/foo/bar.git", "", "/"}
	users := []string{"", "git@", "user:token@", "TOKEN@", "deploy-bot@"}

	scheme := schemes[r.IntN(len(schemes))]
	host := hosts[r.IntN(len(hosts))]
	path := paths[r.IntN(len(paths))]
	user := users[r.IntN(len(users))]

	if scheme == "" {
		// scp-style probe: user@host:path.
		if user == "" {
			user = "git@"
		}
		return strings.TrimSuffix(user, "@") + "@" + host + ":" + strings.TrimPrefix(path, "/")
	}
	return scheme + "://" + user + host + path
}
