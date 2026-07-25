# Triad — Build Workflow

**Companion to:** PROJECT_SPEC.md
**Purpose:** Ordered, practical build sequence — what to build, in what order, with
what libraries/APIs, based on current (July 2026) tooling.
**Stack confirmed:** Go + `bubbletea` v2 (TUI) + flat JSON files (persistence) +
OpenCode Zen (free-tier, OpenAI-compatible API — MiMo V2.5-free model, used for
both agents for now)
**Status:** Project scaffolding complete (module init, directory structure,
`.gitignore`, empty `main.go` all in place). This document picks up from there.

---

## Build Order (Do Not Skip Ahead)

The point of this order: get a **headless, working core loop first** (no TUI,
just terminal prints), prove the hard logic works, *then* wrap it in a TUI.
Building the TUI first means debugging agent logic and rendering bugs at the
same time — avoid that.

### Phase 1 — Transcript + Config (foundation, no API calls yet)

**Goal:** a working, testable data layer with zero network calls involved.

- [x] 1.1 — Define `Entry` struct in `internal/transcript/entry.go`:
      `ID int`, `Speaker string` (`"You" | "Coder" | "Reviewer" | "System"`),
      `Type string` (`"message" | "proposed_action" | "action_result"`),
      `Content string`, `Timestamp time.Time` — per spec §6.1
- [x] 1.2 — Define `Transcript` type in `internal/transcript/transcript.go`:
      wraps `[]Entry` plus a mutex if you expect concurrent access later
      (safe to add now, cheap insurance for Phase 5/6)
- [x] 1.3 — Implement `Append(entry Entry)` — adds to the in-memory slice
      and immediately writes just that one line to the open session file
      (append-only), rather than rewriting the whole file each time
- [x] 1.4 — Implement `SaveToFile(path string) error` and
      `LoadFromFile(path string) (*Transcript, error)` using **JSON Lines**
      format (one JSON object per line) — easy to append to, easy to `tail -f`
      while debugging
- [x] 1.5 — Define `AgentConfig` struct in `internal/agent/config.go`:
      `Name string`, `BaseURL string`, `APIKey string`, `Model string`,
      `HasTools bool` — per spec §6.2
- [x] 1.6 — Implement config loading: read `base_url`, `api_key`, `model` from
      `config.yaml` (using `gopkg.in/yaml.v3`) or `.env` (using
      `github.com/joho/godotenv` or plain `os.Getenv`) — pick one and be
      consistent; build two `AgentConfig` values from it (Coder: `HasTools:
      true`, Reviewer: `HasTools: false`)
- [x] 1.7 — **Manual test (temporary code in `main.go`, delete once verified):**
      create 3–4 fake `Entry` values by hand, `Append` them to a `Transcript`,
      `SaveToFile("sessions/test.jsonl")`, then `LoadFromFile` the same path
      and print the result. Confirm the round-trip preserves order, content,
      and timestamps exactly.
- [x] 1.8 — **Checkpoint:** delete the temporary test code from `main.go` once
      1.7 passes, but keep `sessions/test.jsonl` around as a manual reference
      example if useful

### Phase 2 — Single Agent, Plain Text (no tools yet)

**Goal:** prove you can talk to OpenCode Zen and get a coherent reply back,
before adding any tool-calling complexity.

- [x] 2.1 — Write the request-building function in `internal/agent/client.go`:
      takes an `AgentConfig` and `[]Entry`, returns the JSON body for an
      OpenAI-compatible `/chat/completions` request
- [x] 2.2 — Map transcript entries to chat message roles: `You` → `"user"`,
      `Coder`/`Reviewer` → `"assistant"`. Since OpenAI-style roles don't
      natively support 3-way speaker attribution, prefix message content with
      the speaker tag explicitly, e.g. `"[Coder]: I'll create..."`, so the
      model can tell who said what from context alone
- [x] 2.3 — Write the actual HTTP call: POST to
      `config.BaseURL + "/chat/completions"` with the `Authorization: Bearer
      <APIKey>` header, using `net/http` directly (no SDK needed)
- [x] 2.4 — Parse the response: extract `choices[0].message.content` for a
      plain-text reply (tool_calls parsing comes in Phase 3)
