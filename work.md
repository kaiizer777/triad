# Triad — Workflow 2: Commands, Subagents & Extended Tools

**Current state of project (July 2026):** v1 is complete and working — shared
transcript, Coder + Reviewer on `mimo-v2.5-free` via OpenCode Zen, full
propose→review→execute approval loop with Reviewer veto power, human
interjection, bubbletea TUI, and session persistence/resume. All 8 phases from
Workflow 1 (`docs/work1.md`) are done and the tool runs real tasks end to end.
This document covers the **next layer**: custom slash commands, git
auto-commit on every executed edit, hooks, subagent spawning, and expanded
tool calls (browser control, multi-agent delegation).


---

## 0. What This Phase Adds, and Why

v1 proved the core loop works. What's missing to make Triad feel like a real
daily driver (on par with what OpenCode/Claude Code offer natively) is:

1. **Slash commands** — reusable shortcuts (`/plan`, `/diff`, `/undo`) instead
   of retyping instructions every session
2. **Git auto-commit on every edit** — every executed file write gets its own
   commit automatically, giving you a full, granular history and making
   `/undo` trivial to implement correctly (see Section 2)
3. **Hooks** — automatic actions triggered on events (e.g. auto-format on every
   file write, block a dangerous command before it reaches Reviewer at all)
4. **Subagents** — Coder or Reviewer can delegate a bounded sub-task to a
   short-lived, isolated-context agent and get back only a summary, instead of
   polluting the main transcript with exploratory work
5. **Extended tool calls** — browser/computer control, not just file+shell

This is genuinely how the leading tools (Claude Code, OpenCode itself) are
architected as of mid-2026 — you're not inventing new patterns, you're
implementing well-established ones inside your own transcript-based design.

---

## 1. Slash Commands

### 1.1 What they actually are (confirmed current pattern)

Both Claude Code and OpenCode converged on the same shape: a slash command is
a **markdown file with YAML frontmatter**, not a hardcoded function. OpenCode
specifically defines custom commands in `.opencode/commands/*.md`; Claude Code
merged the same concept into its "skills" system (`.claude/skills/<name>/
SKILL.md`). The frontmatter carries metadata (which agent it targets, which
model to pin, whether it forces a subagent), and the markdown body is the
actual instruction/prompt template.

### 1.2 Design for Triad

Adopt the same shape — it's a proven, well-understood convention, and matches
your existing "config over code" philosophy from v1.

- [ ] 1.2.1 — Create a `commands/` directory in project root (mirrors
      `.opencode/commands/`)
- [ ] 1.2.2 — Define your command file format:
      ```md
      ---
      name: plan
      target: coder        # which agent this command addresses
      description: Ask Coder to produce a plan only, no tool calls yet
      ---
      Propose a step-by-step plan for the following task, but do not call any
      tools yet. Wait for Reviewer and human sign-off on the plan before
      proposing your first concrete action.

      Task: {{args}}
      ```
- [ ] 1.2.3 — Implement a command parser in `internal/commands/parser.go`:
      reads all `.md` files in `commands/`, parses YAML frontmatter (use
      `gopkg.in/yaml.v3`, already a dependency) + body template
- [ ] 1.2.4 — Implement `{{args}}` substitution: when the human types
      `/plan Add Razorpay webhook`, the command loader replaces `{{args}}`
      with `"Add Razorpay webhook"` before injecting the resulting message
      into the transcript as a `You` entry
- [ ] 1.2.5 — Wire command detection into the TUI input handler (Phase 6 of
      Workflow 1): if the human's input starts with `/`, look it up in the
      loaded command set before treating it as a plain message
- [ ] 1.2.6 — **Test:** create 2–3 real commands you'll actually use
      (suggestions below) and confirm each correctly expands and routes

### 1.3 Suggested first commands (start small, expand as you notice repetition)

