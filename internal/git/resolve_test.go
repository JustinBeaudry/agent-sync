package git

import (
	"testing"
)

func TestResolveRef_ShortCircuitSHA(t *testing.T) {
	withDetectReset(t)

	// A well-formed 40-char lowercase hex SHA must be returned verbatim
	// with no network call. The canonical URL here is nonsense on
	// purpose: if we were calling ls-remote, this would fail.
	in := "1234567890abcdef1234567890abcdef12345678"
	got, err := ResolveRef(testCtx(t), "https://example.invalid/r.git", in)
	if err != nil {
		t.Fatalf("ResolveRef: %v", err)
	}
	if got != in {
		t.Fatalf("ResolveRef returned %q, want %q", got, in)
	}
}

func TestResolveRef_BranchToSHA(t *testing.T) {
	requireGit(t)
	withDetectReset(t)

	r := makeRepo(t)

	got, err := ResolveRef(testCtx(t), r.Path, r.HeadBranch)
	if err != nil {
		t.Fatalf("ResolveRef: %v", err)
	}
	if got != r.SecondSHA {
		t.Fatalf("ResolveRef = %q, want %q", got, r.SecondSHA)
	}
}

func TestResolveRef_EmptyRef(t *testing.T) {
	withDetectReset(t)

	_, err := ResolveRef(testCtx(t), "https://example.invalid/r.git", "   ")
	if err == nil {
		t.Fatal("expected error for empty ref")
	}
}

func TestResolveRef_MixedCaseSHA_NotShortCircuited(t *testing.T) {
	requireGit(t)
	withDetectReset(t)

	r := makeRepo(t)

	// Uppercase hex is not a match for shaPattern (which requires
	// lowercase). That means ResolveRef must not short-circuit — it
	// treats the input as a ref name and hands it to ls-remote, which
	// also won't find it. Result: a failure. This locks in the rule
	// that we don't silently normalize SHA case: if the caller
	// mis-formats, they get an error, not a "lucky" success.
	upper := "1234567890ABCDEF1234567890ABCDEF12345678"
	_, err := ResolveRef(testCtx(t), r.Path, upper)
	if err == nil {
		t.Fatal("expected failure: uppercase SHA is not a valid ref and should not short-circuit")
	}
}
