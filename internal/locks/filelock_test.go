package locks_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aienvs/aienvs/internal/locks"
)

func TestAcquireFile_HappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "AGENTS.md")

	fl, err := locks.AcquireFile(context.Background(), target, locks.FileLockOptions{})
	if err != nil {
		t.Fatalf("AcquireFile: %v", err)
	}

	// Flock sidecar exists.
	if _, err := os.Stat(target + ".aienvs-flock"); err != nil {
		t.Errorf("flock sidecar not found: %v", err)
	}

	if err := fl.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

func TestAcquireFile_Sequential(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "AGENTS.md")

	fl1, err := locks.AcquireFile(context.Background(), target, locks.FileLockOptions{})
	if err != nil {
		t.Fatalf("first AcquireFile: %v", err)
	}
	if err := fl1.Release(); err != nil {
		t.Fatal(err)
	}

	fl2, err := locks.AcquireFile(context.Background(), target, locks.FileLockOptions{})
	if err != nil {
		t.Fatalf("second AcquireFile: %v", err)
	}
	fl2.Release()
}

func TestAcquireFile_Concurrent_Serialised(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(target, []byte("user content\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	const goroutines = 8
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results []int
	)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			fl, err := locks.AcquireFile(context.Background(), target, locks.FileLockOptions{
				Timeout:       5 * time.Second,
				RetryInterval: 10 * time.Millisecond,
			})
			if err != nil {
				t.Errorf("goroutine %d AcquireFile: %v", i, err)
				return
			}
			// Critical section — record ordering proof.
			mu.Lock()
			results = append(results, i)
			mu.Unlock()

			time.Sleep(5 * time.Millisecond)
			fl.Release()
		}()
	}
	wg.Wait()

	if len(results) != goroutines {
		t.Errorf("expected %d results, got %d", goroutines, len(results))
	}
}

func TestAcquireFile_Timeout(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "mcp.json")

	fl1, err := locks.AcquireFile(context.Background(), target, locks.FileLockOptions{})
	if err != nil {
		t.Fatalf("first AcquireFile: %v", err)
	}
	defer fl1.Release()

	_, err = locks.AcquireFile(context.Background(), target, locks.FileLockOptions{
		Timeout:       100 * time.Millisecond,
		RetryInterval: 10 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected ErrFileLockTimeout")
	}
}

func TestAcquireFile_RelativePath(t *testing.T) {
	t.Parallel()
	_, err := locks.AcquireFile(context.Background(), "relative/path.json", locks.FileLockOptions{})
	if err == nil {
		t.Fatal("expected error for relative path")
	}
}

func TestFileLock_Release_NilSafe(t *testing.T) {
	t.Parallel()
	var fl *locks.FileLock
	if err := fl.Release(); err != nil {
		t.Errorf("Release on nil: %v", err)
	}
}
