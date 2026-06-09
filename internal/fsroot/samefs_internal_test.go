//go:build unix

package fsroot

import (
	"errors"
	"syscall"
	"testing"
)

func TestIsCrossDevice(t *testing.T) {
	if !isCrossDevice(syscall.EXDEV) {
		t.Fatal("EXDEV should be reported as cross-device")
	}
	if isCrossDevice(syscall.ENOENT) {
		t.Fatal("ENOENT is not a cross-device signal")
	}
	if isCrossDevice(errors.New("plain error")) {
		t.Fatal("a non-errno error is not cross-device")
	}
}

func TestSameFilesystem_SameDir(t *testing.T) {
	dir := t.TempDir()
	same, err := SameFilesystem(dir, dir)
	if err != nil {
		t.Fatalf("SameFilesystem: %v", err)
	}
	if !same {
		t.Fatal("a directory must be on the same filesystem as itself")
	}

	// A missing path surfaces a stat error rather than a bogus bool.
	if _, err := SameFilesystem(dir, dir+"/does-not-exist"); err == nil {
		t.Fatal("expected stat error for missing path")
	}
}
