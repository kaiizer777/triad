---
name: frontend
section: frontend
description: "Triad's TUI surface — bubbletea/v2 Model, lipgloss/v2 styling, viewport layout, keybindings, status bar, and the in-TUI command palette. Pick this for any task touching files in internal/tui/* or the visible behavior of the terminal app."
tier: main
mini_ref: frontend-mini.md
token_budget_main: 6500
token_budget_mini: 3000
---

# Triad Frontend (TUI) — Main Skill

You are working on the **TUI layer of Triad itself**, not a generic frontend
project. The terminal is the product. Everything below assumes you already
loaded the `backend` skill (the agent loop) in the same session, OR that the
task is pure-TUI with no loop changes. If the task crosses both, you should
have both `frontend` and `backend` selected (2 of 3 sections).

Triad's TUI is built on **bubbletea/v2** (`charm.land/bubbletea/v2`) and
**lipgloss/v2** (`charm.land/lipgloss/v2`). It is a single `Model` struct
(`internal/tui/update.go`) that owns all state. There is no React, no DOM, no
CSS — the "render" function returns a `tea.View` string composed of
lipgloss-styled lines.

## File map (read before editing anything in `internal/tui/`)

- `internal/tui/update.go` — `Init()` and the central `Update(msg)` reducer.
  Every keypress, window resize, and async result lands here. This is the
  single state-mutation point. **Do not** start goroutines that touch `m`
  directly — every async op dispatches via a `tea.Cmd` (see "tea.Cmd only"
  below).
- `internal/tui/view.go` — `View() tea.View` renders the current `Model` into
  the terminal. Read-only over `m`; never mutate state here. If you find
  yourself "saving a value during render", you're doing it wrong — push it
  through `Update` instead.
- `internal/tui/cmd.go` — `tea.Cmd` factories: `cmdCoderTurn`, `cmdReviewerTurn`,
  `cmdRunCommand`, `cmdBrowser*`, and so on. Each is a `func() tea.Msg`
  that performs the async work and returns a single message for `Update` to
  consume. **All LLM HTTP calls and shell execution live here**, not in
  `Update` itself.
- `internal/tui/msg.go` — the `tea.Msg` types (`coderTurnResultMsg`,
  `reviewerVerdictMsg`, `toolResultMsg`, `windowSizeMsg`, `keyEnterMsg`, …).
  Adding a new `tea.Msg`? Add the type here and a case in `Update`.
- `internal/tui/model.go` — the `Model` struct definition plus pure helpers
  (`nextID`, `appendEntry`, viewport scroll math, sidebar geometry). Pure
  helpers only — anything that talks to the network or filesystem belongs in
  `cmd.go`.
- `internal/tui/skill_editor.go` — the inline TUI text editor for `/skill edit
  <name>`. Reused by the Phase 3 `/skill` command suite. Worth reading before
  adding any new "inline editor" surface — same conventions apply.
- `internal/tui/persistence_test.go`, `tui_test.go` — unit tests for the
  message router and persistence. Use `teatest` if you add redux-style
  coverage.

## Hard invariants — never violate

1. **`tea.Cmd`-only concurrency.** Async work (LLM calls, `git commit`,
   `git revert`, Playwright commands, file I/O for transcript flush) MUST
   dispatch as a `tea.Cmd` and return a single `tea.Msg`. No `go func() { m.x
   = ... }()` patterns. No `sync.Mutex` around `Model` fields. If a Reviewer
   pattern ever shows up touching `Model` from a goroutine, it's a bug —
   restructure as a `Cmd`.
2. **No state mutation in `View()`.** `View` runs on every render and is
   allowed to be called speculatively. Reading `m` is fine; writing `m.x =`
   is a render-time side effect and will cause "value flickers" or lost
   updates under repaint.
