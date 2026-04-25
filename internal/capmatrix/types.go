// Package capmatrix owns the capability-status types used by the adapter
// framework. Each adapter declares a per-Kind support level via
// `capabilities.yaml` (Unit 8); the framework merges those declarations
// into a workspace-wide matrix at sync time.
//
// This package is intentionally tiny in v1: the type, validation, and a
// small merge helper. It does not import internal/ir to avoid an import
// cycle — adapters import both packages.
package capmatrix

import "fmt"

// CapabilityStatus is the per-Kind support level an adapter declares.
type CapabilityStatus string

const (
	// Supported: adapter fully implements this Kind.
	Supported CapabilityStatus = "supported"

	// Partial: adapter implements the Kind but with documented limits
	// (e.g., only some frontmatter fields). Required nodes mapped to
	// Partial succeed but emit a `warning` op.
	Partial CapabilityStatus = "partial"

	// Unsupported: adapter does not implement this Kind. Required nodes
	// mapped to Unsupported cause sync to fail with required_unmet.
	Unsupported CapabilityStatus = "unsupported"
)

// AllStatuses returns the v1 closed set in stable order.
func AllStatuses() []CapabilityStatus {
	return []CapabilityStatus{Supported, Partial, Unsupported}
}

// Validate reports nil when s is one of the documented statuses.
//
// Capability strings outside this set are decode errors at adapter-load
// time (Unit 8 wires this).
func Validate(s CapabilityStatus) error {
	switch s {
	case Supported, Partial, Unsupported:
		return nil
	default:
		return fmt.Errorf("capmatrix: unrecognized status %q (want supported|partial|unsupported)", string(s))
	}
}

// Compare returns -1, 0, +1 for a strict-to-loose ordering used when the
// framework needs the "weakest" support level across multiple adapters
// targeting the same node:
//
//	Unsupported (-1) < Partial (0) < Supported (+1)
//
// Higher value = stronger support. Use Min(a, b) to find the limiting
// adapter.
func Compare(a, b CapabilityStatus) int {
	return rank(a) - rank(b)
}

// Min returns the weaker of a and b. Used to compute the effective support
// level for a node when multiple adapters are targeted.
func Min(a, b CapabilityStatus) CapabilityStatus {
	if Compare(a, b) <= 0 {
		return a
	}
	return b
}

// rank maps a status to its sort key. Unknown statuses sort below
// Unsupported so they fail required-checks the same way.
func rank(s CapabilityStatus) int {
	switch s {
	case Supported:
		return 2
	case Partial:
		return 1
	case Unsupported:
		return 0
	default:
		return -1
	}
}
