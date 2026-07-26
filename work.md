# Triad — Workflow 4 (Complete) + Workflow 5: Real Chrome Control

## Workflow 4 — Browser Tool Hardening ✅ DONE

All 4 phases shipped:
- **Phase 1** — Selector strategy: role/text/css fallback chain, positional selector flagging ✅
- **Phase 2** — Waiting: condition-based waits, `browser_wait_for`, bounded timeouts, no fixed sleeps ✅
- **Phase 3** — Failure recovery: zero-match vs ambiguous detection, deterministic recovery first, LLM-as-exception-handler only if needed, capped at 2 attempts ✅
- **Phase 4** — Session isolation: explicit context reset at task boundaries, `SaveStorageState` for login persistence ✅

Architecture: DOM-first, LLM-as-exception-handler, never bypasses Reviewer.

---

## Workflow 5 — Real Chrome Control (CDP Mode)

**Problem:** Triad currently launches its own hidden Playwright Chromium (`headless: true`). The user never sees anything, their real Chrome is never touched, and sites like claude.ai block it as a bot.

**Goal:** When the user asks Triad to "use Chrome," Triad controls the user's **actual installed Chrome** — visibly, with their real logins and cookies — by connecting via Chrome DevTools Protocol (CDP).

**How it works:**
1. Triad finds the user's real Chrome executable on disk
2. Launches it with `--remote-debugging-port=9222` (if not already running with CDP)
3. Playwright connects to it via `ConnectOverCDP` instead of launching its own browser
4. The user can **see everything** in their real Chrome window, logged into their real accounts

---

## Phase 1 — Chrome Finder ✅ DONE

**Goal:** Detect the real Chrome executable path cross-platform (Windows priority), expose it as a utility the rest of the code can call.

- [x] 1.1 — Add `FindChrome() (string, error)` in `internal/browser/chrome.go` that checks known Windows paths (`Program Files`, `Program Files (x86)`, `LocalAppData`) plus the `CHROME_PATH` env override, and falls back to common Linux/macOS paths
- [x] 1.2 — If no Chrome is found, return a clear error: `"real Chrome not found; set CHROME_PATH or install Chrome"`
- [x] 1.3 — **Test:** unit tests confirm found path on this machine, env override works, missing binary returns expected error

---

## Phase 2 — CDP Launch & Connect ✅ DONE

**Goal:** Launch real Chrome with remote debugging enabled and connect Playwright to it.

- [x] 2.1 — Add `LaunchRealChrome(port int)` and `LaunchRealChromeWithDataDir(port, dir)` in `chrome.go`: runs the found Chrome binary with `--remote-debugging-port=<port>`, `--no-first-run`, `--no-default-browser-check`, explicit `--user-data-dir` to prevent delegation to existing Chrome instance
- [x] 2.2 — Add `ensureLaunchedCDP()` on `Manager`: calls `pw.Chromium.ConnectOverCDP("http://localhost:<port>")`, reuses existing contexts/pages if present — skips the normal `pw.Chromium.Launch` path entirely
- [x] 2.3 — `WaitForCDP(port, timeout)` polls `/json/version` every 200ms until ready; `IsCDPRunning` does a single quick probe to avoid double-launching
- [x] 2.4 — **Test:** launched real Chrome, connected via CDP, navigated to example.com, confirmed URL `https://example.com/` status 200 ✅

---

## Phase 3 — Manager Mode Switch ✅ DONE

**Goal:** Let `Manager` operate in two modes — `headless` (current default) or `realChrome` (new) — without breaking any existing code.

- [x] 3.1 — `Manager` has `realChrome bool` and `cdpPort int` fields; `chromeCmd` tracks whether we launched Chrome (so Close knows whether to kill it)
- [x] 3.2 — `NewRealChromeManager(port int) *Manager` is the new constructor; `NewManager()` unchanged as the zero-config headless wrapper
- [x] 3.3 — `ensureLaunched()` branches: `realChrome=true` → `ensureLaunchedCDP()`, else → `ensureLaunchedHeadless()` (original path, unmodified)
- [x] 3.4 — **Test:** 5 mode-switch tests pass — headless defaults, real Chrome fields, shared tool surface, close-before-launch safety, idempotent double-close ✅

---

## Phase 4 — Config & CLI Wiring ✅ DONE

**Goal:** Let the user turn on real Chrome mode via `config.yaml` or a CLI flag, with no code change needed.

- [x] 4.1 — Added `browser_mode: "headless" | "real_chrome"` and `chrome_cdp_port: 9222` to `config.yaml` (as commented examples) and to the `Config` struct with defaults
- [x] 4.2 — In `main.go`: reads `browser_mode` from config, passes `NewRealChromeManager(port)` when `real_chrome` is set; prints `"Browser: real Chrome (CDP on port 9222)"` at startup
- [x] 4.3 — `--browser=real` CLI flag overrides config (takes priority); if Chrome not found, exits with a clear message
- [x] 4.4 — Full project builds clean with `go build ./...` ✅
- [x] 4.5 — **Test:** set `browser_mode: real_chrome` in config or pass `--browser=real` → Triad opens your real Chrome window on first `browser_navigate`

---

## Phase 5 — Polish & Error Handling ✅ DONE

**Goal:** Make real Chrome mode robust for daily use.

- [x] 5.1 — `IsCDPRunning(port)` probes the CDP endpoint before launching; if Chrome is already listening, `ensureLaunchedCDP` connects directly without launching a new process — user's existing Chrome is reused
- [x] 5.2 — `chromeCmd` is set only when we launched Chrome ourselves; `Close()` kills it only if non-nil — if user's Chrome was already open, we leave it running
- [x] 5.3 — `WaitForCDP` returns a clear, actionable error on timeout: `"Chrome CDP endpoint not ready after Xs on port Y; Chrome may have failed to start or the port is blocked"`
- [x] 5.4 — `LaunchRealChromeWithDataDir` added for test isolation (temp dir avoids locking user's real profile); `LaunchRealChrome` uses real user profile dir normally
- [x] 5.5 — Final exe built: `triad.exe` ✅ — 16/16 tests pass across all phases

---

## Suggested Order

```
Phase 1 (Chrome Finder)     ← done ✅
Phase 2 (CDP Launch)        ← next
Phase 3 (Mode Switch)       ← depends on 2
Phase 4 (Config & CLI)      ← depends on 3
Phase 5 (Polish)            ← last
```