3. **Speaker prefixes stay explicit.** Transcript entries written by the TUI
   (e.g. when synthesizing a System note) must include `[Coder]:` / `[You]:`
   / `[Reviewer]:` / `[System]:` literally. OpenAI-style `role` fields alone
   don't carry identity in the shared transcript — agents reading prior
   turns need the visible prefix to know who said what.
4. **No hardcoded colors that fight the lipgloss theme.** The TUI pulls its
   palette from a single `Styles` struct (`model.go` or a `styles.go` file).
   If you find yourself writing `lipgloss.Color("#FF00AA")` in a new
   component, lift it into the theme struct first. Hardcoded colors are how
   dark-mode contrast regresses silently.
5. **Transcript writes are append-only.** Anything you write to the session
   JSONL goes through `transcript.Transcript.Append` or `.AppendMany`. Never
   rewrite a session file to "fix" an entry — append a correction entry
   instead. This is project-wide, not TUI-specific, but the TUI is the most
   common place new code does it.
6. **Tool calls go through the approval loop.** If the TUI is calling a
   Coder-side tool (file edit, command run, browser) directly without
   Reviewer sign-off, you've bypassed the project's most important
   invariant. The TUI may *display* the proposal and *execute* the
   Reviewer-approved tool, but the proposal/approve/execute handoff lives
   in `internal/loop`, not the TUI.

## Layout conventions

The TUI uses a **dual-column** layout (sidebar + main) when `width >= 75`,
and a single-column stacked layout below that. The exact breakpoints are
in `view.go`'s `width` ladder — do not duplicate them in new components.
If you need a width-aware style, branch on the same `width` thresholds the
existing sidebar does.

```
┌────────────────────────────────────────────────────┐
│ Title bar (1 line)                                  │
├──────────────────┬─────────────────────────────────┤
│ Sidebar          │ Transcript viewport (scrollable) │
│ (32 / 36 cols)   │                                   │
│                  │                                   │
│                  ├─────────────────────────────────┤
│                  │ Status bar (last line)            │
│                  ├─────────────────────────────────┤
│                  │ Prompt input (textarea)           │
└──────────────────┴─────────────────────────────────┘
```

Speaker badges (`[You]`, `[Coder]`, `[Reviewer]`, `[System]`) are styled via
a small `speakerStyle(spk string) lipgloss.Style` helper. Reuse it — don't
reimplement per-component badge styles. The viewport auto-scrolls to bottom
on new entries unless the user has scrolled up; preserve that behavior when
adding new entry types.

## Keybindings (lock-in — do not rebind casually)

These are wired in `Update`'s `tea.KeyMsg` case and exercised by the
existing TUI tests. Changing them is a UX-level decision; flag the change
in your proposal and let the human approve via Reviewer before doing it.

- **Enter** — submit prompt (or accept the in-progress command if `slashMode`)
- **Esc** — cancel current slash-command palette / inline editor
- **Ctrl+C** — quit (handled at the program level, not in `Update`)
- **Up / Down** — scroll transcript viewport (only when input is empty)
- **PgUp / PgDn** — page-scroll the viewport
- **Tab / Shift+Tab** — switch focus between transcript and sidebar (when
  sidebar is visible)
- **`/`** — open the slash-command palette (when input is empty)

If you add a new command (`/foo`), wire it in `internal/commands` (so
`/help` sees it) AND add the keybinding in `Update` if it has a hotkey —
don't leave a `/foo` that's only reachable by typing the literal `/foo`.

## Adding a new TUI feature — checklist

When Reviewer approves a "add feature X to the TUI" task, do the following
in this order — this is the same shape every TUI feature has followed so
far, so deviating is a flag to call out to the human:

1. **Define the message type first.** New async work? Add a `xxxMsg` struct
   in `msg.go` with a `tea.Msg` marker. The fields it carries are your
   contract with the reducer.
2. **Add a `tea.Cmd` factory in `cmd.go`.** Returns the new `tea.Msg`. All
   I/O happens here, not in `Update`.
