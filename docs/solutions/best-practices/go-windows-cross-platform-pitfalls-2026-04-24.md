---
title: Go cross-platform CI pitfalls — build tags, filepath.IsAbs, and path-separator comparisons
date: 2026-04-24
category: best-practices
module: internal/fsroot, internal/cache, internal/workspace
problem_type: best_practice
component: development_workflow
severity: high
applies_when:
  - Writing Go code that uses filepath, syscall, or path-comparison logic
  - Adding OS-specific test helpers or fixtures (FIFOs, symlinks, Unix sockets)
  - Asserting on string values returned by filepath.Join or filepath.Abs
  - Running CI on both Unix and Windows targets
tags:
  - go
  - windows
  - cross-platform
  - filepath
  - build-tags
  - syscall
  - ci
---

# Go cross-platform CI pitfalls — build tags, filepath.IsAbs, and path-separator comparisons

## Context

Cross-platform Go CI revealed three distinct compile-time and test-time failures when a Windows matrix was exercised against a repo that had only been validated on Linux/macOS. All three failures share a single underlying principle: the assumption that a runtime guard (`if runtime.GOOS == "windows" { t.Skip() }`) provides the same protection as a structural or build-time guarantee. It does not. Runtime checks execute at test time; build tags and path-normalization are enforced earlier — at compile time and at the type level, respectively. When these are confused, code that looks safe on Unix silently breaks the moment a different OS tries to compile or run it.

A precedent for the correct pattern already existed in this repo: Unit 1's `internal/fsroot` package split syscall-dependent code into `_unix.go` / `_windows.go` / `_other.go` files from the start (`samefs_unix.go` + `samefs_windows.go`, `nofollow_unix.go` + `nofollow_other.go`). That discipline was not applied to new tests in Units 3–4, and the gap only surfaced when the GitHub Actions matrix ran Windows for the first time. (session history)

## Guidance

### Rule 1: Use build tags for files that import platform-specific packages

`t.Skip` is a runtime operation. If a file unconditionally imports a package that does not exist on a target platform, the compiler rejects the entire file before any test logic runs. The skip is never reached.

**Before** — compiles fine on Linux, fails `go vet` on Windows:

```go
// internal/workspace/discover_test.go
import (
    "runtime"
    "syscall" // syscall.Mkfifo does not exist on Windows
)

func TestFind_ExplicitWorkspaceToFIFO(t *testing.T) {
    if runtime.GOOS == "windows" {
        t.Skip("FIFOs are not available on Windows") // never reached — file won't compile
    }
    if err := syscall.Mkfifo(fifoPath, 0o644); err != nil {
        t.Fatalf("mkfifo: %v", err)
    }
}
```

**After** — split into a build-tagged file, no runtime skip required:

```go
// internal/workspace/discover_unix_test.go
//go:build !windows

package workspace_test

import (
    "path/filepath"
    "syscall"
    "testing"
)

func TestFind_ExplicitWorkspaceToFIFO(t *testing.T) {
    tmp := t.TempDir()
    fifoPath := filepath.Join(tmp, workspace.ManifestName)
    if err := syscall.Mkfifo(fifoPath, 0o644); err != nil {
        t.Fatalf("mkfifo: %v", err)
    }
    // ... unchanged body
}
```

Remove the `syscall` import and the original function from the shared test file entirely. The build tag is the enforcement point; no runtime conditional is needed inside the tagged file.

**Convention:** prefer `//go:build !windows` over `//go:build linux || darwin` — it covers BSDs and other Unix-likes, and it matches the precedent set by `containment_unix_test.go` and `nofollow_unix.go` in this repo. (session history)

### Rule 2: `filepath.IsAbs` alone is not sufficient to validate relative paths

`filepath.IsAbs` is platform-aware in the wrong direction for security purposes. On Unix, `/etc/passwd` is absolute and `filepath.IsAbs` returns `true`. On Windows the same string returns `false` — Windows requires a drive letter (`C:\...`) for absolute — but a leading `/` still means "root of the current drive" and must not be accepted as a safe relative path.

**Before** — passes the Unix safety check but admits rooted paths on Windows:

```go
func ValidateRelPath(relPath string) error {
    if filepath.IsAbs(relPath) {
        return fmt.Errorf("%w: absolute path %q", ErrUnsafeRelPath, relPath)
    }
    return nil // "/etc/passwd" reaches here on Windows
}
```

**After** — add an explicit leading-separator check independent of `filepath.IsAbs`:

```go
func ValidateRelPath(relPath string) error {
    if filepath.IsAbs(relPath) {
        return fmt.Errorf("%w: absolute path %q", ErrUnsafeRelPath, relPath)
    }
    // Reject bare-rooted paths like "/etc/passwd" on Windows where
    // filepath.IsAbs returns false but the leading separator still
    // resolves to the root of the current drive.
    if strings.HasPrefix(relPath, "/") || strings.HasPrefix(relPath, `\`) {
        return fmt.Errorf("%w: rooted path %q", ErrUnsafeRelPath, relPath)
    }
    return nil
}
```

This makes the definition of "safe relative path" explicit and portable rather than delegating it to OS-specific semantics.

### Rule 3: Normalize separators before comparing path strings

`filepath.Join` produces OS-native separators. Path constants defined with forward slashes (the portable convention) will not match OS-joined paths on Windows when compared with `strings.HasSuffix` or `strings.Contains`.

**Before** — assertion passes on Linux, fails on Windows:

