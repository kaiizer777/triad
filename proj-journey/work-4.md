# Workflow 5 — Task-Driven Skill Injection System

**Status:** Scoped, not yet built
**Depends on:** v1 + Workflow 2 + Workflow 3 + Workflow 4 (all shipped)
**Builds on:** existing slash-command loader (`internal/commands`) — same
markdown+YAML-frontmatter convention, new loader package

---

## 1. Problem

Coder's system prompt today is generic — same prompt whether it's editing
a React component, a Go handler, or a SQL migration. Domain-specific
conventions (naming, error handling, framework idioms, schema patterns)
live only in Coder's training data, not in anything Triad controls or you
can edit. There's no way to say "always do X in frontend work on this
project" and have it reliably applied.

Naively fixing this by stuffing all domain knowledge into one big system
prompt fails: a single frontend+backend+db mega-prompt easily hits
15-20k tokens injected on *every* turn regardless of relevance — expensive,
and dilutes attention on the parts that matter for the current action
(context rot).

## 2. Core Idea

Skills are markdown+frontmatter files, one per domain, loaded
conditionally based on what the current task actually touches — not
injected wholesale every turn.

Two tiers per skill, mirroring how a human stops re-explaining something
once it's already been said once in a conversation:

- **Main Skill** (5–8k tokens): full domain knowledge — conventions,
  patterns, architectural rules, gotchas. Injected **once per session**,
  the first time that domain is detected as relevant.
- **Mini Skill** (2–4k tokens): a condensed reminder/pointer version.
  Injected on every subsequent action in that session that touches the
  same domain, after the Main Skill has already fired once.

This keeps steady-state per-turn cost low (2-4k, not 5-8k) while still
paying the full information cost exactly once per session per domain.

## 3. Who Gets Skills

- **Coder** (in Triad mode) and **coding subagents** (spawned under
  Orchestrator mode) — skill selection is mandatory before the first
  action of any coding task.
- **Orchestrator** itself never receives domain skills — it isn't writing
  code, only routing. It gets orchestration-context only (existing
  behavior, unchanged).
- **Reviewer** does not receive skills either — it reviews diffs against
  locked architecture decisions and correctness, not domain style
  conventions. (Open question if this needs revisiting later — see §8.)

## 4. Skill File Format

New directory: `skills/` at project root (parallel to existing
`commands/*.md`).

```
skills/
  frontend.md
  backend.md
  db.md
  <custom>.md
```

Each file:

```markdown
---
name: frontend
section: frontend   # the bare label shown in the Stage 1 scan
description: "React/TS UI work — components, styling, client state,
  forms. Use for any task touching .tsx/.jsx files, CSS, or client-side
  logic."
tier: main        # or "mini" if this file IS a mini variant
mini_ref: frontend-mini.md   # optional: pointer to the mini-tier file
token_budget_main: 6500
token_budget_mini: 3000
---

<Main skill body — conventions, patterns, gotchas>
```

- `section` is the only field read during Stage 1 — must be unique
  across all skill files, since section:skill is always 1:1 (§5).
- `description` is only read during Stage 2, after a section has already
  been selected — it never factors into the Stage 1 scan cost.
- Main and Mini can be two files (`frontend.md` / `frontend-mini.md`) or
  two sections in one file — implementation detail, doesn't change the
  design. Two files is simpler to diff/edit independently.

## 5. Detection & Loading Flow — Two-Stage Funnel

Skill count is expected to grow over the life of a project (custom
domains beyond frontend/backend/db). A flat scan of every skill's
description on every turn would grow unbounded as skills are added. To
keep the *selection* step cheap regardless of how many skills exist, add
a **section layer** in front of the skill layer:

**Stage 1 — Section scan (cheap, always runs in full):**

Every section that exists is shown to Coder as a bare label — no
description, no body, just the name (e.g. `frontend`, `backend`, `db`,
`mobile`, `auth`, ...). This is the only thing that scales with total
skill count. Short common words like these tokenize to roughly 1 token
each; including per-entry list formatting (delimiter/newline), a
realistic estimate is **~2-3 tokens per section entry**. At that rate,
even 100 sections costs on the order of 200-300 tokens — negligible
against `mimo-v2.5-free`'s 200K context window (verified: OpenCode Zen
lists `mimo-v2.5-free` at 200,000 token context / 32,000 max output),
and negligible against any comparable frontier model. This scan also
re-runs every coding turn (not cached per session) — at this per-turn
cost, that's an acceptable tradeoff for always having an up-to-date
section list, not a performance concern worth optimizing away.