3. **Add the `Update` case.** Match the new `tea.Msg`, mutate `Model`
   accordingly, and return a new `Model` + optional follow-up `tea.Cmd`.
4. **Render in `View` if it has visible output.** Use existing
   `speakerStyle` / `Styles` helpers — do not invent new color literals.
5. **Add a test.** `tui_test.go` covers the router; add a case for the new
   `tea.Msg` if it has nontrivial state transitions. Avoid testing
   `View()`'s exact bytes — that locks the layout. Test the state
   transition, not the rendering.

## Common pitfalls (these have all been hit at least once)

- **Forgetting to mark the model "ready" on `WindowSizeMsg`.** The TUI shows
  "Initializing Triad Studio..." until `m.ready = true`. If you wire a new
  init path, set ready after the first non-zero `WindowSizeMsg`.
- **Returning `nil` from a `Cmd` that should fire-and-forget.** `nil`
  means "no follow-up command". A `tea.Cmd` that performs I/O and then
  returns `nil` silently swallows errors. Always wrap the error in a
  message and let `Update` decide what to render.
- **Mixing `tea.Cmd` and `tea.Batch`.** A `Batch` runs commands in
  parallel; a single `Cmd` runs sequentially. If your feature "fires two
  things at once and waits for both", that's a `Batch`. If "fires one
  thing, then based on its result fires another", that's a returned `Cmd`
  inside the first's handler.
- **Using `fmt.Sprintf` for lipgloss styles instead of `Render` or
  `Render(strings…)`.** Sprintf is fine for test code, but the production
  render path should call the style's `Render(...)` so the ANSI escapes
  compose correctly.
- **Skipping the sidebar refresh.** The sidebar shows session state
  (current model, loaded skills, transcript length). After any state
  change, if the sidebar should reflect it, recompute it in the `Update`
  case that produced the change — don't rely on the next `View()` call
  to re-derive it lazily, because that breaks test snapshots.
- **Mutating a `[]Entry` while iterating it.** Append-only doesn't mean
  "copy-on-write-free". If a render pass triggers a follow-up `Update`
  that appends, and the next render reads the same slice header, you
  get a stale or worse double-rendered view. The pattern that works:
  `Update` returns a fresh `m` value; `View` reads `m.entries` once at
  the top and never re-reads it. If you find yourself reaching for
  `sync.Mutex` to "fix" a render race, the actual fix is to stop
  reading the slice after the first read.
- **Embedding a pointer to a `tea.Cmd` result into a long-lived struct.**
  The result message is meant to flow through `Update` and be
  consumed there. If a `tea.Cmd` returns a `*bigResult` and you store
  it on `Model`, the next `Update` case might find a stale pointer or
  race against the goroutine that produced it. Pass small messages
  (plain structs with value fields) and let `Update` do any heavy
  lifting synchronously after the message arrives.
- **Calling `m.someField.SetContent(...)` from a `tea.Cmd` callback.**
  The fields exposed by bubbletea components (textarea, viewport,
  list) are not concurrency-safe in the same way `Model` itself
  isn't. The right pattern: pass the new content as a `tea.Msg`,
  let `Update` call `SetContent`. The component will re-render on
  the next `View()`.
- **Stale clipboard / OSC52 integration.** If you wire a copy-to-
  clipboard feature, do NOT block the render thread on the paste
  protocol — fire a `tea.Cmd` that performs the OSC52 write
  asynchronously and returns a `clipboardResultMsg`. The render
  thread should not wait on a network or terminal-protocol round
  trip.
- **Forgetting `m.quitting`.** The TUI uses a `quitting` boolean to
  distinguish "user pressed Ctrl+C" from "I lost my terminal".
  Without it, a window resize mid-quit can leave the program in a
  half-shut-down state with goroutines still trying to write to a
  dead model. Set `m.quitting = true` as the very first thing in
  the Ctrl+C handler, before any teardown work.
