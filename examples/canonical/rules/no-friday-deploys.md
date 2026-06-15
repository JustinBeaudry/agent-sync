---
# agent-sync frontmatter (all fields optional):
#   required: fail the sync if a target adapter can't support this node
#   targets:  restrict to specific adapters (omit = all targets)
#   version:  author-controlled counter
targets: [claude, cursor]
version: 1
---

Never deploy on Fridays or the day before a holiday.

The body is the rule content. The rule's id comes from the filename
(`no-friday-deploys`), not from any frontmatter field.
