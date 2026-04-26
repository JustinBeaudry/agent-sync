package contract

import "embed"

// SchemaFS embeds the per-method and per-op JSON Schemas. Exported so
// PR 3's conformance harness and the public adapterkit (also PR 3) can
// load schemas by relative path. Inside this package, the schema parity
// test is the only consumer.
//
//go:embed schema/*.json
var SchemaFS embed.FS
