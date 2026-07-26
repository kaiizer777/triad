---
name: frontend
section: frontend
description: "Triad's TUI surface — bubbletea/v2 Model, lipgloss/v2 styling, viewport layout, keybindings, status bar, command palette. Condensed Mini variant for repeat touches."
tier: mini
---

# Triad Frontend (TUI) — Mini Skill

You already have the full `frontend` skill in this session (Main was
injected earlier). This is the condensed pointer version — for routine
work where you only need to recall the layout and the invariants.

## File map (where to look)

- `internal/tui/update.go` — `Init()` + central `Update(msg)` reducer. The
  ONLY state-mutation point.
- `internal/tui/view.go` — `View() tea.View`. Read-only over `Model`. Never
  mutate state here.
- `internal/tui/cmd.go` — `tea.Cmd` factories (LLM calls, tool execution,
  browser, git). All async I/O lives here.
- `internal/tui/msg.go` — `tea.Msg` types. New async work? Define the type
  here first.
- `internal/tui/model.go` — `Model` struct + pure helpers.
- `internal/tui/skill_editor.go` — inline TUI editor for `/skill edit`.
- `internal/tui/{persistence,tui}_test.go` — router + persistence tests.

## Hard invariants — DO NOT break

1. **`tea.Cmd` only.** No goroutines touching `Model` fields. No
   `sync.Mutex` around `m`. If you see one, that's a bug — restructure as
   a `Cmd`. (This bit us once. Don't reintroduce it.)
2. **No state mutation in `View()`.** Render may run speculatively;
   reading `m` is fine, writing is not.
3. **Speaker prefixes stay explicit** (`[You]:`, `[Coder]:`,
   `[Reviewer]:`, `[System]:`) in any transcript content the TUI
   generates.
4. **No hardcoded colors in new components.** Use the `Styles` /
   `speakerStyle` helpers in the existing theme. New `lipgloss.Color("#xxx")`
   literals go in the theme struct first.
5. **Transcript writes are append-only** via `transcript.Transcript.Append`
   or `.AppendMany`. Never rewrite a session file — append a correction
   entry instead.
6. **Tool calls go through the approval loop** in `internal/loop`, not
   directly from the TUI. The TUI displays proposals and executes
   approved tools; it does not invoke Coder-side tools itself.

## Layout (dual-column at width >= 75)

```
Title bar (1 line)
┌──────────────────┬─────────────────────────────────┐
│ Sidebar          │ Transcript viewport (scrollable) │
│ (32/36 cols)     │                                   │
│                  ├─────────────────────────────────┤
│                  │ Status bar (last line)            │
│                  ├─────────────────────────────────┤
│                  │ Prompt input (textarea)           │
└──────────────────┴─────────────────────────────────┘
```

Width breakpoints live in `view.go` — reuse them, don't duplicate.

## Keybindings (lock-in — flag changes to human)

- Enter — submit prompt
- Esc — cancel palette / inline editor
- Ctrl+C — quit (program level)
- Up/Down — scroll viewport (when input empty)
- PgUp/PgDn — page-scroll viewport
- Tab/Shift+Tab — focus transcript ↔ sidebar
- `/` — open slash-command palette (when input empty)

## Adding a new TUI feature — checklist

1. Define the `tea.Msg` type in `msg.go`.
2. Add a `tea.Cmd` factory in `cmd.go` (does the I/O, returns the msg).
3. Add the `Update` case (match msg, mutate `Model`, return new model +
   optional follow-up `Cmd`).
4. Render in `View` if visible (use `speakerStyle` / `Styles`).
5. Add a test in `tui_test.go` for nontrivial state transitions — don't
   pin `View()` byte-exact output (locks layout).

## Speaker badge helper

`speakerStyle(spk string) lipgloss.Style` — reuse for any new badge
component. Don't reimplement.

## Common pitfalls (all hit at least once)

- Forgetting `m.ready = true` after first `WindowSizeMsg` — TUI sticks on
  "Initializing Triad Studio...".
- Returning `nil` from a `Cmd` that has a real result — silently swallows
  errors. Always wrap in a `tea.Msg`.
- `tea.Batch` vs single `Cmd` — Batch = parallel, Cmd = sequential. Pick
  the right one.
- `fmt.Sprintf` for lipgloss in production render — use the style's
  `Render(...)` method so ANSI composes correctly.
- Skipping sidebar refresh after a state change — recompute the sidebar
  in the `Update` case that produced the change, don't rely on the next
  `View()` to re-derive it (breaks snapshots).
- Mutating a `[]Entry` while iterating it. Append-only doesn't mean
  "copy-on-write-free." If a render triggers a follow-up `Update` that
  appends, and the next render reads the same slice header, you get a
  stale or double-rendered view. The fix: `Update` returns a fresh
  `m`; `View` reads `m.entries` once at the top, never re-reads.
  Reaching for `sync.Mutex` to "fix" a render race means the fix is
  elsewhere.
- Embedding a pointer to a `tea.Cmd` result into a long-lived struct.
  Pass small messages (plain structs with value fields) and let
  `Update` do any heavy lifting after the message arrives.
- Calling `m.someField.SetContent(...)` from a `tea.Cmd` callback. The
  fields exposed by bubbletea components (textarea, viewport, list) are
  not concurrency-safe in the same way `Model` isn't. Pass the new
  content as a `tea.Msg`, let `Update` call `SetContent`.
- Stale OSC52 / clipboard integration. Don't block the render thread
  on the paste protocol — fire a `tea.Cmd` and return a
  `clipboardResultMsg`.
- Forgetting `m.quitting`. The TUI uses a `quitting` boolean to
  distinguish "user pressed Ctrl+C" from "I lost my terminal." Set it
  as the very first thing in the Ctrl+C handler, before any teardown
  work, otherwise a window resize mid-quit can leave the program in a
  half-shut-down state with goroutines still writing to a dead model.
- Trying to use `tea.Quit` directly from a sub-handler. `tea.Quit` is
  a sentinel message; emit it as the *return* of `Update`, not from a
  goroutine or a `tea.Cmd`'s body.
- Over-styling. The TUI looks worse with five lipgloss styles applied
  to one line than with one. Use a small palette
  (`Styles.Title`, `Styles.Muted`, `Styles.Accent`, `Styles.Border`,
  `Styles.Error`) and reach for them sparingly.
- Speaker badges that don't round-trip. If a tool result has a
  `[Coder]:` prefix in its content, do NOT also apply a `[Coder]`
  speaker badge via lipgloss — you'll get `[Coder]: [Coder]: ...` in
  the transcript. Render the badge from the `Entry.Speaker` field,
  never from the content prefix.
- Bubbletea v1 → v2 migration leftovers. v1 examples use
  `github.com/charmbracelet/bubbletea`; Triad is on
  `charm.land/bubbletea/v2` where `View()` returns `tea.View{}` not
  `string` and `tea.KeyMsg` is used directly (no `.String`). If a
  snippet has the v1 import path, it does not apply.
- Mouse events when the user didn't ask for them. Default TUI is
  keyboard-first. Opting into `tea.WithMouseCellMotion` /
  `tea.WithMouseAllMotion` introduces a category of edge cases (focus
  loss, terminal escape collisions with lipgloss, drag artifacts) the
  rest of the TUI does not handle. Stay keyboard unless a specific
  feature demands it.

## Patterns that work (copy these before inventing a new one)

- **`Update` returns the same `Model` value, every case.** No pointer
  receivers on `Update`. Always
  `func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd)`. The
  bubbletea contract is "return a new model"; the compiler lets you
  get away with pointer mutation, but it makes the data flow
  impossible to follow and breaks `teatest` snapshots.
- **All `tea.Cmd` factories take their dependencies as parameters.**
  No package-level `var client = ...` mutable globals. Signature:
  `cmdFoo(tr *transcript.Transcript, c loop.AgentClient, ...) tea.Cmd`
  — pass everything in, no `init()`-time wiring.
- **One `tea.Msg` per result.** Don't return `(result, error)` as a
  single message; return `xxxResultMsg{Result, Err}` and let `Update`
  switch on the err. Keeps bubbletea dispatch flat and the error path
  uniform.
- **State slices with `append` are owned by `Model`.** Don't pass a
  `*[]Entry` to a helper that appends and call it "clean
  refactoring." The append happens in `Update`, full stop. Helper
  computes the new entry, returns it; `Update` appends.
- **`View` reads `m` exactly once, top to bottom.** Build local
  variables for the parts of `m` it needs (`entries := m.entries`,
  `width := m.width`, etc.), then use the locals. Re-reading `m.x`
  inside a conditional in `View` is how "render flickers" happen.

## Worked example — adding a sidebar badge

**Task:** "Show the most recent `[You]:` message in the
sidebar's top section, in muted style, truncated to one line."

No new `tea.Msg` (data is local). No `tea.Cmd`. Just a render
helper change:

```go
func (m Model) renderSidebar(width int) string {
    var lines []string
    // existing sidebar sections...
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

`truncateToWidth` is `runewidth`-aware, not a byte slice. CJK
and emoji width differently; naive `text[:n]` truncation
visibly misaligns. Test the empty-transcript case explicitly
(blank badge, not a panic).

## Worked example — adding a "scroll to bottom" keybinding

**Task:** "Pressing `G` in the transcript viewport scrolls
to bottom — same as auto-scroll on new entries."

No new `tea.Msg`. No `tea.Cmd`. Just an `Update` case:

```go
case "G":
    if m.focus == focusTranscript {
        m.viewport.GotoBottom()
        return m, nil
    }
```

Note the focus check — `G` should not steal the keystroke if
the user is currently editing the prompt input. Test in
`tui_test.go`: scroll up, send `tea.KeyMsg` "G", assert
`viewport.AtBottom()`.

This is a "small" change even though it touches `Update`,
`View` (via viewport state), and tests: data is local, side
effect is a single viewport method, user-visible change is
one keystroke. The "add a feature" checklist exists so a
30-line change doesn't accidentally bypass review — not so
trivial changes get bogged down in ceremony. Skip the steps
that don't apply, do the ones that do.

## What this skill does NOT cover

- Agent loop, approval state machine, transcript persistence → `backend`
  skill. If you're touching `internal/loop`, `internal/agent`,
  `internal/subagent`, or `internal/transcript`, also select `backend`.
- Slash-command content (`commands/*.md`) — not a TUI concern, edit
  directly.
- Skill file format / loader — `backend` (Workflow 5 Phase 1).

`frontend` alone is correct only for: keybindings, rendering, layout,
status bar, sidebar, prompt input, command palette, inline editors. The
moment a task needs an agent call, an approval-state change, or a
transcript write going through non-`Append*` paths, add `backend` to the
selection.
