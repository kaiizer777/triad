---
name: backend
section: backend
description: "Triad's agent loop, approval state machine, transcript persistence, subagent isolation, browser tools, slash-command loader, and skill funnel. Condensed Mini variant for repeat touches."
tier: mini
---

# Triad Backend (Agent Loop) — Mini Skill

You already have the full `backend` skill in this session (Main was
injected earlier). This is the condensed pointer version — for routine
work where you only need the file map and the invariants.

## Core flow (the only correct control flow)

```
Coder.Respond(transcript) → proposed_action
   └─ if proposed_action:
        Reviewer.Respond(transcript)
           ├─ approve → execute → append action_result → git commit
           └─ object  → append objection → Coder revises & re-proposes
   └─ if Coder signals done AND Reviewer confirms:
        session → idle
```

Single-process, single-transcript, three participants
(`{You, Coder, Reviewer}`) + implicit System entries.

## File map (where to look)

- `internal/loop/loop.go` — main approval state machine
  (`Propose` / `Review` / `Execute`). The canonical control flow.
- `internal/loop/orchestrator.go` — Orchestrator mode (subagent
  spawning). Same approval loop, short-lived Coder.
- `internal/loop/clarify_loop_test.go`, `learn_loop_test.go` —
  reference tests for loop extensions. Copy one as a starting
  skeleton for new loop features.
- `internal/agent/client.go` — OpenAI-compatible HTTP client
  (request builder, retry/backoff, response parser). **No
  hardcoded endpoints or model strings** — read from config.
- `internal/agent/config.go` — `AgentConfig`
  (Name, Model, SystemPrompt, Tools). Value-copy per turn to
  add the per-turn skills extension; never mutate the persistent
  config in place.
- `internal/agent/tools.go` — tool schemas (`write_file`,
  `read_file`, `run_command`, `browser_*`, `spawn_subagent`).
- `internal/transcript/transcript.go` — append-only JSONL
  `transcript.Transcript`. `Entry` is the only persisted shape
  (`ID`, `Speaker`, `Type`, `Content`, `Timestamp`).
- `internal/transcript/entry.go` — entry model + speaker/type
  constants.
- `internal/subagent/subagent.go` — isolated subagent engine:
  fresh context, restricted tools, turn cap, summary-only return.
  **Depth cap = 1.** A subagent may NOT spawn a subagent.
- `internal/subagent/subagent_tools.go` — tool subset a subagent
  may call.
- `internal/browser/chrome.go` — Playwright-Go manager
  (`browser_navigate`, `click`, `type`, `get_text`,
  `screenshot`). Lazy launch.
- `internal/commands/*.md` + `internal/commands/loader.go` —
  slash-command library (markdown + YAML frontmatter,
  `{{args}}` expansion). Drop a new `.md` in the dir to add a
  command; reload on session start.
- `internal/skills/loader.go` — Workflow 5 skill loader. Glob
  `skills/*.md`, parse frontmatter, expose Stage 1 (cheap
  section labels) + Stage 2 (full Main + Mini bodies).
  **Section:skill is strictly 1:1** — duplicate sections
  rejected at load.
- `internal/skills/funnel.go` — per-turn selection +
  Main-vs-Mini decision. `MaxSectionsPerTurn = 3` is a hard
  cap; the funnel truncates beyond 3, no exception.
- `internal/skills/tokens.go` — `EstimateTokens` (4-chars/token
  approximation) + `EstimateInjectedTokens` (body + delimiter
  overhead).
- `internal/loop/sessions/traces/default.jsonl` — append-only
  per-session trace log. New EventType? Add the constant in
  `internal/tracelog/` first, then write via `tracelog.Append`.
  Never write to this path directly.
- `internal/gitcommit/` — automated `git add` + `git commit`
  per approved action. `/undo` is `git revert`.
  `GetSessionSummary` is the local commit-log-based report
  (no LLM call).
- `internal/tracelog/tracelog.go` — append-only structured
  trace log used by `/trace`. EventType is a string enum.

## Hard invariants — DO NOT break

1. **Reviewer has no tool access** (`HasTools: false` enforced
   at the client layer). New model role with tools = Coder,
   not Reviewer.
2. **Every proposed action goes through the approval loop.** No
   "trusted Coder" shortcut, no human-override skip, no
   "internal" action class. Extend the loop to represent the
   action's semantics; never bypass it.
3. **Model IDs, endpoints, API keys come from config.** Never
   inline `mimo-v2.5-free` or `https://opencode.ai/zen/v1` in
   a `.go` file. Read from `AgentConfig` passed in.