| Command | Purpose |
|---|---|
| `/plan <task>` | Coder proposes a plan only, no tool calls, forces explicit Reviewer sign-off before any action |
| `/diff` | Reviewer re-displays the full diff of the last proposed (not yet executed) action, for a second look |
| `/undo` | Reverts the last executed file write (built on the auto-commit system in Section 2 below) |
| `/status` | Prints a short summary of session state: current task, how many actions taken, idle or active |
| `/strict` | Toggles a session flag that tightens Reviewer's system prompt to be maximally skeptical for one task (useful for genuinely critical changes) |

`/undo` is covered in full in **Section 2 (Git Auto-Commit)** below, since it's
now a thin command on top of the auto-commit system rather than its own
separate checkpoint mechanism.

---

## 2. Git Auto-Commit on Every Edit

### 2.1 What this is, and why it fits naturally here

Every executed `write_file` action (and optionally `run_command`, if it
changes files) gets committed to git **automatically**, with no human or
agent needing to remember to do it. This gives you:

- A full, granular, timestamped history of every change either agent made,
  independent of your own git discipline
- A trivial foundation for `/undo` (Section 2.3) — reverting becomes
  `git revert`/`git checkout` against a real commit, not a custom snapshot
  system you have to build and maintain separately
- A natural audit trail that complements the transcript: the transcript says
  *why* something happened (Coder's reasoning, Reviewer's check), git says
  *what* changed, file by file

This is a good match for your existing "no hardcoded destructive-action gate"
decision from v1 — auto-commit doesn't block or slow down execution at all,
it just runs alongside it. It adds safety net *after the fact* without adding
friction *before* the fact, so it doesn't quietly walk back your original
design choice the way a blocking hook might (see Section 3.2.3's note on this
same tension).

### 2.2 Design for Triad

