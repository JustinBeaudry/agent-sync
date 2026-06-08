package locks

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newRegistry(t *testing.T) (*FileLockRegistry, string) {
	t.Helper()
	root := openRoot(t)
	reg, err := NewFileLockRegistry(root)
	if err != nil {
		t.Fatalf("NewFileLockRegistry: %v", err)
	}
	return reg, root.Path()
}

func TestFileLock_HappyPath(t *testing.T) {
	t.Parallel()
	reg, ws := newRegistry(t)
	agents := filepath.Join(ws, "AGENTS.md")

	release, err := reg.Acquire(context.Background(), agents, "cursor", AcquireOpts{})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	// A hashed sidecar exists under filelocks/.
	entries, _ := os.ReadDir(reg.dir)
	if len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), ".lock") {
		t.Errorf("expected one .lock sidecar; got %v", entries)
	}
	if err := release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	// Release is idempotent.
	if err := release(); err != nil {
		t.Errorf("second release should be a no-op; got %v", err)
	}
}

func TestFileLock_SameFileSerializes(t *testing.T) {
	t.Parallel()
	reg, ws := newRegistry(t)
	agents := filepath.Join(ws, "AGENTS.md")

	var concurrent atomic.Int32
	var maxConcurrent atomic.Int32
	var wg sync.WaitGroup
	for _, holder := range []string{"cursor", "codex"} {
		wg.Add(1)
		go func(h string) {
			defer wg.Done()
			rel, err := reg.Acquire(context.Background(), agents, h, AcquireOpts{Timeout: 5 * time.Second})
			if err != nil {
				t.Errorf("Acquire(%s): %v", h, err)
				return
			}
			n := concurrent.Add(1)
			for {
				m := maxConcurrent.Load()
				if n <= m || maxConcurrent.CompareAndSwap(m, n) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			concurrent.Add(-1)
			_ = rel()
		}(holder)
	}
	wg.Wait()
	if maxConcurrent.Load() != 1 {
		t.Errorf("two adapters held the same file lock concurrently (max=%d); must serialize", maxConcurrent.Load())
	}
}

func TestFileLock_DifferentFilesDoNotBlock(t *testing.T) {
	t.Parallel()
	reg, ws := newRegistry(t)

	relA, err := reg.Acquire(context.Background(), filepath.Join(ws, "AGENTS.md"), "cursor", AcquireOpts{})
	if err != nil {
		t.Fatalf("Acquire AGENTS.md: %v", err)
	}
	defer func() { _ = relA() }()
	// A different file must acquire immediately even while AGENTS.md is held.
	relB, err := reg.Acquire(context.Background(), filepath.Join(ws, ".mcp.json"), "cursor", AcquireOpts{Timeout: 500 * time.Millisecond})
	if err != nil {
		t.Fatalf(".mcp.json should not block on AGENTS.md: %v", err)
	}
	_ = relB()
}

func TestFileLock_PathIdentityConverges(t *testing.T) {
	t.Parallel()
	reg, ws := newRegistry(t)
	abs := filepath.Join(ws, "AGENTS.md")

	rel, err := reg.Acquire(context.Background(), abs, "cursor", AcquireOpts{})
	if err != nil {
		t.Fatalf("Acquire abs: %v", err)
	}
	defer func() { _ = rel() }()

	// A different spelling (with a redundant ./ segment) of the same
	// file must hash to the same lock and therefore block.
	spelling := filepath.Join(ws, ".", "AGENTS.md")
	_, err = reg.Acquire(context.Background(), spelling, "codex", AcquireOpts{Timeout: 200 * time.Millisecond})
	if !errors.Is(err, ErrFileLockTimeout) {
		t.Fatalf("equivalent spelling should contend; err=%v want ErrFileLockTimeout", err)
	}
}

func TestFileLock_NonexistentLeafUnderSymlinkConverges(t *testing.T) {
	t.Parallel()
	reg, ws := newRegistry(t)

	// realdir/ exists; link/ -> realdir. The target file does NOT exist
	// yet (first emission). The two spellings must still converge.
	realdir := filepath.Join(ws, "realdir")
	if err := os.Mkdir(realdir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(realdir, filepath.Join(ws, "link")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	viaReal := filepath.Join(realdir, "AGENTS.md")    // does not exist yet
	viaLink := filepath.Join(ws, "link", "AGENTS.md") // same file via symlinked dir

	rel, err := reg.Acquire(context.Background(), viaReal, "cursor", AcquireOpts{})
	if err != nil {
		t.Fatalf("Acquire viaReal: %v", err)
	}
	defer func() { _ = rel() }()
	_, err = reg.Acquire(context.Background(), viaLink, "codex", AcquireOpts{Timeout: 200 * time.Millisecond})
	if !errors.Is(err, ErrFileLockTimeout) {
		t.Fatalf("nonexistent leaf under symlinked dir should converge with resolved spelling; err=%v want ErrFileLockTimeout", err)
	}
}

func TestFileLock_TimeoutNamesPathAndHolder(t *testing.T) {
	t.Parallel()
	reg, ws := newRegistry(t)
	agents := filepath.Join(ws, "AGENTS.md")

	rel, err := reg.Acquire(context.Background(), agents, "cursor", AcquireOpts{})
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	defer func() { _ = rel() }()

	_, err = reg.Acquire(context.Background(), agents, "codex", AcquireOpts{Timeout: 150 * time.Millisecond})
	if !errors.Is(err, ErrFileLockTimeout) {
		t.Fatalf("err=%v want ErrFileLockTimeout", err)
	}
	if !strings.Contains(err.Error(), "AGENTS.md") || !strings.Contains(err.Error(), "cursor") {
		t.Errorf("timeout error must name the path and holder; got %q", err.Error())
	}
}