```go
const DirName = "aienvs/repos" // forward slash — fine on Unix

loc.Root = filepath.Join(override, DirName) // "C:\...\aienvs\repos" on Windows

// WRONG: "C:\...\aienvs\repos" does not have suffix "aienvs/repos"
if !strings.HasSuffix(loc.Root, cache.DirName) {
    t.Errorf("root %q missing DirName suffix", loc.Root)
}
```

**After** — normalize to forward slash before the string comparison:

```go
if !strings.HasSuffix(filepath.ToSlash(loc.Root), cache.DirName) {
    t.Errorf("root %q missing DirName suffix", loc.Root)
}
```

Alternatively, build the expected value with `filepath.Join` segments so both sides use OS separators consistently. The invariant: both operands of a path comparison must use the same separator convention.

This aligns with the repo's existing preference — use high-level stdlib path helpers (`filepath.ToSlash`, `filepath.FromSlash`, `filepath.Join`, `filepath.VolumeName`) rather than hand-rolled string manipulation with hardcoded `/` or `\`. (session history)

## Why This Matters

These failures share a deceptive quality: the code looks defensive. It has a runtime skip, it calls an OS-aware stdlib function, it uses a named constant. Each of those choices is locally reasonable on the platform where the code was written. The problem only manifests when a second platform is introduced, and by then the assumption is deeply embedded.

The deeper principle is that Go's portability guarantee covers syntax and most of the standard library, but not syscall surface area, not platform-specific path semantics, and not string-level representations of OS-joined paths. In all three cases, the fix moves enforcement to an earlier, structural level — the build system, an explicit invariant, or a normalization step — rather than relying on runtime branching to paper over a compile-time or semantic mismatch.

The one sentence that unifies all three: **runtime checks protect execution paths, but cannot substitute for build-tag isolation, explicit semantic invariants, or separator normalization.**

## When to Apply

- Any test file that imports `syscall`, `golang.org/x/sys/unix`, or any other package with known platform-specific availability. Split with `//go:build` rather than wrapping the body in a runtime check.
- Any validation function that must reject non-relative paths, especially in security-sensitive code (path traversal, safe-filesystem layers). Do not rely on `filepath.IsAbs` alone; also reject strings with leading `/` or `\`.
- Any test or assertion that compares an OS-joined path (produced by `filepath.Join`, `os.UserCacheDir`, etc.) against a string constant defined with forward slashes. Normalize with `filepath.ToSlash` before comparing, or keep both sides in the same convention.
- When adding a new CI matrix entry — particularly Windows. Run `go vet ./...` and `go build ./...` on Windows before declaring a build passing. `go vet` catches missing symbols that runtime skips miss entirely. Locally, `GOOS=windows GOARCH=amd64 go vet ./...` is cheap and catches Rule 1 violations before a remote CI round-trip.

## Examples

All three manifestations occurred in the same PR while exercising a Windows CI matrix for the first time:

| # | Location | Failure mode | Enforcement level | Fix |
|---|----------|--------------|-------------------|-----|
| 1 | `internal/workspace/discover_test.go` | `syscall.Mkfifo` compile error on `windows/amd64` | Compile-time | Split into `discover_unix_test.go` with `//go:build !windows` |
| 2 | `internal/fsroot/root.go` `ValidateRelPath` | `/etc/passwd` accepted as relative path on Windows | Semantic invariant | Add `strings.HasPrefix(relPath, "/")` check alongside `filepath.IsAbs` |
| 3 | `internal/cache/key_test.go` + `location.go` | `strings.HasSuffix` fails because `filepath.Join` produces `\` on Windows | Test assertion | Wrap `loc.Root` in `filepath.ToSlash` before suffix comparison |

### What didn't work (historical context)

- **Runtime `t.Skip` as a substitute for build tags on syscall-dependent tests.** `t.Skip` protects the test body, not the file's compile unit. `go vet` and `go build` on the target platform still resolve every imported symbol before any runtime branching happens. (session history)
- **Assuming `main` was already cross-platform-clean.** The fsroot test failures surfaced by this PR (`TestValidateRelPath/absolute-unix`, `TestStagedWrite_RejectsUnsafeRelPath//etc/passwd`) were present in Unit 1's code on `main` but had never been run against Windows because the GitHub Actions matrix was not triggered against `main` alone — only PRs activated it. A first-PR CI run therefore surfaces drift from every unit that preceded it. (session history)

### Considered alternatives for Rule 2

When Rule 2's fix was chosen, three options were weighed explicitly: (1) fix Windows now with a ~10-minute test-and-source change, (2) merge with failing Windows CI via `--admin` bypass and open a follow-up, (3) `t.Skip` the Windows test with a TODO. Option 1 was chosen with the rationale "land cross-platform test parity before Phase B" — the principle being that structural portability is cheaper to establish when the code is still fresh than to retrofit later. (session history)

## Related

- No prior `docs/solutions/` documentation exists in this repo yet — this is the first learning doc.
- Precedent files demonstrating the correct build-tag pattern: `internal/fsroot/samefs_unix.go`, `internal/fsroot/samefs_windows.go`, `internal/fsroot/nofollow_unix.go`, `internal/fsroot/nofollow_other.go`, `internal/fsroot/containment_unix_test.go`.
- Fixes landed in: commits `ee07b06` (build-tag extraction), `93df9cb` (gofmt), `d18579b` (ValidateRelPath + ToSlash) on branch `feat/manifest-workspace-cache`.