**Sections are strictly single-domain — no combination sections.**
There is no `frontend+backend` section. A task spanning multiple domains
is handled by selecting multiple single-domain sections in the same
pass (see cap below), not by pre-authoring every possible pairing —
combination sections would grow combinatorially (10 domains → up to
1000+ pairings) and defeat the point of a cheap scan.

**Sections map 1:1 to skill files — no bundling.** One section always
corresponds to exactly one skill file. A section never silently bundles
several related skill files together, because that would let a small
number of section picks balloon into loading many skill files' worth of
tokens — directly undermining the point of capping section selection.

**Hard cap: Coder may select at most 3 sections per task, no
exceptions.** This is a hard ceiling, not a soft default — even a task
that plausibly spans 4+ domains is capped at 3. This bounds worst-case
per-task skill-loading cost regardless of how many sections exist in
the project.

**Stage 2 — Skill load (only for the ≤3 selected sections):**

1. **Every coding session** (Triad Coder turn, or Orchestrator-spawned
   coding subagent), before the first action: Coder runs the Stage 1
   section scan and selects up to 3 sections relevant to the current
   task. This step is **mandatory**, not optional — no coding action
   proceeds without a section selection having happened, even if the
   answer is "none apply."
2. For each selected section's skill, **not yet loaded this session**:
   inject its **Main Skill**. Mark it as loaded-this-session.
3. For each selected section's skill, **already loaded this session**:
   inject its **Mini Skill** instead of the Main Skill.
4. **Multi-domain tasks stack independently within the 3-section cap** —
   a task touching frontend+backend+db (3 sections, at the cap) runs the
   Main/Mini logic per section, not as one merged decision. First DB
   touch this session → DB Main. Second frontend touch this session →
   frontend Mini (if frontend Main already fired earlier this session).
5. Section selection and the resulting load (which section, which skill,
   which tier) gets written to the transcript as a system entry — this
   is what makes it observable (§7), not a side-channel decision Coder
   makes invisibly.

This two-stage design means adding a 50th or 100th skill/section never
increases per-task cost beyond the Stage 1 label scan — Stage 2 cost is
bounded by the 3-section cap regardless of how many sections exist to
choose from.

## 6. `/skill` Command

New slash command, following the existing `commands/*.md` convention
(consistent UX with `/plan`, `/diff`, `/undo`, etc.):

- `/skill list` — show all skills with name, description, tier sizes,
  last-modified.
- `/skill view <name>` — show a skill's full content (Main and Mini)
  in the TUI.
- `/skill edit <name>` — open Main or Mini body for editing inline
  (TUI text editor pane, not shelling out — same philosophy as your other
  in-TUI flows). Edits apply on next load; no hot-reload needed
  mid-task.
- `/skill add <name>` — scaffold a new skill file from a template
  (empty frontmatter + placeholder sections for Main/Mini), then drop
  you into edit mode.
- `/skill delete <name>` — remove a skill file (with confirmation, same
  pattern as any destructive TUI action).

Content is written by whichever coding agent you're using day to day —
Triad doesn't generate skill content itself. `/skill add` just gives you
the scaffold and editing surface.

## 7. Observability

New TUI view (or extend `/trace` rather than invent a fourth
observability surface — recommend extending `/trace` since it's already
your flat cross-agent log): for each coding turn, show:

- The user message that triggered the turn
- Which skill(s) Coder selected
- Which tier (Main/Mini) actually got injected for each
- Token cost of what was injected that turn

This directly answers "why did Coder just do something DB-flavored when I
asked for a UI tweak" — a debugging need that doesn't exist today.

## 8. Open Questions (need a decision before/during build, not blocking scope)

- **Session boundary for "once per session":** does compaction (if/when
  you build context compaction) reset the loaded-skill tracking, forcing
  Main to re-fire post-compaction? Recommend: yes, tie it to the same
  context lifecycle as compaction, so a heavily-compacted long session
  doesn't run forever on a stale Mini-only understanding. Note: current
  model (`mimo-v2.5-free`, 200K context) makes this a low-urgency concern
  today — a session would need to be very long before Main/Mini skill
  injection meaningfully contributes to hitting that ceiling — but the
  design should still account for it before you build compaction later.
