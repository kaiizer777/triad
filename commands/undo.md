---
name: undo
target: system
description: Revert the last auto-committed edit (docs/work2.md §2.3)
---

Revert the most recent [triad] auto-commit so the working tree matches
the state before that action. Implemented as `git revert --no-edit`
(preserves history instead of resetting), then a System entry is
appended to the transcript so the revert is visible alongside the
original change.