- [ ] 2.2.1 — On startup, check whether the project working directory is
      already a git repo; if not, run `git init` automatically (surface this
      clearly to the human the first time it happens, don't do it silently)
- [ ] 2.2.2 — Add a commit step to the **end** of the action-execution path
      in the approval loop (`internal/loop/loop.go`, right after a
      `write_file` tool call succeeds and its `action_result` entry is
      appended) — not before, since you want to commit what actually landed
      on disk, not a pre-action snapshot
- [ ] 2.2.3 — Use `git add <path>` scoped to just the file(s) the action
      touched, then `git commit -m "<message>"` — don't blanket `git add .`,
      since that could sweep in unrelated working-tree changes you didn't
      intend to commit
- [ ] 2.2.4 — Design the commit message format to be genuinely useful later,
      not just `"auto-commit"` — include the transcript entry ID and a short
      excerpt of Coder's stated intent, e.g.:
      ```
      [triad] entry #47: add HMAC signature verification

      Proposed by: Coder
      Approved by: Reviewer
      Session: sessions/2026-07-25-webhook.jsonl
      ```
- [ ] 2.2.5 — Decide how `run_command` actions that modify files are handled
      — a shell command isn't a single known file path the way `write_file`
      is. Simplest approach: after any `run_command` executes, run
      `git status --porcelain` to detect what changed, then commit exactly
      those paths with a similar message format
- [ ] 2.2.6 — Handle the "nothing changed" case cleanly: if a `run_command`
      or `write_file` action results in no actual diff (e.g. writing
      identical content, or a command that only reads), skip the commit
      rather than creating empty/no-op commits
- [ ] 2.2.7 — Handle commit failures gracefully (e.g. git not configured with
      a user.name/user.email on this machine) — surface a clear one-time
      error rather than silently failing on every single action afterward
- [ ] 2.2.8 — **Test:** run a task through the full approval loop, confirm
      each approved action produces exactly one commit, with the file(s)
      actually changed and a message that matches the format from 2.2.4
- [ ] 2.2.9 — **Test:** run a `run_command` action that modifies multiple
      files in one go (e.g. a codegen script), confirm all changed files land
      in a single sensible commit rather than being missed or split oddly

### 2.3 `/undo` on top of auto-commit

With every action now individually committed, `/undo` (introduced in Section
1.3) becomes straightforward:

- [ ] 2.3.1 — Implement `/undo` as `git revert <last-triad-commit>
      --no-edit` (revert, not reset — preserves history rather than
      destroying it, which matters since you want the transcript and git log
      to stay in sync as two honest records of what happened, including
      corrections)
- [ ] 2.3.2 — After reverting, append a `System` entry to the transcript
      noting what was undone and referencing the original entry ID, so the
      transcript and git history tell the same story
- [ ] 2.3.3 — **Test:** execute a file write via the normal approval loop,
      run `/undo`, confirm the file reverts on disk, a new revert commit
      appears in git log, and the transcript reflects it

### 2.4 A decision worth making explicitly: does auto-commit apply during objection loops?

- [ ] 2.4.1 — Decide: should a `proposed_action` that Reviewer *objects to*
      ever touch git at all? (Recommended: no — only actions that actually
      execute get committed. A rejected proposal never reaches
      `write_file`/`run_command` in the first place under the v1 design, so
      this should already be naturally true; just confirm it explicitly in
      testing rather than assuming.)

---

## 3. Hooks

### 3.1 What they are

A hook is code that runs automatically on a specific event — before or after
a tool call — without the model having to remember to trigger it. Claude
Code's hook system is the clearest reference implementation: hooks fire on
events like `PostToolUse` (matched against tool name/pattern) and run a
shell command, e.g. auto-formatting a file immediately after every `Edit`/
`Write`.

### 3.2 Design for Triad

- [ ] 3.2.1 — Define a `hooks.yaml` (or extend `config.yaml`) with a simple
      event → command mapping:
      ```yaml
      hooks:
        post_write_file:
          - "gofmt -w {{path}}"
        pre_run_command:
          - block_if_matches: ["rm -rf", "git push --force", "sudo "]
      ```
- [ ] 3.2.2 — Implement `post_write_file` hooks: after a `write_file` tool
      executes successfully (Phase 3.4 of Workflow 1) **and after the
      auto-commit from Section 2.2 has run**, execute each configured
      command with `{{path}}` substituted, append the hook's output as a
      `System` transcript entry. Running this after auto-commit means a
      hook like `gofmt -w` produces its own separate, clearly-attributed
      follow-up commit rather than being silently folded into Coder's
      commit.
- [ ] 3.2.3 — Implement `pre_run_command` hooks as a **pattern-match safety
      net**, separate from and prior to Reviewer's veto: if a proposed shell
      command matches a blocklist pattern, refuse to execute it even if
      Reviewer approved, and surface this clearly in the transcript as a hard
      stop rather than a silent skip
      - Note: this is worth reconsidering given your v1 decision for "full
        trust in Reviewer, no hardcoded gate" — a hook-based blocklist is a
        *narrower*, opt-in version of that gate (a handful of genuinely
        catastrophic patterns, not a general human-approval requirement), and
        you can leave the blocklist empty by default if you want to preserve
        the original decision exactly. Your call; document whichever you pick
        in PROJECT_SPEC.md so it doesn't silently drift from the original
        design record.
- [ ] 3.2.4 — **Test:** configure a `gofmt` post-write hook, confirm it fires
      automatically and reformats a file Coder just wrote, without either
      agent being told to do so explicitly

---

## 4. Subagents

### 4.1 The pattern, confirmed from current implementations

Every major 2026 coding agent (Claude Code, OpenCode, Cursor, Codex) has
converged on the same shape for subagents:

- A subagent gets its **own isolated context window** — it does not see the
  parent's full conversation history, only what it's explicitly handed
- It runs with **its own (often narrower) tool permissions**
- When it finishes, it returns **only a summary** to the parent — intermediate
  tool calls and exploration stay private to the subagent, keeping the
  parent's context clean
- OpenCode's own internal implementation (confirmed from their agent-teams
  work) uses **fire-and-forget spawning + file-based inbox persistence**: the
  parent writes a task file, the subagent runs independently, writes its
  result to an inbox file, and the parent polls or reads it back — this maps
  directly onto your existing JSONL transcript approach

### 4.2 Design for Triad

This is the most architecturally significant addition in this phase — budget
real time here, similar to how Phase 4 (the approval loop) was the hard part
of Workflow 1.