- [x] 2.5 — Wrap 2.1–2.4 into a single function, e.g.
      `func (c *Client) Respond(cfg AgentConfig, transcript []Entry) (string, error)`
- [x] 2.6 — **Test:** call this function with Reviewer's config (no tools,
      simplest case) against a static, hand-written test transcript (e.g. just
      a `You` message asking a simple question). Print the raw response to
      stdout.
- [x] 2.7 — **Verify model ID:** if the call fails with a model-not-found or
      auth-style error, double check the exact model string via OpenCode's
      `/models` command or the Zen dashboard — `mimo-v2.5-free` may need an
      exact prefix/casing you haven't confirmed yet
- [x] 2.8 — **Checkpoint:** you should now be able to hold a basic, one-off
      "ask Reviewer a question, get an answer" exchange purely from the
      terminal, no transcript looping yet

### Phase 3 — Tool Calling for Coder

**Goal:** Coder can actually read/write files and run shell commands, gated
behind the model's own tool-call decisions.

- [x] 3.1 — Define the 3 tool schemas in `internal/agent/tools.go` as Go
      structs matching OpenAI's function-calling JSON schema format:
      `write_file(path, content)`, `read_file(path)`, `run_command(command)`
- [x] 3.2 — Attach the tool schema array to the request body **only when
      `cfg.HasTools == true`** — Reviewer's calls should never include it
- [x] 3.3 — Update response parsing to detect `tool_calls` in
      `choices[0].message` — this is a separate field from `.content`, and a
      response can contain one without the other
- [x] 3.4 — Implement `write_file`/`read_file` executors: plain
      `os.WriteFile`/`os.ReadFile`. Resolve the given path relative to a fixed
      project working directory; reject absolute paths or `..` traversal
      segments as a basic safety floor (cheap, doesn't violate the "no human
      gate" decision — just stops accidental filesystem escape outside the
      project)
- [x] 3.5 — Implement `run_command` executor: `exec.Command("cmd", "/C",
      command)` with `Dir` set to the project working directory, capturing
      stdout, stderr, and exit code separately
- [x] 3.6 — Wire tool execution results back as a new `Entry` with
      `Type: "action_result"`, containing the captured output
- [x] 3.7 — **Test:** manually construct a transcript that should provoke
      Coder into calling `write_file` (e.g. `"[You]: Create a file called
      hello.txt with the text 'hello world' in it."`), run it through
      `Respond`, confirm the tool call is correctly parsed and the file
      actually appears on disk with the right content
- [x] 3.8 — **Test each tool independently** before moving on — don't assume
      `run_command` works just because `write_file` did; they exercise
      different code paths

### Phase 4 — The Approval Loop (the hard part — budget real time here)

**Goal:** the actual coder-proposes / reviewer-checks / execute-or-revise
cycle, working correctly on repeated real attempts, not just one lucky run.

- [x] 4.1 — Implement the core loop in `internal/loop/loop.go`, matching spec
      §6.3 exactly: Coder proposes an action → append `proposed_action` entry
      → Reviewer checks the full transcript including that entry → approve
      (execute the tool, append `action_result`) or object (append the
      objection as a `message` entry, do **not** execute)
- [x] 4.2 — On objection, loop back to Coder on the *same* atomic action —
      Coder should see Reviewer's specific objection in the transcript and
      revise its next `proposed_action` accordingly, not start over from
      scratch
- [x] 4.3 — Add a loop guard: cap the number of propose→object cycles on a
      single atomic action (e.g. 5) so a disagreement that never resolves
      doesn't spin forever — on hitting the cap, surface it to the human
      rather than silently giving up
- [x] 4.4 — Implement "done" detection: define a clear signal Coder uses to
      indicate the whole task is complete (e.g. a specific phrase, or a
      dedicated `task_complete` tool with no arguments) and have Reviewer
      explicitly confirm before the session transitions to idle
- [x] 4.5 — Implement idle-state handling: once confirmed done, the loop stops
      calling Coder/Reviewer and simply waits for the next human-provided task
      (read from stdin for now — TUI input comes in Phase 6)
