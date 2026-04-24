package git

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpen_NonExistent(t *testing.T) {
	_, err := Open(filepath.Join(t.TempDir(), "not-a-repo"))
	if err == nil {
		t.Fatal("expected error for missing repo dir")
	}
}

func TestRepository_Path(t *testing.T) {
	requireGit(t)
	withDetectReset(t)

	src := makeRepo(t)
	dst := filepath.Join(t.TempDir(), "out")
	if err := Clone(testCtx(t), src.Path, dst); err != nil {
		t.Fatalf("Clone: %v", err)
	}
	repo, err := Open(dst)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = repo.Close() }()

	if got := repo.Path(); got != dst {
		t.Fatalf("Path() = %q, want %q", got, dst)
	}
}

func TestSortEntries_Unsorted(t *testing.T) {
	// Covers the swap path of sortEntries; ReadTree happens to feed
	// sortEntries mostly-sorted input in practice, so exercise the
	// out-of-order case directly.
	entries := []TreeEntry{
		{Path: "z"}, {Path: "a"}, {Path: "m"}, {Path: "b"},
	}
	sortEntries(entries)
	for i := 1; i < len(entries); i++ {
		if entries[i-1].Path > entries[i].Path {
			t.Fatalf("sortEntries left unsorted result: %v", entries)
		}
	}
}

func TestHasCommit_Present(t *testing.T) {
	requireGit(t)
	withDetectReset(t)

	r := makeRepo(t)
	dst := filepath.Join(t.TempDir(), "out")
	if err := Clone(testCtx(t), r.Path, dst); err != nil {
		t.Fatalf("Clone: %v", err)
	}

	repo, err := Open(dst)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = repo.Close() }()

	has, err := repo.HasCommit(r.SecondSHA)
	if err != nil {
		t.Fatalf("HasCommit: %v", err)
	}
	if !has {
		t.Fatalf("expected HasCommit(%s) = true", r.SecondSHA)
	}
}

func TestHasCommit_Absent(t *testing.T) {
	requireGit(t)
	withDetectReset(t)

	r := makeRepo(t)
	dst := filepath.Join(t.TempDir(), "out")
	if err := Clone(testCtx(t), r.Path, dst); err != nil {
		t.Fatalf("Clone: %v", err)
	}
	repo, err := Open(dst)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = repo.Close() }()

	absent := "0000000000000000000000000000000000000000"
	has, err := repo.HasCommit(absent)
	if err != nil {
		t.Fatalf("HasCommit absent: %v", err)
	}
	if has {
		t.Fatalf("expected HasCommit(%s) = false", absent)
	}
}

func TestHasCommit_InvalidSHA(t *testing.T) {
	requireGit(t)
	withDetectReset(t)

	r := makeRepo(t)
	dst := filepath.Join(t.TempDir(), "out")
	if err := Clone(testCtx(t), r.Path, dst); err != nil {
		t.Fatalf("Clone: %v", err)
	}
	repo, err := Open(dst)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = repo.Close() }()

	if _, err := repo.HasCommit("not-a-sha"); err == nil {
		t.Fatal("expected error for malformed SHA")
	}
}

func TestReadTree_Deterministic(t *testing.T) {
	requireGit(t)
	withDetectReset(t)

	r := makeRepo(t)
	dst := filepath.Join(t.TempDir(), "out")
	if err := Clone(testCtx(t), r.Path, dst); err != nil {
		t.Fatalf("Clone: %v", err)
	}
	repo, err := Open(dst)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = repo.Close() }()

	first, err := repo.ReadTree(r.SecondSHA)
	if err != nil {
		t.Fatalf("ReadTree: %v", err)
	}
	second, err := repo.ReadTree(r.SecondSHA)
	if err != nil {
		t.Fatalf("ReadTree second call: %v", err)
	}
	if len(first) != len(second) {
		t.Fatalf("ReadTree lengths differ: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("ReadTree not deterministic at %d: %+v vs %+v", i, first[i], second[i])
		}
	}

	// AGENTS.md must be in the tree.
	var found bool
	for _, e := range first {
		if e.Path == "AGENTS.md" {
			found = true
			if e.Size <= 0 {
				t.Fatalf("AGENTS.md has non-positive size: %d", e.Size)
			}
		}
	}
	if !found {
		t.Fatal("AGENTS.md missing from ReadTree output")
	}

	// Sorted order invariant.
	for i := 1; i < len(first); i++ {
		if first[i-1].Path > first[i].Path {
			t.Fatalf("ReadTree not sorted: %q > %q", first[i-1].Path, first[i].Path)
		}
	}
}

