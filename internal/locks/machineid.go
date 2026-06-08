package locks

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/aienvs/aienvs/internal/fsroot"
)

const machineIDRel = ".aienv/state/machine-id"

// machineIDAttempts bounds the create/re-read retry loop that resolves
// the first-sync create race and a crash-poisoned empty file.
const machineIDAttempts = 6

// machineID reads or creates the stable per-machine identity used to
// gate stale-lock reconciliation. It is deliberately NOT os.Hostname()
// — hostnames collide across containers, cloned VM images, and default
// "localhost" on a shared filesystem. The id is a random 128-bit hex
// string created once and persisted.
//
// Creation is robust against two races the naive O_EXCL approach hits:
//   - First-sync create race: a loser of the O_EXCL create re-reads
//     the winner's file, retrying through the brief window where the
//     winner has created the (still-empty) file but not yet written it.
//   - Crash-poisoned empty file: a process killed between create and
//     write leaves a zero-byte file that O_EXCL would block forever.
//     A persistently-empty file is removed and recreated.
//
// All file ops route through the fsroot Root (os.Root, which refuses
// symlink traversal), so the machine-id write is containment-safe.
func machineID(root *fsroot.Root) (string, error) {
	if err := root.Inner().MkdirAll(stateDirRel, 0o755); err != nil {
		return "", fmt.Errorf("locks: mkdir %s: %w", stateDirRel, err)
	}

	for attempt := 0; attempt < machineIDAttempts; attempt++ {
		id, err := readMachineID(root)
		if err == nil && id != "" {
			return id, nil
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		// File is absent, or present-but-empty (a crash-poisoned file).
		// Remove a poisoned empty file so the exclusive create below can
		// recreate it.
		if err == nil && id == "" {
			_ = root.Inner().Remove(machineIDRel)
		}

		newID, gerr := generateMachineID()
		if gerr != nil {
			return "", gerr
		}
		f, oerr := root.Inner().OpenFile(machineIDRel, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if oerr != nil {
			if errors.Is(oerr, fs.ErrExist) {
				// Lost the create race; brief backoff, then loop to
				// re-read the winner's (now hopefully written) file.
				time.Sleep(time.Duration(attempt+1) * 5 * time.Millisecond)
				continue
			}
			return "", fmt.Errorf("locks: create machine-id: %w", oerr)
		}
		writeErr := writeAndSync(f, newID)
		if writeErr != nil {
			// Do not leave a poisoned partial file behind.
			_ = root.Inner().Remove(machineIDRel)
			return "", writeErr
		}
		return newID, nil
	}
	return "", errors.New("locks: could not establish machine-id after retries")
}

func writeAndSync(f *os.File, id string) error {
	if _, err := f.WriteString(id + "\n"); err != nil {
		_ = f.Close()
		return fmt.Errorf("locks: write machine-id: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("locks: fsync machine-id: %w", err)
	}
	return f.Close()
}

func generateMachineID() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("locks: generate machine-id: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}

func readMachineID(root *fsroot.Root) (string, error) {
	f, err := root.Inner().Open(machineIDRel)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(f)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}
