package git

import (
	"context"
	"testing"
)

func TestResolveLocalRef(t *testing.T) {
	requireGit(t)
	repo := makeRepo(t)
	ctx := context.Background()

	// HEAD resolves to the branch tip.
	if got, err := ResolveLocalRef(ctx, repo.Path, "HEAD"); err != nil || got != repo.SecondSHA {
		t.Fatalf("ResolveLocalRef HEAD = %q, %v; want %q", got, err, repo.SecondSHA)
	}
	// Empty ref defaults to HEAD.
	if got, err := ResolveLocalRef(ctx, repo.Path, ""); err != nil || got != repo.SecondSHA {
		t.Fatalf("ResolveLocalRef \"\" = %q, %v; want %q", got, err, repo.SecondSHA)
	}
	// Branch name resolves to its tip.
	if got, err := ResolveLocalRef(ctx, repo.Path, repo.HeadBranch); err != nil || got != repo.SecondSHA {
		t.Fatalf("ResolveLocalRef %q = %q, %v; want %q", repo.HeadBranch, got, err, repo.SecondSHA)
	}
	// Annotated tag peels to the underlying commit (not the tag object).
	if got, err := ResolveLocalRef(ctx, repo.Path, repo.TagName); err != nil || got != repo.TagSHA {
		t.Fatalf("ResolveLocalRef %q = %q, %v; want commit %q", repo.TagName, got, err, repo.TagSHA)
	}
	// A literal SHA round-trips.
	if got, err := ResolveLocalRef(ctx, repo.Path, repo.InitialSHA); err != nil || got != repo.InitialSHA {
		t.Fatalf("ResolveLocalRef %q = %q, %v; want itself", repo.InitialSHA, got, err)
	}
	// An unknown ref errors.
	if _, err := ResolveLocalRef(ctx, repo.Path, "no-such-ref"); err == nil {
		t.Fatal("expected error for unknown ref")
	}
	// A relative repo path is rejected.
	if _, err := ResolveLocalRef(ctx, "relative/path", "HEAD"); err == nil {
		t.Fatal("expected error for relative repo path")
	}
}