- **Trying to use `tea.Quit` directly from a sub-handler.** `tea.Quit`
  is a sentinel message; emit it as the *return* of `Update`, not
  from a goroutine or a `tea.Cmd`'s body. The runtime knows to stop
  only when `Update` returns it.
- **Over-styling.** The TUI looks worse with five lipgloss styles
  applied to one line than with one. Use a small palette
  (`Styles.Title`, `Styles.Muted`, `Styles.Accent`, `Styles.Border`,
  `Styles.Error`) and reach for them sparingly. A line that needs
  all five to read correctly is a line that needs a structural
  rewrite, not another style pass.
- **Speaker badges that don't round-trip.** If a tool result has a
  `[Coder]:` prefix in its content, do NOT also apply a `[Coder]`
  speaker badge via lipgloss — you'll get `[Coder]: [Coder]: ...`
  in the transcript. Render the badge from the `Entry.Speaker`
  field, never from the content prefix. This bit us during the
  journey-view work.
- **Bubbletea v1 → v2 migration leftovers.** If you copy-paste a
  bubbletea v1 example from the web, the import path is wrong
  (Triad uses `charm.land/bubbletea/v2`) and `View()` returns
  `tea.View{}` not `string`. The other common v1 leftover is
  `tea.KeyMsg.String` — it's `tea.KeyMsg` directly in v2. Search
  the codebase for the v2 import first; if your snippet uses
  `github.com/charmbracelet/bubbletea`, it does not apply.
- **Mouse events when the user didn't ask for them.** The default
  TUI is keyboard-first. If you opt into `tea.WithMouseCellMotion`
  or `tea.WithMouseAllMotion`, you're signing up for a category of
  edge cases (focus loss, terminal escape sequence collisions with
  lipgloss, dragging artifacts) that the rest of the TUI does not
  handle. Stay keyboard unless a specific feature demands it.

## Patterns that work (read these before inventing a new one)

These are the patterns the codebase has settled into. If you're
about to do something close to one of these, copy the existing
implementation rather than designing a new one.

- **`Update` returns the same `Model` value, every case.** No
  pointer receivers on `Update`. The bubbletea contract is
  "return a new model"; the compiler will let you get away with
  pointer mutation, but it makes the data flow impossible to
  follow and breaks `teatest` snapshots. Always
  `func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd)`.
- **All `tea.Cmd` factories take their dependencies as
  parameters.** No package-level `var client = ...` mutable
  globals. The factory signature is
  `cmdFoo(tr *transcript.Transcript, c loop.AgentClient, ...) tea.Cmd`
  — pass everything in, no `init()`-time wiring.
- **One `tea.Msg` per result.** Don't return `(result, error)` as
  a single message; return a single `xxxResultMsg{Result, Err}`
  struct and let `Update` switch on the err. This keeps the
  bubbletea dispatch flat and the error path uniform across the
  whole app.
- **State slices with `append` are owned by `Model`.** Don't pass
  a `*[]Entry` to a helper that appends and call it "clean
  refactoring." The append happens in `Update`, full stop. If a
  helper needs to compute a new entry, it returns the entry;
  `Update` appends it.
- **`View` reads `m` exactly once, top to bottom.** Build local
  variables for the parts of `m` it needs (`entries := m.entries`,
  `width := m.width`, etc.), then use the locals. Re-reading `m.x`
  inside a conditional in `View` is the standard way to get
  "render flickers" where the same line shows two different values
  across one paint.

## What this skill does NOT cover

- The Coder / Reviewer / Subagent agent logic itself — that's `backend`.
  Touching `internal/loop`, `internal/agent`, or `internal/subagent` from
  a frontend-selected turn? Add `backend` to the selection.
- Transcript file format / append-only persistence — that's `backend`
  too (it lives in `internal/transcript`, which is loop-adjacent).
