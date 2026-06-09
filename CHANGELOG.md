# Changelog

All notable changes to `agent-sync` are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

While the project is pre-1.0, minor version bumps may include breaking changes;
the adapter wire protocol follows its own "freeze the frame, grow capabilities"
compatibility policy documented in `docs/spec/adapter-protocol-v1.md`.

## [Unreleased]

### Added

- Apache-2.0 `LICENSE` and `NOTICE` files; license declared in `README`.
- `docs/threat-model.md` — supply-chain and filesystem-safety threat model.
- `.goreleaser.yaml` and `.github/workflows/release.yml` — reproducible
  multi-platform release packaging (darwin/amd64, darwin/arm64, linux/amd64,
  linux/arm64, windows/amd64) with SHA-256 checksums and optional minisign
  signatures.
- CI coverage gate: total statement coverage is enforced against the 80% floor
  mandated by CLAUDE.md and fails the build on a drop. Added behavioral tests
  for previously-untested packages (`internal/validate`, `conformance/echo`,
  `cmd/agent-sync`) plus the adapter op round-trip, watch sync, trust store,
  and conformance assertion paths to clear it.
- Fixed a nil-pointer panic in `adapterkit.ExitError.Error()` (the `e == nil`
  guard dereferenced `e.Code` in the same branch), surfaced by the new tests.
- CI now fails loud if the real-git/real-filesystem end-to-end tests are
  skipped, via `AGENT_SYNC_REQUIRE_GIT=1`.

### Changed

- `README` tool-support table now distinguishes bundled adapters (Claude Code,
  Cursor) from planned ones, instead of advertising unimplemented tiers.

### Removed

- Roo Code from the planned tool list (decommissioned upstream).
