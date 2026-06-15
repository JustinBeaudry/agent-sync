# Code Review

Review the diff for correctness bugs, missing tests, and unclear naming.

IMPORTANT: this is an agent-sync canonical source file, NOT a finished
tool-native skill. Do NOT add native `name:` / `description:` frontmatter
here — agent-sync's decoder rejects unknown frontmatter fields. The skill's
name is taken from the directory (`code-review`), and each adapter generates
the tool-native frontmatter (e.g. Claude's `name:`/`description:`) on emit.

Only agent-sync frontmatter fields are recognized: `required`, `targets`,
`version` (all optional). See docs/spec/ir-v1.md for the full contract.