- [x] 4.6 — **Test — happy path:** run a real small task end to end (e.g.
      "create a hello.txt file with 'hello world' in it") from a single
      `main.go` entrypoint, using plain `fmt.Println` output, no TUI. Confirm
      propose → approve → execute → done → idle all work correctly in
      sequence.
- [x] 4.7 — **Test — objection path:** deliberately give an ambiguous or
      under-specified task (e.g. "add rate limiting" with no detail) that
      should provoke Reviewer into objecting on the first attempt. Confirm the
      objection actually blocks execution and Coder visibly revises before
      anything gets written to disk.
- [x] 4.8 — **Test — loop guard:** try to deliberately trigger the cap from
      4.3 (e.g. by feeding Reviewer instructions to always object) and confirm
      it surfaces cleanly instead of hanging

### Phase 5 — Human Interjection (still headless)

**Goal:** you can type into the running session at any point, not just at the
very start of a task.

- [x] 5.1 — Add a background goroutine that reads lines from stdin and sends
      them into a Go channel, rather than blocking the main loop on
      `bufio.Scanner` directly
- [x] 5.2 — In the main loop, `select` between "a human line arrived on the
      channel" and "it's time for the next agent turn" — a human message
      should be appended to the transcript as soon as it arrives, regardless
      of whose turn it currently is
- [x] 5.3 — Confirm ordering: if you type mid-loop, your message should appear
      in the transcript at the point you sent it, and both agents should see
      it framed correctly (as a `You` message) on their very next call
- [x] 5.4 — **Test:** start a multi-step task, deliberately type an
      interjection partway through (e.g. "wait, use PostgreSQL not MySQL"),
      confirm Coder's next proposed action reflects it
- [x] 5.5 — **Checkpoint:** this is the trickiest concurrency piece before
      introducing the TUI — get it fully correct here, in a throwaway terminal
      version, since bubbletea layers its own event/concurrency model on top
      (Phase 6) and you don't want to debug both at once

### Phase 6 — TUI (bubbletea)

**Important architecture note:** bubbletea v2 (`charm.land/bubbletea/v2`) uses
the Elm Architecture (`Model` / `Init` / `Update` / `View`) and its own
concurrency primitive, **Commands** (`tea.Cmd`). Do not spawn raw goroutines
that mutate the `Model` directly — bubbletea's own docs warn against this
explicitly. Long-running work (an API call, waiting on stdin) must be wrapped
as a `tea.Cmd` that runs in the background and sends a `tea.Msg` back into
`Update` when it completes. This is a different concurrency style from Phase
4/5's headless loop — expect to **adapt the logic, not directly port the
code.**

- [x] 6.1 — Define the bubbletea `Model` struct in `internal/tui/model.go`:
      holds the `Transcript` (for rendering), a `viewport.Model` (from
      `bubbles`) for scrollback, and a `textinput.Model` for the input box
- [x] 6.2 — Define your custom `tea.Msg` types: e.g. `agentResponseMsg`,
      `toolResultMsg`, `humanInputMsg` — these are what your background `Cmd`s
      send back into `Update` when work completes
- [x] 6.3 — Implement `Init()`: kick off the first `tea.Cmd` (e.g. "wait for
      the next agent turn" or "wait for human input", depending on session
      state at startup)
- [x] 6.4 — Implement `Update()` in `internal/tui/update.go`: handle key
      presses (typing into the input box, Enter to send), and handle each
      custom `Msg` type by updating the `Model` and returning the *next*
      `tea.Cmd` in the sequence
- [x] 6.5 — Re-express the Phase 4 approval loop as a **chain of `tea.Cmd`
      functions** rather than a blocking `for` loop: "Coder proposes" is a
      `Cmd` that calls the agent client and returns a `Msg`; `Update` receiving
      that `Msg` triggers the "Reviewer checks" `Cmd`; and so on
- [x] 6.6 — Implement `View()` in `internal/tui/view.go`: render the
      transcript inside the viewport, color-coded by speaker using
      `charm.land/lipgloss/v2`, with the input box docked at the bottom
