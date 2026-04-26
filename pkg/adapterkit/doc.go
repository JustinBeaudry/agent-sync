// Package adapterkit is the public Go SDK for aienvs adapters.
//
// It mirrors the frozen aienvs/v1 wire types and provides a small
// server runtime so adapter authors only implement initialize, emit,
// and shutdown behavior. The package intentionally does not depend on
// internal/ packages; conformance and the CLI runtime remain internal.
package adapterkit