- **Reviewer and skills:** if Reviewer starts vetoing things that are
  actually correct-per-skill-convention (e.g. a project-specific pattern
  it doesn't recognize), does Reviewer get read-only visibility into
  which skill was active for that action, without receiving the skill
  content itself? Cheap to add, avoids false-positive objections.
- **Bad self-classification:** if Coder picks the wrong skill or misses
  an applicable one, is there a Reviewer-level or human-level override to
  force-load a specific skill mid-task? Recommend a manual `/skill force
  <name>` escape hatch for this session only.
- **Custom skill discovery:** confirmed design is "Coder sees all titles
  + descriptions and self-selects, including custom ones you've added" —
  no separate registration step needed beyond dropping the file in
  `skills/`. Loader just globs the directory each session start.

## 9. Non-Goals (explicit, to prevent scope creep)

- No auto-generation of skill content by Triad itself — a human (you,
  via your coding agent) writes it.
- No LLM-based fuzzy classification layer beyond Coder's own
  description-based self-selection — no separate classifier model/call.
- No skill versioning/history beyond what git already gives you (skill
  files live in the repo, git tracks changes).

## 10. Build Phases

Same numbering/checkbox convention as the other workflow docs — `- [ ]`
per subtask, checked off as your coding agent completes and verifies each
one. A phase is done only when every subtask under it is checked.

### Phase 1 — Skill File Format + Loader

**Goal:** a working, testable loader with no wiring into Coder's turn yet
— prove the file format and parsing logic in isolation first.

- [x] 1.1 — Define the `skills/*.md` frontmatter schema: `name`,
      `section`, `description`, `tier` (`main` | `mini`), `mini_ref`,
      `token_budget_main`, `token_budget_mini` — per §4
- [x] 1.2 — Implement the loader in a new `internal/skills` package:
      glob the `skills/` directory, parse YAML frontmatter + markdown
      body per file (reuse the same frontmatter-parsing approach as
      `internal/commands`, since it's the same convention)
- [x] 1.3 — Validate on load: reject duplicate `section` values across
      files — section:skill must stay 1:1 (§5). Surface a clear error,
      don't silently pick one.
- [x] 1.4 — Expose a Stage-1 accessor: returns the bare list of section
      labels only (no description, no body) — this is what gets shown
      to Coder every turn per §5
- [x] 1.5 — Expose a Stage-2 accessor: given a section name, returns
      that skill's description, Main body, and Mini body
- [x] 1.6 — **Manual test (temporary code, delete once verified):**
      hand-write 3 fake skill files (frontend/backend/db, minimal
      placeholder content), point the loader at them, print the
      Stage-1 label list and a Stage-2 lookup for each. Confirm parsing
      is correct and no field is dropped or mangled.
- [x] 1.7 — **Test — malformed input:** missing `mini_ref`, duplicate
      `section` values, malformed YAML frontmatter, an empty `skills/`
      directory — confirm each fails cleanly with a clear error, not a
      panic or silent skip
- [x] 1.8 — **Checkpoint:** delete temporary test code from 1.6 once
      passing; loader should now be usable standalone with no
      dependency on the approval loop or TUI yet

### Phase 2 — Selection + Injection Wiring

**Goal:** the two-stage funnel (§5) actually runs on every coding turn
and correctly decides Main vs. Mini — this is the core logic of the
whole workflow, budget real time here.

- [x] 2.1 — Wire the mandatory Stage 1 scan into Coder's turn (Triad
      mode) and into coding-subagent turns (Orchestrator mode) — no
      coding action may proceed without a section selection having
      happened first, per §5 step 1
- [x] 2.2 — Enforce the hard 3-section cap (§5): reject or truncate any
      selection beyond 3, no exceptions, even if the task plausibly
      spans more domains
- [x] 2.3 — Implement per-session loaded-set tracking: which sections
      have already had their Main Skill fire this session
- [x] 2.4 — Implement the Main-vs-Mini decision: first touch of a
      section this session → inject Main; every subsequent touch →
      inject Mini instead
- [x] 2.5 — Confirm Orchestrator itself never receives skill content —
      only its existing routing/orchestration context (§3, unchanged
      behavior — this is a regression check, not new logic)
- [x] 2.6 — Confirm Reviewer does not receive skill content either (§3)
- [x] 2.7 — Write each section-selection + tier-load decision to the
      transcript as a system entry — this is what Phase 4's
      observability work will read from
- [x] 2.8 — **Test — single domain:** a task touching only `frontend`
      loads frontend Main on first touch; a second frontend-touching
      turn later in the same session loads frontend Mini instead
- [x] 2.9 — **Test — multi-domain within cap:** a task touching
      frontend+backend+db (3 sections, at the cap) loads 3 distinct
      Main skills correctly in one turn, none skipped, none duplicated
- [x] 2.10 — **Test — cap enforcement:** deliberately construct a
      scenario where Coder's self-selection would naturally exceed 3
      sections; confirm the cap actually blocks the 4th rather than
      silently allowing it
- [x] 2.11 — **Checkpoint:** you should now be able to run a real
      multi-domain task and see correct Main/Mini injection happening,
      even with no `/skill` command or observability UI built yet
      (verify via logs/transcript inspection)

### Phase 3 — `/skill` Command Suite

**Goal:** you can view, edit, and manage skill files without leaving
the TUI, following the same conventions as `/plan`, `/diff`, `/undo`.

- [x] 3.1 — `/skill list` — show all skills with name, description,
      tier sizes, last-modified
- [x] 3.2 — `/skill view <name>` — show a skill's full Main and Mini
      content in the TUI
- [x] 3.3 — `/skill edit <name>` — inline TUI editing pane (not
      shelling out to an external editor) for Main or Mini body
- [x] 3.4 — `/skill add <name>` — scaffold a new skill file from a
      template (empty frontmatter + placeholder Main/Mini sections),
      then drop into edit mode
- [x] 3.5 — `/skill delete <name>` — remove a skill file, with a
      confirmation prompt matching the pattern used for other
      destructive TUI actions
- [x] 3.6 — `/skill force <name>` — manual override escape hatch to
      force-load a specific skill for the rest of the current session
      only (per §8 open question)
- [x] 3.7 — **Test:** add a new custom skill via `/skill add`, confirm
      it immediately shows up in the next session's Stage 1 section
      scan without any code change or restart beyond a new session
- [x] 3.8 — **UX pass:** confirm `/skill` follows the same interaction
      conventions as existing commands (`/plan`, `/diff`, `/undo`) —
      this is a consistency check, not new functionality

### Phase 4 — Observability

**Goal:** you can always see why a given skill fired, without guessing.

- [x] 4.1 — Extend `/trace` (do not build a new fourth observability
      surface) with: the triggering user message, section(s) selected,
      tier (Main/Mini) injected per section, and token cost of what was
      injected that turn
- [x] 4.2 — **Test:** trigger a mixed-domain task and confirm `/trace`
      correctly attributes each skill choice to the specific turn that
      caused it, not a prior or later turn
- [x] 4.3 — **Checkpoint:** you should be able to answer "why did Coder
      just do something DB-flavored when I asked for a UI tweak"
      purely by reading `/trace`, without needing to inspect raw logs

### Phase 5 — Starter Skills (frontend / backend / db)

**Goal:** ship real, usable skill content for this project — not just
the plumbing.

- [x] 5.1 — Author `frontend.md` (Main) + its Mini variant, scoped to
      this project's actual frontend surface if it has one, or as a
      reusable template if it doesn't yet
- [x] 5.2 — Author `backend.md` (Main) + its Mini variant, scoped to
      the Go/bubbletea/v2 conventions already locked in the project
      spec (tea.Cmd-only concurrency, package layout, etc.)
- [x] 5.3 — Author `db.md` (Main) + its Mini variant (template if the
      project has no DB surface yet)
- [x] 5.4 — Verify each Main file is actually within the 5-8k token
      budget and each Mini within 2-4k, using a real tokenizer count —
      not an estimate
- [x] 5.5 — **Test — end to end:** run one real task touching all 3
      domains in a single session; confirm Main fires exactly once per
      domain and Mini fires correctly on any repeat touch
- [x] 5.6 — **Checkpoint:** Workflow 5 is complete when a real
      multi-domain task, run start to finish, shows correct two-stage
      selection, correct Main/Mini tiering, working `/skill` management,
      and a `/trace` record that fully explains what happened and why