- Slash-command content (the `commands/*.md` files) — not a TUI concern;
  edit them directly when needed, they reload on next session.
- Skill file format and the loader — `backend` (Phase 1 of Workflow 5).

If the task only touches the TUI (keybindings, rendering, layout,
status-bar, sidebar, prompt input, command palette), `frontend` alone is
correct. If you're about to add a new agent call, change the approval
state machine, or modify how transcript entries are persisted, switch on
`backend` as well.

## Worked example — adding a "current task" badge to the sidebar

Concrete walkthrough of the "add a feature" checklist above, so
the abstract steps have a concrete shape.

**Task:** "Show the most recent `[You]:` message in the sidebar's
top section, in muted style, truncated to one line."

**Step 1 — Define the message type.** No new `tea.Msg` needed —
the data is already in `m.transcript.Entries`. The change is
purely a `View`-side computation. (If the data needed a network
round trip, the first step would be a new `tea.Msg`.)

**Step 2 — `tea.Cmd` factory.** None. The data is local.

**Step 3 — `Update` case.** None. The badge is computed during
render.

**Step 4 — Render in `View`.** In the `renderSidebar` helper
(extract it from `view.go` if not already extracted):

```go
func (m Model) renderSidebar(width int) string {
    var lines []string
    // existing sidebar sections...

    // New: most-recent You message
    for i := len(m.entries) - 1; i >= 0; i-- {
        e := m.entries[i]
        if e.Speaker == transcript.SpeakerYou {
            text := truncateToWidth(e.Content, width-4)
            lines = append(lines, styles.Muted.Render(text))
            break
        }
    }
    return styles.Sidebar.Render(strings.Join(lines, "\n"))
}
```

Note: `truncateToWidth` is a `runewidth`-aware helper, not a
byte slice. Width is the lipgloss column count, not the byte
count. CJK characters and emoji width differently — naive
`text[:n]` truncation will visibly misalign.

**Step 5 — Test.** Add a test that constructs a `Model` with
3 entries (`[You]:`, `[Coder]:`, `[You]:`), calls
`renderSidebar(40)`, and asserts the second `[You]:` content
appears in the output, not the first. Use a `runewidth`-aware
assertion or compare a stable substring.

**Common mistake on this exact change:** forgetting to handle
the empty-transcript case. If `m.entries` is empty, the loop
returns nothing and the badge area is just blank. Decide
explicitly whether blank is correct (yes, it is — a blank
badge is better than a panic), but at least handle it.

## Worked example — adding a new keybinding for "scroll to bottom"

**Task:** "When the user presses `G` (shift-g) in the transcript
viewport, scroll to the bottom — the same behavior the
auto-scroll already does on new entries."

**Step 1 — Define the message type.** None. This is a
synchronous `tea.KeyMsg` handler.

**Step 2 — `tea.Cmd` factory.** None.

**Step 3 — `Update` case.** In the `tea.KeyMsg` case, add:

```go
case "G":
    if m.focus == focusTranscript {
        m.viewport.GotoBottom()
        return m, nil
    }
```

Note the focus check — `G` should not steal the keystroke if the
user is currently editing the prompt input.

**Step 4 — Render in `View`.** No change. The viewport state
already drives render.

**Step 5 — Test.** Add a test in `tui_test.go` that:
1. Constructs a `Model` with N entries that exceed viewport
   height.
2. Scrolls the viewport up via `m.viewport.LineUp(5)`.
3. Sends a `tea.KeyMsg` with type "G" and value "G".
4. Asserts the viewport's `AtBottom()` returns true.

**Why this is a "small" change** even though it touches `Update`,
`View` (via viewport state), and tests: the data is local, the
side effect is a single viewport method call, and the user-
visible change is one keystroke. The "add a feature" checklist
exists so a 30-line change doesn't accidentally bypass
review — not so trivial changes get bogged down in ceremony.
Skip the steps that don't apply, do the ones that do.