- [ ] 4.2.1 — Define a `Subagent` concept distinct from your main `Coder`/
      `Reviewer`: same `Agent` interface from v1 (§6.2 of PROJECT_SPEC.md),
      but instantiated with a **fresh, empty transcript** (or a narrow, hand-
      constructed one) rather than the full session transcript
- [ ] 4.2.2 — Add a new tool for Coder: `spawn_subagent(task, context)` —
      `context` is an explicit, bounded string the parent passes in (not the
      full transcript), forcing Coder to be deliberate about what the
      subagent actually needs to know
- [ ] 4.2.3 — Implement subagent execution as genuinely separate: its own
      transcript file under `sessions/subagents/<id>.jsonl`, its own call loop
      (can reuse most of Phase 2/3's client code), running to completion (or a
      turn cap) independently of the main loop
- [ ] 4.2.4 — On subagent completion, produce a **summary only** — either have
      the subagent itself emit a final "summary" message as its last turn, or
      have the parent's `spawn_subagent` tool result contain just that
      summary, not the subagent's full transcript
- [ ] 4.2.5 — Append the subagent's summary to the **main** transcript as a
      single `action_result` entry attributed to the subagent (e.g.
      `Speaker: "Subagent:explore"`), so Reviewer still sees *that* delegation
      happened and *what* it concluded, without seeing every intermediate step
- [ ] 4.2.6 — **Recursion guard:** if you ever let a subagent spawn its own
      subagent, enforce an explicit depth limit (start at 1 — subagents in v1
      of this feature cannot themselves spawn subagents) to avoid runaway
      nesting. This is a known failure mode in current research on subagent
      systems — worth taking seriously even at small scale.
