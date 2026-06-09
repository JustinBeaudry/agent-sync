# Reference Echo Adapter

`conformance/echo/` is the canonical Go example for an `aienvs/v1`
adapter built with [`pkg/adapterkit`](../../pkg/adapterkit/).

What it demonstrates:

- constructing an `adapterkit.Server`
- declaring capabilities for every v1 IR kind
- declaring one owned output subtree (`.echo/`)
- translating incoming IR nodes into declarative adapter ops
- running the adapter over stdio with the protocol cookie handshake

Use it as a starting point:

1. Copy this directory into your adapter repository.
2. Replace the server name/version in `main.go`.
3. Change the declared outputs to the paths your adapter owns.
4. Replace `handleEmit` with your real IR-to-op translation logic.
5. Run `agent-sync adapter conformance-test ./your-binary --format=json` in CI.

The reference adapter never writes workspace files directly. It only
returns protocol ops; the `agent-sync` runtime owns applying them.
