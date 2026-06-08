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

	"github.com/aienvs/aienvs/internal/fsroot"
)

const machineIDRel = ".aienv/state/machine-id"

// machineID reads or creates the stable per-machine identity used to
// gate stale-lock reconciliation. It is deliberately NOT os.Hostname()
// — hostnames collide across containers, cloned VM images, and default
// "localhost" on a shared filesystem, which would let one machine's
// reconcile decision misclassify another machine's live lock. The id
// is a random 128-bit hex string created once and persisted.
//
// Creation uses O_CREATE|O_EXCL so a first-sync race between two
// processes on the same machine resolves to a single id: the loser of
// the create re-reads the winner's file.
func machineID(root *fsroot.Root) (string, error) {
	if id, err := readMachineID(root); err == nil && id != "" {
		return id, nil
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}

	if err := root.Inner().MkdirAll(stateDirRel, 0o755); err != nil {
		return "", fmt.Errorf("locks: mkdir %s: %w", stateDirRel, err)
	}

	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("locks: generate machine-id: %w", err)
	}
	id := hex.EncodeToString(buf[:])

	f, err := root.Inner().OpenFile(machineIDRel, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			// Lost the create race; the winner's id is authoritative.
			if existing, rerr := readMachineID(root); rerr == nil && existing != "" {
				return existing, nil
			}
		}
		return "", fmt.Errorf("locks: create machine-id: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(id + "\n"); err != nil {
		return "", fmt.Errorf("locks: write machine-id: %w", err)
	}
	return id, nil
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
