package contract

import (
	"embed"
	"fmt"
)

// SchemaFS embeds the per-method and per-op JSON Schemas. Exported so
// PR 3's conformance harness and the public adapterkit (also PR 3) can
// load schemas by relative path. Inside this package, the schema parity
// test is the only consumer.
//
//go:embed schema/*.json
var SchemaFS embed.FS

// LoadSchema returns the raw bytes for one embedded schema file.
func LoadSchema(name string) ([]byte, error) {
	data, err := SchemaFS.ReadFile("schema/" + name + ".json")
	if err != nil {
		return nil, fmt.Errorf("contract: load schema %q: %w", name, err)
	}
	return data, nil
}
