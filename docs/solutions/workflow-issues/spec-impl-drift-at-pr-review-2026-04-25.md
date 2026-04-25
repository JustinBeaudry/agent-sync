---
title: Reconciling spec-vs-impl drift caught at PR review
date: 2026-04-25
module: internal/ir
problem_type: workflow_issue
component: development_workflow
severity: medium
related_components:
  - documentation
  - testing_framework
applies_when:
  - "Spec is authored before the implementation it describes"
  - "Two or more auto-reviewers flag the same line with conflicting proposed fixes"
  - "A contract detail (IDs, sort order, error scope) differs between docs/spec/*.md and the code that realizes it"
  - "You are tempted to fast-apply a reviewer-suggested one-line patch"
tags:
  - spec-impl-drift
  - pr-review
  - reconciliation
  - ir
  - decoder
  - reviewer-cadence
  - documentation
  - aienvs
---

# Reconciling spec-vs-impl drift caught at PR review

## Context

On PR #4 (Unit 7 — IR v1 decoder), `docs/spec/ir-v1.md` was authored in
commit `84024a0` before the decoder landed in `77a3ff1`
(`internal/ir/{types,kinds,decode}.go`). This is a reasonable v1 cadence:
write the contract, then build to it. Drift between the two was inevitable
because the spec made assumptions the impl could not satisfy, and neither
side was test-locked to the other.

Two concrete drifts surfaced at PR review, each flagged by multiple
auto-reviewer threads (per the *PR review cadence* repo learning, every
push runs gemini + copilot + codex):

1. **Node IDs for shared agent files.** Spec said
   AGENTS.md / CLAUDE.md / GEMINI.md "share the same node id."
   `buildAgentsMD` at `internal/ir/decode.go:280-330` derives the ID via
   `strings.ToLower(strings.TrimSuffix(path.Base(e.Path), path.Ext(e.Path)))`,
   yielding three distinct IDs (`agents`, `claude`, `gemini`).
2. **Node ordering.** Spec said "depth-first lexicographic"; `sortNodes`
   actually sorts by `(Kind, ID)`.

For drift #1, two reviewer threads suggested fixing the impl
(`PRRT_kwDOSIQ0HM59j11O`, `PRRT_kwDOSIQ0HM59j17m`: "set `id := \"agents\"`")
and two more suggested fixing the spec (j109, j17o). Same line, opposite
prescriptions.

The resolution that survived: **keep the impl, update the spec on both
counts.** Each `Node` carries a `Targets []string` field, and `markSeen`
enforces per-`Kind` ID uniqueness — collapsing AGENTS/CLAUDE/GEMINI into one
node-id with three Targets is unrepresentable in the chosen Node shape.
For ordering, `(Kind, ID)` is more useful for downstream consumers than a
filesystem-walk-order recreation. The spec was speculating; the impl had
invariants.

## Guidance

When spec and impl disagree on a contract detail and reviewers propose
fixes, **identify which side carries invariants before touching either
file.** Reviewer voting is information, not a verdict.

The reconciliation procedure:

1. **Name the disagreement precisely.** Quote the spec line and the impl
   line side-by-side. Don't paraphrase — reviewer comments often blur which
   side they're proposing to change, and two reviewers can use the same
   words for different fixes.
2. **Ask which side is enforced.** Look for type signatures, uniqueness
   checks, sort comparators, test assertions, downstream consumers. The
   side with mechanical enforcement is *load-bearing*; the side without is
   prose.
3. **Move the prose.** Update the unenforced side to match the enforced
   side, unless the enforced side is itself wrong on the merits (then
   change the impl *and* its tests, not just the spec).
4. **Resist the highest-vote reviewer fix when it would break invariants.**
   Two reviewers saying "fix the code" carries no more weight than two
   saying "fix the spec" — they're often reading different paragraphs.

Two specific failure modes to refuse:

- **Vote-counting**: applying whichever reviewer fix has the most threads
  attached. Auto-reviewers re-flag the same line on every push, so vote
  counts mostly reflect how many pushes happened, not which fix is right.
- **Spec-rubber-stamping**: updating the spec to match impl without
  checking whether the impl is actually doing the right thing. The spec
  exists to catch impl drift in the other direction too.

## Why This Matters

This repo runs gemini, copilot, and codex auto-reviewers on every push
(see `pr_review_cadence` memory). On a typical PR that means three
reviewers × 1–2 fix rounds, often re-flagging old commits. When two
reviewers point at the same line with different proposed fixes, the
signal is "this is a real ambiguity," not "implement the most popular
suggestion." Treating reviewer count as truth-value loses the actual
information — that the spec and code disagree on a load-bearing detail
and a human needs to pick the source of truth.

Equally, when a spec is documentation rather than a generated artifact,
nothing prevents drift between commits like `84024a0` (spec) and
`77a3ff1` (impl). The drift is detected only at PR review, and only
because three independent reviewers happened to read both files. That is
not a reliable defense.

The repo invariant in `AGENTS.md` is explicit: *"Silent drift from the
plan is a bug. If code disagrees with the plan, update the plan first
in the same PR, or stop and surface the disagreement."* This learning
operationalizes that rule for the spec/impl pair specifically.

