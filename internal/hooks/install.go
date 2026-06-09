// Package hooks installs and removes git hook wrappers that run
// `agent-sync sync --post-merge` after pulls/checkouts. Wrappers are POSIX
// `#!/bin/sh` scripts with absolute paths baked in (so GUI git clients
// with a minimal PATH still work) and a marker comment so uninstall only
// ever removes aienvs-generated hooks.
package hooks

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// marker identifies an aienvs-generated hook wrapper. Uninstall and
// reinstall key off this exact line.
const marker = "# aienvs-managed-hook v1"

// ManagedHooks are the git hooks agent-sync installs.
var ManagedHooks = []string{"post-merge", "post-checkout"}

// Sentinel errors callers branch on.
var (
	// ErrForeignHook is returned when a non-agent-sync hook already exists and
	// neither --replace nor --append was requested.
	ErrForeignHook = errors.New("hooks: a non-agent-sync hook already exists; use --replace or --append")
	// ErrNotGitRepo is returned when the target has no .git directory.
	ErrNotGitRepo = errors.New("hooks: not a git repository (no .git directory)")
)

// Options configures installation.
type Options struct {
	// AienvsPath is the absolute path to the agent-sync binary baked into the
	// wrapper. Required.
	AienvsPath string
	// WorkspacePath is the absolute workspace path passed to sync. Required.
	WorkspacePath string
	// Replace backs up and overwrites an existing foreign hook.
	Replace bool
	// Append wraps an existing foreign hook (runs it, then agent-sync).
	Append bool
}

// Result reports what installation did, per hook.
type Result struct {
	Installed []string // hook names written
	BackedUp  []string // foreign hooks backed up (paths)
	Advisory  []string // advisory notes (e.g. lefthook/husky detected)
}

// hooksDir returns the git hooks directory for repoRoot, honoring an
// explicit core.hooksPath when set, else <gitdir>/hooks. It resolves the
// real git dir from the .git entry, which is a directory in a normal
// clone but a `gitdir: <path>` pointer FILE in a worktree or submodule.
func hooksDir(repoRoot string) (string, error) {
	gitDir, err := resolveGitDir(repoRoot)
	if err != nil {
		return "", err
	}
	// core.hooksPath detection is intentionally minimal: read the git
	// config for a hooksPath entry. Absent → default <gitdir>/hooks.
	if hp := readHooksPath(filepath.Join(gitDir, "config")); hp != "" {
		if filepath.IsAbs(hp) {
			return hp, nil
		}
		return filepath.Join(repoRoot, hp), nil
	}
	return filepath.Join(gitDir, "hooks"), nil
}

// resolveGitDir returns the real git directory for repoRoot. .git is a
// directory in a normal clone, or a file containing `gitdir: <path>` in a
// worktree/submodule.
func resolveGitDir(repoRoot string) (string, error) {
	dotGit := filepath.Join(repoRoot, ".git")
	info, err := os.Stat(dotGit)
	if err != nil {
		return "", ErrNotGitRepo
	}
	if info.IsDir() {
		return dotGit, nil
	}
	// .git is a file: read the `gitdir: <path>` pointer.
	data, rerr := os.ReadFile(dotGit) //nolint:gosec // repo's own .git pointer file
	if rerr != nil {
		return "", ErrNotGitRepo
	}
	line := strings.TrimSpace(string(data))
	target, ok := strings.CutPrefix(line, "gitdir:")
	if !ok {
		return "", ErrNotGitRepo
	}
	target = strings.TrimSpace(target)
	if !filepath.IsAbs(target) {
		target = filepath.Join(repoRoot, target)
	}
	return target, nil
}