- [x] 6.7 — Wire human input: typing should always update the `textinput`
      component regardless of session state; pressing Enter sends a
      `humanInputMsg` that gets appended to the transcript immediately, same
      behavior as Phase 5 but now routed through bubbletea's event loop
      instead of a raw stdin goroutine
- [x] 6.8 — **Test:** run the same "create hello.txt" task from Phase 4.6
      through the actual TUI. Confirm the transcript scrolls live, speaker
      colors are distinct and legible, and the input box works at any point in
      the cycle
- [x] 6.9 — **Test:** re-run the Phase 4.7 objection-path and Phase 5.4
      interjection scenarios through the TUI specifically, since the
      Cmd-based rewiring in 6.5 is where subtle behavior differences from the
      headless version are most likely to surface

### Phase 7 — UI Polish (OpenCode-style TUI upgrade)

**Goal:** make the terminal interface feel premium — closer to tools like
OpenCode or CommandCode, not a plain scrolling log.

- [x] 7.1 — **Spinner while agents think:** add a `charm.land/bubbles/v2/spinner`
      component to the Model; start it when a `cmdCoderTurn` or
      `cmdReviewerTurn` fires and stop it when the `agentResponseMsg` arrives.
      Display it inline in the status bar so the user always knows something
      is happening rather than staring at a frozen screen
- [x] 7.2 — **Two-panel layout:** split the terminal horizontally using
      `lipgloss.JoinHorizontal` — a narrow left sidebar (≈28 cols) showing
      session metadata (session ID, working directory, current state, retry
      counter) and a wider right panel for the transcript viewport. Both
      panels should have a `lipgloss.RoundedBorder()` for visual separation
- [x] 7.3 — **Styled input bar:** replace the bare `textinput` prompt with a
      full-width bordered box at the bottom, similar to how OpenCode renders
      its input — a left label `[You]` in the speaker color, a thin border
      around the entire input line, and a blinking cursor inside
- [x] 7.4 — **Syntax-highlighted tool call blocks:** when rendering a
      `proposed_action` entry in the viewport, format it as a mini code block
      (e.g. dark background panel, different foreground for key vs value) so
      it visually pops from surrounding plain text
- [x] 7.5 — **Gradient/accent title bar:** redesign the header to show the
      tool name on the left, the current session file path (truncated to fit)
      in the center, and a right-aligned keyboard hint (`ESC quit · ↑↓ scroll`)
      — all on a single line with a contrasting background color strip
- [x] 7.6 — **Speaker name pills:** render each speaker label as a short
      colored "pill" (padded, rounded border, bold) rather than plain text —
      e.g. `▌ Coder ▐` in purple, `▌ Reviewer ▐` in amber, `▌ You ▐` in green
- [x] 7.7 — **Timestamp dimming:** render timestamps in a noticeably dimmer
      color (e.g. `#555555`) relative to speaker and content so they're
      available but don't compete visually with the message text
- [x] 7.8 — **Test:** resize the terminal window while a session is active;
      confirm both panels reflow correctly and nothing overflows or wraps
      unexpectedly. Also verify the spinner appears and disappears at the right
      moments during a "create hello.txt" task run

### Phase 8 — Persistence & Resume

**Goal:** killing and restarting the process doesn't lose the session.

- [x] 8.1 — On startup, check for an existing transcript file matching the
      current/most-recent session ID; if found, `LoadFromFile` it instead of
      starting with an empty `Transcript`
- [x] 8.2 — Ensure every `Append` (from Phase 1.3) is already writing to disk
      immediately — confirm this still holds true now that Phase 6 has
      rewired the loop into `tea.Cmd`s, since it's easy to accidentally batch
      writes when restructuring
- [x] 8.3 — On resume, correctly restore session state: if the loaded
      transcript's last entries show a completed task, resume in **idle**
      state; if they show a task mid-flight (e.g. last entry was a
      `proposed_action` with no matching `action_result` yet), decide and
      implement a clear resume behavior (simplest: re-prompt Reviewer with the
      existing pending action, rather than guessing what should happen next)
- [x] 8.4 — **Test:** start a task, let it get partway through (a couple of
      approved actions), forcibly kill the process (`Ctrl+C` or `kill`),
      restart, and confirm both the full transcript history and the
      idle/active state are correctly restored