## When to Apply

- Any spec-then-impl PR where reviewers flag a contract mismatch.
- Strong signal: 2+ reviewer threads at the same line proposing different
  fixes (especially when one says "fix code" and another says "fix spec").
- Especially urgent when the impl has type-level or runtime-enforced
  invariants (uniqueness checks, sort comparators, exhaustive switches)
  that the spec did not anticipate.
- Also applies in reverse: when reviewers all agree to "fix the spec to
  match the code," before doing so confirm the code is right on its
  merits, not just internally consistent.

## Examples

**Concrete case — node IDs (PR #4):**
- Spec (before): "AGENTS.md / CLAUDE.md / GEMINI.md share the same node id."
- Impl (`internal/ir/decode.go:280-330`): IDs derived from
  `strings.ToLower(strings.TrimSuffix(path.Base(e.Path), path.Ext(e.Path)))`.
- Invariant on impl side: `markSeen` enforces per-`Kind` uniqueness; each
  `Node` has `Targets []string`, so one node-id with three scopes is
  unrepresentable.
- Resolution: keep impl, rewrite spec section "Concept set" to describe
  three nodes with distinct IDs and per-node `Targets`. Shipped in
  commit `d4226c1`.

**Sibling case — node order (same PR):**
- Spec (before): "depth-first lexicographic."
- Impl: `sortNodes` orders by `(Kind, ID)`.
- Invariant on impl side: `(Kind, ID)` is what every downstream consumer
  iterates by; reproducing filesystem walk order would require carrying
  an extra field on `Node` for no consumer benefit.
- Resolution: keep impl, update spec to describe `(Kind, ID)` ordering.

**Repo precedent for the spec-as-lever pattern (Unit 2 manifest, session history):**
The same pattern showed up earlier on the manifest schema: `ce-review`
flagged speculative schema fields, and two `gated_auto` fixes were
deferred specifically because they "need a spec decision on Unit 2's
byte-for-byte preservation contract" (CRLF preservation and inline-comment
preservation). The lesson then was the same — when impl-vs-spec tension
surfaces, the spec is the lever to move. Unit 6's trust spec
(`docs/spec/trust-store-v1.md`) avoided this drift entirely by staying at
contract level (exit codes, error classes, JSONL schema) rather than
dictating ID-derivation rules. (session history)

**Counter-example (when to fix the impl instead):** if `markSeen` had no
uniqueness check and the impl was producing duplicate IDs only by
accident, the reviewer suggestion `id := "agents"` would have been
correct — but then the fix is impl + tests asserting the new shape, not
just a one-liner.

## Prevention

The drift in this PR was caught by humans-via-reviewers; it should be
caught by tests. Three patterns to add as the codebase grows:

1. **Spec-locked fixture test.** A `TestSpecLayoutMatchesImpl` that parses
   the canonical-layout fenced code block in `docs/spec/ir-v1.md`,
   extracts each declared filename pattern, and asserts each resolves to
   the correct `Kind` via `kindForExt`. The spec becomes a fixture; if it
   drifts from the impl, the test breaks the build, not the PR review.
2. **Don't `t.Skip()` invariant tests.** The Unit 7 decoder shipped with a
   `t.Skip()`'d duplicate-id test — the comment said "v1 layout's
   filename→id mapping makes natural duplicates impossible inside a
   single canonical repo." The skipped test would have made the per-`Kind`
   uniqueness invariant *visible* to anyone reading the spec. Skipped
   tests are how invariants become invisible. Either delete the test or
   write it against a synthetic case that actually exercises the
   invariant. (session history)
3. **Generate spec sections from impl.** For sections that describe
   enumerable behavior (Kinds, sort key, ID-derivation rules), generate
   the spec section from godoc + a sample-fixture run. The hand-written
   prose stays for rationale; the mechanical parts come from code.

v1 ships without these guardrails — the cost is acceptable now because
PR-review-as-spec-test is working — but Unit 8+ should add at least the
fixture test before the spec grows further.

## Related

- `docs/spec/ir-v1.md` — the spec under reconciliation (post-fix state).
- `internal/ir/decode.go:280-330` — `buildAgentsMD`, the ID-derivation
  source of truth.
- `internal/ir/decode.go:51` — `markSeen`, the per-`Kind` uniqueness
  invariant the spec failed to anticipate.
- `AGENTS.md` invariant: "Silent drift from the plan is a bug" — the
  repo-level rule this learning operationalizes for spec/impl pairs.
- `~/.claude/projects/-Users-justinbeaudry-Projects-aienvs/memory/pr_review_cadence.md`
  — auto-reviewer cadence note that surfaced this drift on round 1.
- PR #4: <https://github.com/JustinBeaudry/aienvs/pull/4>
- Resolved threads: copilot `PRRT_kwDOSIQ0HM59j11O` and gemini
  `PRRT_kwDOSIQ0HM59j17m` (suggested impl fix; rejected); copilot j109
  and gemini j17o (suggested spec fix; accepted).
- Fix commit: `d4226c1` (clusters spec edit, decoder error-handling fixes,
  and kinds.go validation tightening into one push).
