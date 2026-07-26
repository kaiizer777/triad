---
name: backend
section: backend
description: "Triad's agent loop, approval state machine, transcript persistence, subagent isolation, browser tools, and slash-command loader. Pick this for any task touching internal/loop/*, internal/agent/*, internal/transcript/*, internal/subagent/*, internal/browser/*, internal/commands/*, internal/skills/*, or the approval-flow core of internal/tui/cmd.go."
tier: main
mini_ref: backend-mini.md
token_budget_main: 7000
token_budget_mini: 3000
---

# Triad Backend (Agent Loop) — Main Skill

You are working on the **agent runtime of Triad itself**: the Coder /
Reviewer turn loop, the propose-review-execute state machine, the
append-only JSONL transcript, subagent isolation, browser automation,
slash-command loading, and the skill-injection funnel. This skill is the
"how Triad actually works" reference for any task that touches
non-rendering behavior.

If the task is *only* visible TUI behavior (keybindings, layout,
sidebar, status bar, command palette rendering), prefer the `frontend`
skill alone. The moment the work touches the loop, the transcript, the
agent client, or a tool, this skill is in scope — possibly alongside
`frontend` if the change also has a visible TUI surface.

## Core architecture (memorize this)

Triad is a **single-process, single-transcript, three-participant**
system. The participant set is exactly `{You, Coder, Reviewer}`, plus
implicit System entries written by the loop for skill-selection
records, git-commit receipts, trace events, and so on.

The state machine lives in `internal/loop/loop.go` and is the heart of
the project. Every Coder proposal goes through:

```
Coder.Respond(transcript) → proposed_action
   └─ if proposed_action:
        Reviewer.Respond(transcript)
           ├─ approve → execute action → append action_result → git commit
           └─ object  → append objection → Coder must revise and re-propose
   └─ if Coder signals done AND Reviewer confirms:
        session → idle
```

This is the only correct flow. There is no "skip Reviewer for trusted
actions" path. The TUI may *display* what's happening, but it cannot
*invoke* a Coder-side tool without Reviewer approval. If a feature
proposal would bypass Reviewer, that is itself a design violation —
flag it in your plan, don't paper over it.

## File map

- `internal/loop/loop.go` — the main approval state machine. The
  `Propose` / `Review` / `Execute` triple is the canonical control
  flow. New turn types (e.g. clarify, learn, journey) extend it
  without replacing it.
- `internal/loop/orchestrator.go` — Orchestrator mode, which spawns
  coding subagents. Same approval loop, but the Coder is a
  short-lived subagent instead of the long-running main Coder.
- `internal/loop/clarify_loop_test.go` and `learn_loop_test.go` —
  reference tests for the loop's two extensions. New loop
  extensions: copy one of these as a starting skeleton.
- `internal/agent/client.go` — OpenAI-compatible HTTP client. The
  request builder, response parser, retry/backoff, and tool-call
  schema negotiation all live here. **Never hardcode an endpoint or
  model string in this file** — those come from `config.yaml` /
  env vars.
- `internal/agent/config.go` — `AgentConfig` (Name, Model, SystemPrompt,
  Tools). Per-agent config. A `*AgentConfig` is value-copied per turn
  to add the per-turn skills extension (`skills.BuildCoderSystemPromptExtension`)
  — never mutate the persistent config in place.
- `internal/agent/tools.go` — tool schemas (`write_file`, `read_file`,
  `run_command`, `browser_*`, `spawn_subagent`). All tool execution
  goes through the approval loop; this file only defines the JSON
  schemas the model emits.
- `internal/transcript/transcript.go` — append-only JSONL
  `transcript.Transcript` type. The `Entry` struct is the only
  persisted shape (`ID`, `Speaker`, `Type`, `Content`, `Timestamp`).
  No other on-disk format for transcript data — don't introduce one.
- `internal/transcript/entry.go` — entry model + speaker/type
  constants. New entry type? Add the constant here, append from
  the loop.
- `internal/subagent/subagent.go` — isolated subagent engine: fresh
  context, restricted tool set, turn cap, summary-only return. Depth
  cap is 1 (a subagent cannot spawn a nested subagent — enforced in
  the spawn tool).
- `internal/subagent/subagent_tools.go` — the tool subset a
  subagent may call. The default restriction is the Coder tool set
  minus anything that can recursively spawn.