func TestReadTree_InvalidSHA(t *testing.T) {
	requireGit(t)
	withDetectReset(t)

	r := makeRepo(t)
	dst := filepath.Join(t.TempDir(), "out")
	if err := Clone(testCtx(t), r.Path, dst); err != nil {
		t.Fatalf("Clone: %v", err)
	}
	repo, err := Open(dst)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = repo.Close() }()

	if _, err := repo.ReadTree("not-a-sha"); err == nil {
		t.Fatal("expected error for malformed SHA")
	}
}

func TestReadTree_MissingSHA(t *testing.T) {
	requireGit(t)
	withDetectReset(t)

	r := makeRepo(t)
	dst := filepath.Join(t.TempDir(), "out")
	if err := Clone(testCtx(t), r.Path, dst); err != nil {
		t.Fatalf("Clone: %v", err)
	}
	repo, err := Open(dst)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = repo.Close() }()

	absent := "abcdef0000000000000000000000000000000000"
	_, err = repo.ReadTree(absent)
	if !errors.Is(err, ErrShaNotInCache) {
		t.Fatalf("expected ErrShaNotInCache, got %v", err)
	}
}

func TestBlobContent_Happy(t *testing.T) {
	requireGit(t)
	withDetectReset(t)

	r := makeRepo(t)
	dst := filepath.Join(t.TempDir(), "out")
	if err := Clone(testCtx(t), r.Path, dst); err != nil {
		t.Fatalf("Clone: %v", err)
	}
	repo, err := Open(dst)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = repo.Close() }()

	content, err := repo.BlobContent(r.SecondSHA, "AGENTS.md")
	if err != nil {
		t.Fatalf("BlobContent: %v", err)
	}
	// The second commit adds "line 2".
	if !strings.Contains(string(content), "line 2") {
		t.Fatalf("expected blob to contain 'line 2', got %q", content)
	}
}

func TestBlobContent_Missing(t *testing.T) {
	requireGit(t)
	withDetectReset(t)

	r := makeRepo(t)
	dst := filepath.Join(t.TempDir(), "out")
	if err := Clone(testCtx(t), r.Path, dst); err != nil {
		t.Fatalf("Clone: %v", err)
	}
	repo, err := Open(dst)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = repo.Close() }()

	_, err = repo.BlobContent(r.SecondSHA, "does-not-exist.txt")
	if !errors.Is(err, ErrBlobNotFound) {
		t.Fatalf("expected ErrBlobNotFound, got %v", err)
	}
}

func TestBlobContent_EmptyPath(t *testing.T) {
	requireGit(t)
	withDetectReset(t)

	r := makeRepo(t)
	dst := filepath.Join(t.TempDir(), "out")
	if err := Clone(testCtx(t), r.Path, dst); err != nil {
		t.Fatalf("Clone: %v", err)
	}
	repo, err := Open(dst)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = repo.Close() }()

	_, err = repo.BlobContent(r.SecondSHA, "")
	if !errors.Is(err, ErrBlobNotFound) {
		t.Fatalf("expected ErrBlobNotFound for empty path, got %v", err)
	}
}

func TestBlobContent_FirstCommit_FileHasOldContent(t *testing.T) {
	requireGit(t)
	withDetectReset(t)

	r := makeRepo(t)
	dst := filepath.Join(t.TempDir(), "out")
	if err := Clone(testCtx(t), r.Path, dst); err != nil {
		t.Fatalf("Clone: %v", err)
	}
	repo, err := Open(dst)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = repo.Close() }()

	// At the first commit, the file exists but "line 2" does not yet.
	content, err := repo.BlobContent(r.InitialSHA, "AGENTS.md")
	if err != nil {
		t.Fatalf("BlobContent initial: %v", err)
	}
	if strings.Contains(string(content), "line 2") {
		t.Fatalf("initial commit should not contain 'line 2': %q", content)
	}
}