// Install writes the managed hook wrappers into repoRoot's hooks dir.
func Install(repoRoot string, opts Options) (Result, error) {
	if opts.AienvsPath == "" || opts.WorkspacePath == "" {
		return Result{}, errors.New("hooks: AienvsPath and WorkspacePath are required")
	}
	if opts.Replace && opts.Append {
		return Result{}, errors.New("hooks: --replace and --append are mutually exclusive")
	}
	dir, err := hooksDir(repoRoot)
	if err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return Result{}, fmt.Errorf("hooks: create hooks dir: %w", err)
	}

	var res Result
	if adv := detectHookManagers(repoRoot); adv != "" {
		res.Advisory = append(res.Advisory, adv)
	}

	for _, name := range ManagedHooks {
		path := filepath.Join(dir, name)
		existing, readErr := os.ReadFile(path) //nolint:gosec // path is the repo's own hooks dir
		switch {
		case readErr == nil && isAienvsHook(existing):
			// Our own hook — overwrite cleanly.
		case readErr == nil && len(existing) > 0:
			// Foreign hook present.
			switch {
			case opts.Replace:
				backup := path + ".aienvs-backup"
				//nolint:gosec // G703: backup path derives from a constant hook name (ManagedHooks) under the repo's own hooks dir
				if werr := os.WriteFile(backup, existing, 0o600); werr != nil {
					return res, fmt.Errorf("hooks: back up %s: %w", name, werr)
				}
				res.BackedUp = append(res.BackedUp, backup)
			case opts.Append:
				// Preserve the predecessor as a separate executable sidecar
				// the wrapper invokes as a subprocess, so the predecessor's
				// own `exit` (common in hooks) cannot skip the agent-sync step.
				if werr := writeAppendWrapper(path, existing, opts); werr != nil {
					return res, werr
				}
				res.Installed = append(res.Installed, name)
				continue
			default:
				return res, fmt.Errorf("%w: %s", ErrForeignHook, path)
			}
		}
		if werr := os.WriteFile(path, []byte(wrapperScript(opts)), 0o755); werr != nil { //nolint:gosec // hooks must be executable
			return res, fmt.Errorf("hooks: write %s: %w", name, werr)
		}
		res.Installed = append(res.Installed, name)
	}
	return res, nil
}

// wrapperScript returns the POSIX wrapper. Absolute paths avoid PATH
// surprises in GUI git clients.
func wrapperScript(opts Options) string {
	return strings.Join([]string{
		"#!/bin/sh",
		marker,
		"set -eu",
		fmt.Sprintf("exec %q sync --post-merge --workspace %q", opts.AienvsPath, opts.WorkspacePath),
		"",
	}, "\n")
}

// predecessorSuffix names the sidecar that holds an --append predecessor.
const predecessorSuffix = ".aienvs-predecessor"

// writeAppendWrapper preserves the foreign predecessor as an executable
// sidecar (<hook>.aienvs-predecessor) and writes a wrapper that runs it as
// a SUBPROCESS before agent-sync. Running it as a subprocess (not inlining)
// means the predecessor's own `exit N` ends only that subprocess — the
// wrapper inspects its status and still runs agent-sync. A non-zero
// predecessor status aborts the hook with that status (preserving the
// predecessor's veto semantics, e.g. a pre-commit-style guard).
func writeAppendWrapper(path string, predecessor []byte, opts Options) error {
	sidecar := path + predecessorSuffix
	if werr := os.WriteFile(sidecar, predecessor, 0o755); werr != nil { //nolint:gosec // hooks must be executable
		return fmt.Errorf("hooks: write predecessor sidecar: %w", werr)
	}
	body := strings.Join([]string{
		"#!/bin/sh",
		marker,
		"set -eu",
		"# Run the preserved predecessor hook as a subprocess so its exit",
		"# does not skip agent-sync; a non-zero status still vetoes the hook.",
		fmt.Sprintf("if [ -x %q ]; then %q \"$@\" || exit $?; fi", sidecar, sidecar),
		fmt.Sprintf("exec %q sync --post-merge --workspace %q", opts.AienvsPath, opts.WorkspacePath),
		"",
	}, "\n")
	return os.WriteFile(path, []byte(body), 0o755) //nolint:gosec // hooks must be executable
}

func isAienvsHook(content []byte) bool {
	return strings.Contains(string(content), marker)
}

// detectHookManagers returns an advisory string when lefthook/husky is
// detected, since those tools also manage git hooks.
func detectHookManagers(repoRoot string) string {
	if fileExists(filepath.Join(repoRoot, "lefthook.yml")) || fileExists(filepath.Join(repoRoot, "lefthook.yaml")) {
		return "lefthook detected: agent-sync installed standalone git hooks; consider adding `agent-sync sync --post-merge` to lefthook instead to avoid conflicts."
	}
	if hasHusky(repoRoot) {
		return "husky detected: agent-sync installed standalone git hooks; consider integrating via husky to avoid conflicts."
	}
	return ""
}

func hasHusky(repoRoot string) bool {
	if fileExists(filepath.Join(repoRoot, ".husky")) {
		return true
	}
	pkg, err := os.ReadFile(filepath.Join(repoRoot, "package.json"))
	return err == nil && strings.Contains(string(pkg), "\"husky\"")
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// readHooksPath extracts a hooksPath value from a git config file. Minimal
// INI scan; returns "" when absent.
func readHooksPath(configPath string) string {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		// Git config keys are case-insensitive (hooksPath, hookspath, ...).
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		if strings.EqualFold(key, "hooksPath") {
			return strings.TrimSpace(line[eq+1:])
		}
	}
	return ""
}
