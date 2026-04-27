// Package conformance owns the frozen adapter-protocol corpus and the
// harness that runs a real adapter binary against it.
//
// The package exists to enforce the v1 adapter contract as executable
// data: each corpus case captures the IR input, the minimum declared
// capabilities/outputs expected from initialize, and the expected emit
// result or runtime error classification. Third-party adapters and the
// reference adapters in this repository both exercise the same corpus,
// which keeps the protocol spec and the runtime aligned.
package conformance
