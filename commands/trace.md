---
name: trace
target: system
description: Render chronological trace log across all agents and modes
---
Show session trace log

For each Coder turn, the trace also shows the Workflow 5 skill selection
that fired for that turn: the user task that triggered it, the section(s)
Coder picked, the tier (main vs mini) actually injected per section, the
per-section and total token cost, and a [cap-truncated] tag if the
3-section cap dropped any of Coder's picks. This is the Phase 4
observability surface for "why did Coder do something X-flavored when I
asked for Y" — read it before guessing at Coder's behavior.