4. **Speaker attribution stays explicit** in any transcript
   content the loop writes directly: `[Coder]:`, `[Reviewer]:`,
   `[You]:`, `[System]:`. OpenAI `role` fields alone don't
   carry multi-agent identity in a shared transcript.
5. **JSONL transcript files are append-only.** No rewrite to
   "fix" an entry — append a correction entry that references
   the prior. The `transcript.Transcript` API has no rewrite
   method, by design.
6. **`tea.Cmd`-only concurrency in the TUI side.** All async
   work into the TUI dispatches via `tea.Cmd` and returns a
   single `tea.Msg`. No channel callbacks, no goroutines
   touching `Model`. (Restated in `frontend` skill.)
7. **No invented tool schemas.** Extend
   `internal/agent/tools.go` matching the existing pattern
   (OpenAI-style `type: function` + JSON schema params). Don't
   invent a parallel schema dialect — it complicates the
   Reviewer prompt and the test fixtures.
8. **Subagent depth is 1.** Enforced in `spawn_subagent`. If a
   task really needs recursion, that's a human-level decision
   to raise the cap.

## Adding a new tool — checklist

1. Define the schema in `internal/agent/tools.go` (JSON
   schema, one-line description, tool name).
2. Add the dispatcher case in the loop's
   `executeApprovedAction`.
3. Implement the executor in `internal/<pkg>/`. Subagent-
   allowed? Add to `internal/subagent/subagent_tools.go`.
4. Return structured `actionResult{OK, Output, Err}`. The
   loop appends it as `action_result` with `[Coder]:`
   speaker.
5. Test happy path AND at least one failure path
   (malformed args, missing file, timeout, network error).
   Phase 1 invariant: happy-path-only is not "done."
6. Add a `commands/<toolname>.md` stub if the human should
   be able to invoke it directly via `/foo`.

## Adding a new slash command

1. Create `commands/<name>.md` with frontmatter (`name`,
   `target: coder` or `target: reviewer`, `description`) +
   body using `{{args}}` for argument interpolation.
2. On next session start, the loader picks it up
   automatically. New command appears after restart (or
   `/restart` in-session).
3. Test by typing `/<name> <args>` in a session.

## Adding a new skill (meta)

1. `/skill add <name>` to scaffold, or hand-write
   `skills/<name>.md` + `skills/<name>-mini.md`.
2. Frontmatter must match `internal/skills/loader.go`:
   `name`, `section`, `description`, `tier` (`main` /
   `mini`), `mini_ref`, `token_budget_main`,
   `token_budget_mini`.
3. Section must be unique across all skill files. Short,
   single-word label.
4. Verify budget (Main 5–8k, Mini 2–4k) with a real
   tokenizer — see "Token budget" below.

## Token budget

`EstimateTokens` (4 chars/token) is for `/trace` display
only. Hard budget enforcement needs a real tokenizer
(Phase 5.4). Practical options:

- Build-time Go test with a real BPE encoder
  (e.g. `github.com/sugarme/tokenizer`, CL100k vocab) —
  fails CI on out-of-budget.
- Manual phase-gate: human runs a tokenizer script
  (Python `tiktoken`) and commits the count as a comment
  next to the frontmatter.

Until the human picks one, `token_budget_*` frontmatter
values are advisory.

## Common pitfalls (all hit at least once)

- Hardcoding a model ID or endpoint in a new file —
  always go through `AgentConfig` / config layer.
- Adding a new model role with tools when you meant
  "Reviewer with extra permissions" — that's still
  Reviewer; extend Reviewer's prompt, not the tool set.
- Free-tier rate-limit handling: backoff is in
  `internal/agent/client.go`. If you're tempted to add a
  `time.Sleep` retry in a new call site, route through
  the client instead.
- `/trace` cross-pollution: trace path is derived from
  the bound session. If a new call site writes to the
  default `default.jsonl` without a bound session, you'll
  pollute other sessions' traces.
- Forgetting to mark the loop's transcript as bound
  before writing a skill-selection trace — the
  `appendSkillSelectionTrace` no-ops in that case, which
  is correct, but it can mask a bug where you meant to
  bind a session.

## What this skill does NOT cover

- TUI rendering, keybindings, layout, status bar, sidebar
  → `frontend` skill.
- Persistent DB schema or migration → no DB; see `db`
  skill for the JSONL-as-database reality.
- Skill content authoring — see "Adding a new skill"
  above; that's a meta-skill, not a domain.
- Cross-agent routing in Orchestrator mode
  (`internal/loop/orchestrator.go`) — same approval loop,
  but the routing rubric (`internal/loop/rubric.go`) is
  its own concern; read that file directly when touching
  it.