- [x] 8.5 — **Test:** repeat 8.4 but kill the process specifically *between* a
      `proposed_action` entry and its `action_result` — this is the edge case
      most likely to be handled wrong on first attempt

### Phase 9 — Hardening (ongoing, do this against real tasks, not toy tests)

**Goal:** make the tool trustworthy enough to actually use daily, not just
demoable.

- [x] 9.1 — Handle OpenCode Zen rate-limit responses (HTTP 429) with
      exponential backoff and retry, rather than crashing or silently
      dropping the request. Limits for `mimo-v2.5-free` aren't clearly
      published — log the full response headers/body the first several times
      you hit a 429, since they may reveal the actual ceiling (e.g. a
      `Retry-After` header)
- [x] 9.2 — Handle malformed or partial `tool_calls` JSON from the model
      gracefully — open/free models are less reliable at strict schema
      adherence than frontier closed models. On a malformed tool call, surface
      a clear error back into the transcript (as a `System` entry) rather than
      crashing the whole session
- [x] 9.3 — Add a timeout to `run_command` executions via
      `exec.CommandContext` — a hung shell command should not freeze the
      entire session indefinitely; pick a sensible default (e.g. 30s) and
      make it configurable
- [x] 9.4 — Add basic logging (to a file, not stdout, since bubbletea owns the
      terminal) of every request/response pair for debugging — you will need
      this the first time the loop does something unexpected and you need to
      see exactly what the model was actually sent and actually returned
- [ ] 9.5 — Run the tool on a real, moderately complex task end to end (not
      "hello.txt") — e.g. the Razorpay webhook example from
      PROJECT_SPEC.md §5. This is where you'll find the actual gaps in the
      approval loop logic that toy tests don't surface
- [ ] 9.6 — After 9.5, deliberately try to break it: give Coder a vague or
      contradictory instruction, see whether Reviewer actually catches
      something wrong rather than rubber-stamping every proposal — this is
      the core value proposition of the whole project, so it's worth
      specifically testing for false approvals, not just crashes

---

## Suggested Directory Structure

```
triad/
├── go.mod
├── config.yaml                  # or .env — model/endpoint/key config
├── main.go                      # entrypoint, wires everything together
├── internal/
│   ├── transcript/
│   │   ├── entry.go             # Entry struct
│   │   └── transcript.go        # Transcript type, Append/Save/Load
│   ├── agent/
│   │   ├── config.go            # AgentConfig struct
│   │   ├── client.go            # HTTP client → OpenAI-compatible endpoint
│   │   └── tools.go             # tool schema defs + executors
│   ├── loop/
│   │   └── loop.go              # core approval loop (headless logic,
│   │                              reused/adapted by the TUI layer)
│   └── tui/
│       ├── model.go              # bubbletea Model
│       ├── update.go             # Update() + Cmd definitions
│       └── view.go               # View() + lipgloss styling
└── sessions/
    └── <session-id>.jsonl        # persisted transcripts
```

---

## Reference: Minimal OpenAI-Compatible Request Shape

This is what your `agent/client.go` is building and sending, regardless of
which free model you point it at:

```json
{
  "model": "mimo-v2.5-free",
  "messages": [
    {"role": "user", "content": "[You]: Add a Razorpay webhook handler."}
  ],
  "tools": [
    {
      "type": "function",
      "function": {
        "name": "write_file",
        "description": "Write content to a file",
        "parameters": {
          "type": "object",
          "properties": {
            "path": {"type": "string"},
            "content": {"type": "string"}
          },
          "required": ["path", "content"]
        }
      }
    }
  ]
}
```

Reviewer's request is identical in shape, just with `"tools"` omitted entirely
and a different `"model"` value.

---

## What to Build First If You Only Have One Evening

If you want a quick win to confirm the whole concept before the full build:
**Phases 1–4, headless, no TUI, no persistence.** A single Go file that runs
one task start-to-finish in the terminal with `fmt.Println` output is enough
to prove the coder/reviewer/veto loop actually works. Everything after that
(TUI, persistence, hardening) is refinement on a proven core, not a gamble.