- `internal/browser/chrome.go` — Playwright-Go browser manager
  (`browser_navigate`, `click`, `type`, `get_text`, `screenshot`).
  Lazy browser launch; on-demand reuse.
- `internal/commands/*.md` — slash-command library (markdown +
  YAML frontmatter, `{{args}}` expansion). Loader at
  `internal/commands/loader.go`. Add a new command by dropping a
  `.md` file in this directory; restart not needed.
- `internal/skills/loader.go` — Workflow 5 skill loader. Glob
  `skills/*.md`, parse frontmatter, expose Stage 1 (cheap section
  labels) and Stage 2 (full Main + Mini bodies). **Section:skill
  is strictly 1:1** — duplicate sections are rejected at load
  time, not silently picked.
- `internal/skills/funnel.go` — the per-turn selection +
  Main-vs-Mini decision. `MaxSectionsPerTurn = 3` is a hard cap;
  the funnel truncates beyond 3 with no exception.
- `internal/skills/tokens.go` — `EstimateTokens` (4-chars-per-token
  approximation) + `EstimateInjectedTokens` (body + delimiter
  overhead). The actual BPE count for budget enforcement lives
  here or upstream — see "Token budget" below.
- `internal/loop/sessions/traces/default.jsonl` — the append-only
  per-session trace log. **New EventType for the trace? Add the
  constant in `internal/tracelog/` first**, then write through
  `tracelog.Append`. Never write to this path directly.
- `internal/gitcommit/` — automated `git add` + `git commit` per
  approved action. `/undo` is a `git revert`. Session-level
  `/summary` reads from the local commit log, not from the
  transcript (per `internal/gitcommit.GetSessionSummary`).
- `internal/tracelog/tracelog.go` — append-only structured trace
  log used by `/trace` (Workflow 5 Phase 4). EventType is a
  string enum — extend it as needed but never rewrite a JSONL
  line.

## Hard invariants — never violate

1. **Reviewer has no tool access.** `HasTools: false` on the
   Reviewer `AgentConfig` is enforced at the client layer. If
   you're adding a new model role and tempted to give it tools,
   ask: is this really the Reviewer under a new name, or is it
   Coder? If the latter, it's Coder and stays under approval.
2. **Every proposed action goes through the approval loop.** No
   "trusted Coder" shortcut, no "human override" skip, no
   "internal" action class that bypasses Reviewer. If the
   existing loop's review step can't represent the action's
   semantics, *extend the loop*, don't bypass it.
3. **Model IDs, endpoints, API keys come from config.** Never
   inline `mimo-v2.5-free` or `https://opencode.ai/zen/v1` in a
   `.go` file. The config layer is
   `internal/agent/config.go` + `config.yaml` + env vars. If a
   new call site needs the model, it reads from the
   `AgentConfig` passed in.
4. **Speaker attribution stays explicit.** When writing
   transcript content directly (not via a helper), use
   `[Coder]:` / `[Reviewer]:` / `[You]:` / `[System]:`
   prefixes. OpenAI `role` fields alone don't carry
   multi-agent identity in a shared transcript.
5. **JSONL transcript files are append-only.** Never rewrite a
   session file to "fix" an entry. Append a new entry that
   references the prior (e.g. `[System]: correcting entry #N
   — …`). The `transcript.Transcript` API does not expose a
   rewrite method, by design.
6. **`tea.Cmd`-only concurrency in the TUI side.** All async
   work dispatches via `tea.Cmd` and returns a single
   `tea.Msg`. The loop side runs in its own goroutines, but
   anything that flows into the TUI must be a `Msg`, not a
   channel callback. (See `frontend` skill for the TUI-side
   restatement.)
7. **No invented tool schemas.** If you need a new tool,
   extend the schema in `internal/agent/tools.go` matching the
   existing pattern (OpenAI-style `type: function` + JSON
   schema parameters). Don't invent a parallel schema dialect
   for "this one tool" — it complicates the Reviewer prompt
   and the test fixtures.
8. **Subagent depth is 1.** A subagent may not spawn a
   subagent. Enforced in `spawn_subagent`. If a task really
   needs recursion, the human needs to decide whether to raise
   the cap (it has cost — every nested level doubles context
   growth and tool-call latency).

## Adding a new tool — checklist

When the task is "add a new Coder tool X", the path is well-worn:

1. **Define the schema in `internal/agent/tools.go`.** JSON
   schema for the params, a one-line description, the tool
   name. Match the existing tools' style.
2. **Implement the dispatcher case in the loop's
   `executeApprovedAction`.** The dispatcher routes the
   parsed tool call to the executor.
3. **Implement the executor.** Put I/O in `internal/<pkg>/`.
   Subagent-allowed? Add to `internal/subagent/subagent_tools.go`
   allowed list. Browser-related? Add to `internal/browser/`.
4. **Result format.** Return a structured
   `actionResult{OK bool, Output string, Err string}`. The
   loop appends it as a transcript `action_result` entry
   with `[Coder]:` speaker.
5. **Test.** Happy path AND at least one failure path
   (malformed args, missing file, command timeout, network
   error). Phase 1's invariant applies to every new tool —
   happy-path-only is not "done."
6. **Update `/help`?** Slash commands live in
   `commands/*.md` and reload on session start. New tools
   don't need a `/foo` of their own, but if the tool is
   something the human should be able to invoke directly,
   add a `commands/<toolname>.md` stub that tells Coder to
   use the tool.

## Adding a new slash command

Slash commands are pure markdown + YAML frontmatter. No code
change needed.

1. Create `commands/<name>.md` with frontmatter
   (`name`, `target: coder` or `target: reviewer`,
   `description`) and a body that uses `{{args}}` for
   argument interpolation.
2. On next session start, the loader picks it up
   automatically — no restart required for the file itself,
   but the running session has already loaded its command
   list, so the new command appears after `/restart` or a
   fresh process.
3. Test by typing `/<name> <args>` in a session and
   confirming the right agent receives the expanded
   prompt.

## Adding a new skill (meta)

If a new domain emerges in real work (e.g. someone is doing a
lot of Docker work and wants a `docker` skill):

1. Use `/skill add <name>` to scaffold (or hand-write a
   `skills/<name>.md` + `skills/<name>-mini.md`).
2. Frontmatter must match `internal/skills/loader.go`:
   `name`, `section`, `description`, `tier` (`main` for
   `<name>.md`, `mini` for `<name>-mini.md`),
   `mini_ref`, `token_budget_main`, `token_budget_mini`.
3. Section must be unique across all skill files. Pick a
   short, single-word label — it's the Stage 1 token.
4. Verify the body fits the budget (Main 5–8k tokens, Mini
   2–4k) with a real tokenizer (Phase 5.4). The 4-chars/token
   estimator is fine for a quick gut check but not for
   enforcement.

## Token budget

`EstimateTokens` (4 chars/token) is good enough for the
in-`/trace` per-turn cost display. The hard budget
enforcement — checking that a Main body is 5–8k tokens and
a Mini is 2–4k — needs a real tokenizer. The project does
not currently bundle tiktoken. Two practical options:

- **Build-time check via a small Go test** that runs a
  real BPE encoder (e.g. `github.com/sugarme/tokenizer`
  with a GPT-2/CL100k vocab) against each loaded skill
  and fails CI on out-of-budget.
- **Manual phase-gate** where the human runs a tokenizer
  script (e.g. Python `tiktoken`) and commits the count
  next to the frontmatter as a comment.

Pick whichever the human prefers. Until then, the
`token_budget_*` frontmatter values are advisory.

## What this skill does NOT cover

- TUI rendering, keybindings, layout, status bar, sidebar
  → `frontend` skill. If your change is only visible
  behavior with no loop / transcript / tool change, use
  `frontend` alone.
- Persistent DB schema or migration → there is no DB;
  see `db` skill for the JSONL-as-database reality.
- Skill content authoring — see "Adding a new skill"
  above; that's a meta-skill, not a domain.
- Cross-agent routing in Orchestrator mode
  (`internal/loop/orchestrator.go`) — same approval loop,
  but the routing rubric (`internal/loop/rubric.go`) is its
  own concern; consult that file directly when touching
  it.

## Worked example — adding a new `read_file` tool (happy + failure paths)

Concrete walkthrough of the "add a tool" checklist so the
abstract steps have a shape.

**Task:** "Add a `read_file` tool to Coder that returns the
contents of a file under the working directory, with a
5000-byte cap."

**Step 1 — Schema in `internal/agent/tools.go`.** Match
existing tool style:

```go
{
    Name: "read_file",
    Description: "Read the contents of a file under the working directory. " +
        "Returns up to 5000 bytes. Use this to inspect code, config, " +
        "or any text file before proposing a write_file edit.",
    Parameters: map[string]any{
        "type": "object",
        "properties": map[string]any{
            "path": map[string]any{
                "type": "string",
                "description": "Path relative to the working directory. " +
                    "Absolute paths are rejected.",
            },
        },
        "required": []string{"path"},
    },
},
```

The 5000-byte cap belongs in the description because the
model needs to know to chunk reads for large files; it
also belongs as a hard limit in the executor to enforce it.

**Step 2 — Dispatcher case in the loop's
`executeApprovedAction`.** Match the existing tool
dispatcher pattern (usually a `switch tool.Name` with one
case per tool). Add a `case "read_file":` that calls the
executor and wraps the result.

**Step 3 — Executor.** Lives in `internal/tools/read.go`
(or wherever the existing file tools live — keep them
co-located):

```go
func ReadFile(workdir, relPath string) (string, error) {
    if filepath.IsAbs(relPath) {
        return "", fmt.Errorf("absolute paths not allowed: %q", relPath)
    }
    full := filepath.Join(workdir, relPath)
    // Resolve and re-check: filepath.Join + IsAbs isn't enough
    // because ".." can produce a path that escapes workdir.
    abs, err := filepath.Abs(full)
    if err != nil {
        return "", fmt.Errorf("resolve %q: %w", relPath, err)
    }
    if !strings.HasPrefix(abs, workdir) {
        return "", fmt.Errorf("path escapes workdir: %q", relPath)
    }
    data, err := os.ReadFile(abs)
    if err != nil {
        return "", fmt.Errorf("read %q: %w", abs, err)
    }
    const cap = 5000
    if len(data) > cap {
        return string(data[:cap]) + "\n[...truncated, file is " +
            strconv.Itoa(len(data)) + " bytes total...]", nil
    }
    return string(data), nil
}
```

The two-step absolute-path check is the security
invariant — never trust the model's input path even
though the approval loop gates it. Reviewer should
catch a malicious path, but the executor must not
rely on Reviewer as a security boundary.

**Step 4 — Result format.** Return
`actionResult{OK: err == nil, Output: content, Err: errMsg}`.
The loop appends it as a transcript `action_result` entry
with `[Coder]:` speaker, so the human sees the file
content in the conversation flow.

**Step 5 — Tests.** At minimum:

- **Happy path:** write a known string to a file, call
  `ReadFile(workdir, relPath)`, assert the string comes
  back unchanged.
- **Absolute path rejected:** `ReadFile(workdir,
  "/etc/passwd")` returns an error, the executor never
  touches the filesystem.
- **Path traversal rejected:** `ReadFile(workdir,
  "../outside.txt")` returns an error, the executor
  never touches the filesystem.
- **Missing file:** `ReadFile(workdir, "nope.md")`
  returns the wrapped os.ReadFile error, no panic.
- **Truncation:** write 10000 bytes, call `ReadFile`,
  assert output is `5000 bytes + truncation marker`.

**Step 6 — `/read` slash command?** Optional. If the
human should be able to invoke `read_file` directly,
add `commands/read.md`:

```markdown
---
name: read
target: coder
description: Read a file under the working directory
---

Use the `read_file` tool to inspect {{args}}. Report the
full contents back to me. If the file is larger than
5000 bytes, the tool truncates — call it again with a
smaller scope if you need more.
```

## Patterns that work (read these before inventing a new one)

These are the patterns the codebase has settled into. If
you're about to do something close to one of these, copy
the existing implementation rather than designing a new
one.

- **All async work goes through `loop.AgentClient`.** The
  loop owns the HTTP client (`internal/agent/client.go`).
  Don't instantiate a second `http.Client` in a tool
  executor. If a tool needs to make an HTTP call, the
  call should be a subagent or it should be a feature
  request to add to `AgentClient`. The reason: rate
  limiting, retry/backoff, and trace logging all live
  in `AgentClient` — bypassing it means bypassing
  observability.
- **The approval state machine is single-loop.** All
  Coder turns funnel through `loop.Run` (or
  `loop.RunOrchestrator`). Don't spin up a second loop
  in a tool executor "for parallelism" — it breaks
  transcript ordering, Reviewer state, and the trace
  log correlation. If a tool is genuinely
  long-running, the right answer is `spawn_subagent`
  (which is itself a single-loop call).