- [ ] 4.2.7 — **Test:** give Coder a task that plausibly benefits from
      delegation (e.g. "check how the existing codebase handles auth before
      adding this new route" — a bounded research task), confirm it spawns a
      subagent, the subagent runs independently, and only a clean summary
      lands back in the main transcript for Reviewer to see

### 4.3 What subagents are (and aren't) good for in Triad specifically

- **Good fit:** bounded research/exploration ("read these 5 files and tell me
  the existing error-handling pattern"), isolated verification tasks ("run the
  test suite and summarize failures"), anything where the *process* of finding
  an answer is noisy but the *answer* is short
- **Bad fit:** anything that needs Reviewer's live, step-by-step oversight —
  subagents bypass the propose/review/execute loop by design (that's the whole
  point of isolation), so don't let Coder spawn a subagent to do the actual
  risky work of the task itself. Reserve subagents for support work around the
  main loop, not as a way to route around Reviewer.

---

## 5. Extended Tool Calls

### 5.1 Two very different tiers — pick based on cost, not just capability

Current tooling splits into two fundamentally different approaches, confirmed
from Anthropic's own computer-use docs and current MCP tooling:

1. **Raw computer use** (screenshot + mouse/keyboard loop): the model sees a
   screenshot, decides on a click/type/scroll action, gets a new screenshot,
   repeats. Works for anything with a GUI, but every turn sends a
   base64-encoded image — expensive in tokens, and slower per action. This is
   what Anthropic's own Computer Use API and Claude Code's computer-use mode
   do.
2. **Structured browser control** (Playwright-MCP style): tool calls like
   `navigate(url)`, `click(selector)`, `type(selector, text)`,
   `get_text(selector)` operate against the actual DOM/accessibility tree, not
   pixels. Far cheaper, faster, and more reliable — but only works for
   browser-based targets, not arbitrary desktop apps.

**Recommendation for Triad, given your free-tier model constraint:** build
structured browser control (tier 2) first, skip raw computer use (tier 1)
entirely for now. Screenshot-based tool calls will burn through your
already-uncertain free-tier rate limits far faster, and `mimo-v2.5-free` was
chosen for tool-calling reliability, not multimodal screenshot reasoning
quality — tier 2 plays to what you already have.

### 5.2 Design for Triad — structured browser tool

- [ ] 5.2.1 — Add a Go browser automation dependency —
      `github.com/playwright-community/playwright-go` is the direct
      equivalent of the Node Playwright MCP server, usable from Go directly
      without needing Node in the loop
- [ ] 5.2.2 — Define new tool schemas in `internal/agent/tools.go`, same
      pattern as `write_file`/`run_command`: `browser_navigate(url)`,
      `browser_click(selector)`, `browser_type(selector, text)`,
      `browser_get_text(selector)`, `browser_screenshot()` (only for cases
      where visual confirmation genuinely matters — keep this one rare)
- [ ] 5.2.3 — These go through the **same approval loop** as file/shell tools
      — no special-casing. Reviewer sees "Coder wants to navigate to
      `https://api.razorpay.com/docs`" the same way it sees a proposed file
      diff, and approves/objects the same way
- [ ] 5.2.4 — **Test:** give Coder a task that requires checking live
      documentation or a running local dev server (e.g. "check that the
      webhook endpoint returns 200 by hitting it in a browser"), confirm the
      browser tools work through the full propose→approve→execute cycle

### 5.3 Explicit non-goal for this phase

Do **not** build raw screenshot-based computer use in this phase. It's a
legitimate future addition if you hit a wall structured browser control can't
solve (e.g. a native desktop app with no DOM), but it's meaningfully more
complex and more expensive to run — don't reach for it before you've actually
needed it.

---

## 6. Suggested Build Order for This Phase

Same philosophy as Workflow 1: prove the simplest thing works before adding
the next layer.

1. **Slash commands** (Section 1) — no architecture change, pure quality-of-
   life, do this first
2. **Git auto-commit** (Section 2) — small, self-contained addition; do this
   right after commands since `/undo` depends on it and it's good to have
   real commit history before you're also running hooks/subagents that touch
   files
3. **Hooks** (Section 3) — small, isolated addition, good next step
4. **Extended browser tools** (Section 5) — same shape as existing tools, just
   more of them; do this before subagents since it doesn't require new
   architecture
5. **Subagents** (Section 4) — save for last; it's the one genuinely new
   architectural concept in this phase and benefits from everything else
   being stable first

---

## 7. Updated Directory Structure (additions only)

```
triad/
├── commands/                    # NEW — slash command .md files
│   ├── plan.md
│   ├── status.md
│   └── strict.md
├── hooks.yaml                   # NEW — event → command mapping
├── internal/
│   ├── commands/
│   │   └── parser.go            # NEW — frontmatter + template parsing
│   ├── gitcommit/
│   │   └── gitcommit.go         # NEW — auto-commit on executed actions,
│   │                              /undo support (Section 2)
│   ├── hooks/
│   │   └── hooks.go             # NEW — hook execution
│   ├── subagent/
│   │   └── subagent.go          # NEW — isolated subagent spawn/summary
│   └── agent/
│       └── tools.go             # UPDATED — add browser_* tool schemas
└── sessions/
    └── subagents/                # NEW — isolated subagent transcripts
        └── <subagent-id>.jsonl
```

---

## 8. Open Items Carried Forward / New

- Decide whether auto-commit (Section 2) should be possible to disable per
  session (e.g. if you're working inside a repo with its own commit
  conventions and don't want Triad's commits interleaved with yours) — a
  config flag is cheap insurance even if you leave it on by default
- Confirm `git revert` behavior (§2.3.1) when a later commit has already
  modified the same lines an `/undo` target touched — a plain revert can
  conflict, and you'll want a defined fallback (e.g. surface the conflict to
  the human rather than attempting an automatic resolution)
- Confirm whether `pre_run_command` blocklist hooks (§3.2.3) should exist at
  all, given the v1 "no hardcoded gate" decision — resolve and document in
  PROJECT_SPEC.md rather than letting the two documents disagree
- Decide the subagent turn/time cap (§4.2.3) — unbounded subagent execution on
  a rate-limited free tier is a real risk, needs a concrete number, not just
  "runs until done"
- `playwright-go` requires downloading browser binaries on first run — this is
  a network dependency outside your currently-allowed egress domains list in
  some environments; confirm it can actually install where you're building
  before committing to it
- Once subagents exist, decide whether Reviewer should ever be allowed to
  spawn one (v1 of this phase assumes only Coder can — worth stating
  explicitly rather than leaving ambiguous)