- **Tool executors are pure functions.** They take
  inputs, return outputs, and do not touch the
  transcript directly. The loop wraps the executor's
  return into a transcript entry. This separation
  is what makes the executors testable without
  standing up the full TUI / loop / transcript stack.
- **Error wrapping follows `%w`, not `%v`.** Always
  `fmt.Errorf("...: %w", err)` so `errors.Is` /
  `errors.As` work for callers. `%v` swallows the
  chain and is a one-line code review reject.
- **Context is the first parameter.** Any function
  that does I/O (HTTP, file, network) takes
  `ctx context.Context` as its first parameter.
  `context.Background()` is a code review reject
  except in `main.go` and tests.
- **Structured logging via `logger.L()`.** Don't
  reach for `log` or `fmt.Println` in a tool
  executor. The structured logger (`internal/logger`)
  is what `/trace` reads from, and the log file
  (`triad.log`) is the only place to debug a tool
  failure that doesn't surface in the transcript.
- **Concurrency limits are explicit.** The subagent
  pool has a max-parallel setting in config. Don't
  add a `sync.WaitGroup` "for a moment" — go through
  the pool. Same applies to the browser pool, the
  HTTP client pool, etc. Implicit concurrency
  causes the "ten agents, one rate limit" failure
  mode.
- **Timeouts are explicit.** Every I/O call has a
  `context.WithTimeout` somewhere. The default
  per-tool timeout lives in `config.yaml`; a tool
  that needs longer should document why in its
  description and override explicitly. An I/O call
  with no timeout is a hang waiting to happen.

## Common pitfalls (all hit at least once)

- **Hardcoding a model ID or endpoint in a new file** —
  always go through `AgentConfig` / config layer. The
  config layer exists *so you don't have to think about
  which model is configured today*. A literal
  `"mimo-v2.5-free"` in a tool executor means the
  executor only works for one model and one endpoint.
- **Adding a new model role with tools when you meant
  "Reviewer with extra permissions"** — that's still
  Reviewer; extend the Reviewer's prompt to ask for the
  extra signal, not give it a tool set. Reviewer-with-
  tools is just Coder wearing a different hat, and it
  reintroduces the "who reviews the reviewer" problem.
- **Free-tier rate-limit handling: backoff is in
  `internal/agent/client.go`.** If you're tempted to add
  a `time.Sleep` retry in a new call site, route through
  the client instead. The client's backoff is shared
  across all callers; ad-hoc sleeps are not.
- **`/trace` cross-pollution: trace path is derived
  from the bound session.** If a new call site writes
  to the default `default.jsonl` without a bound
  session, you'll pollute other sessions' traces. Use
  `tracelog.TracePathForSession(sessionPath)` — never
  hardcode a path.
- **Forgetting to mark the loop's transcript as bound
  before writing a skill-selection trace** — the
  `appendSkillSelectionTrace` no-ops in that case,
  which is correct, but it can mask a bug where you
  meant to bind a session. If you're writing a trace
  event from a new call site, the first line should
  be `tr := tr.BindFile(sessionPath)` (or equivalent
  in the existing API).
- **Subagent return shape.** A subagent's return
  value is a single `Summary` string. Don't try to
  return structured data through the subagent
  channel — the orchestration layer will throw it
  away. If you need structured data, have the
  subagent write it to a file and have the parent
  Coder read that file. (Slightly indirect, but it's
  the only contract the subagent API guarantees.)
- **Browser cleanup.** The Playwright browser is
  launched lazily and lives for the session. Don't
  launch a new browser in a tool executor — call the
  shared `browser.Manager`. Otherwise the
  integration tests' teardown will leak browser
  processes.
- **Reviewer prompt drift.** The Reviewer's system
  prompt is in `internal/agent/prompts.go`. If you
  tweak it, run the integration tests — they assert
  the Reviewer catches a set of known-bad
  proposals, and a prompt change can silently
  regress that. Same for Coder's prompt.
- **Concurrent appends to the same session JSONL.**
  `transcript.Transcript.Append` is safe for
  concurrent use within a process, but if you write
  to a different JSONL in the same directory
  concurrently, you can hit OS-level write
  interleaving on some filesystems. Stick to one
  writer per file, per process.
