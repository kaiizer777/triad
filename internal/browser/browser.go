// Package browser implements the structured browser-control tools exposed to
// Coder as browser_navigate / browser_click / browser_type / browser_get_text /
// browser_screenshot (docs/work2.md §4.2). It is intentionally not a "raw
// computer use" layer — there is no screenshot-decision loop here. Coder calls
// discrete, typed tools that operate on the actual page DOM, and every tool
// call goes through the same propose→Reviewer→execute approval loop as
// write_file / run_command.
//
// The package owns a single Playwright browser process and a single page.
// Multiple sequential tool calls share the same page, so click/type/get_text
// work against the most recent navigate() result. A new navigate() replaces
// the current page. This is the simplest model that still feels like a real
// browser session, and it's what a human would do — they don't open a new
// tab every time they type a character.
//
// Browser launch is lazy and best-effort. The first browser_navigate call is
// what actually launches Chromium. If the browser binary isn't installed (the
// docs flag this as a known first-run network issue), the executor returns a
// clear "browser not installed, run `playwright install chromium`" message
// rather than crashing the session. The approval loop sees that as a normal
// tool error.
package browser

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mxschmitt/playwright-go"
)

// DefaultTimeout is the default per-action timeout for browser_* tools
// (navigate wait, click, type, get_text, screenshot). Configurable via the
// timeout argument on Manager methods.
const DefaultTimeout = 30 * time.Second

// ErrBrowserNotInstalled is returned when the Playwright browser binary is
// not present on disk. The user-visible message tells the operator exactly
// how to fix it.
var ErrBrowserNotInstalled = errors.New("browser: Playwright browser binary not installed; run `playwright install chromium` (or set PLAYWRIGHT_BROWSERS_PATH) and try again")

// Manager owns the long-lived Playwright browser process and current page.
// All exported methods are safe for concurrent calls — the underlying
// Playwright API is not, so the Manager serialises them with a mutex.
//
// Work 4 Phase 4: The Manager now supports context-based session
// isolation. Each "task" can optionally get a fresh BrowserContext,
// resetting cookies, localStorage, and navigation history while
// optionally preserving login state via storageState.
type Manager struct {
	mu sync.Mutex

	pw      *playwright.Playwright
	browser playwright.Browser
	context playwright.BrowserContext
	page    playwright.Page

	// headless controls whether Chromium runs with a visible UI. We always
	// default to true — Triad is a CLI tool, there's no display server in
	// the test environment, and a headed Chromium would just hang waiting
	// for someone to close a window that never opens. We expose the flag
	// anyway because some future debug-only mode might want headed.
	headless bool

	// launched is true once Manager has successfully launched a browser
	// process. We don't track "current URL" here because playwright.Page
	// already does — we just check `page != nil` when we need it.
	launched bool

	// closed is set by Close() so we can fail fast on later calls without
	// trying to talk to a torn-down browser. We don't enforce close-on-
	// exit because the process tear-down at the OS level cleans up the
	// Chromium process too; Close() is here for tests and graceful
	// shutdown paths.
	closed bool

	// savedStorage holds the serialized storageState (cookies + localStorage)
	// from a previous SaveStorageState call. When non-nil, the next
	// ResetContext call will load this state into the new context, preserving
	// login sessions across task boundaries.
	savedStorage *playwright.OptionalStorageState
}

// NewManager returns a Manager that has not yet launched a browser process.
// The first call to a tool method will trigger the launch.
func NewManager() *Manager {
	return &Manager{headless: true}
}

// Close tears down the browser process. Safe to call multiple times.
// Returns the first non-nil error encountered, but always attempts the
// remaining teardown steps so a half-closed browser doesn't leak.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return nil
	}
	m.closed = true

	var firstErr error
	if m.page != nil {
		if err := m.page.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("browser: failed to close page: %w", err)
		}
		m.page = nil
	}
	if m.context != nil {
		if err := m.context.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("browser: failed to close context: %w", err)
		}
		m.context = nil
	}
	if m.browser != nil {
		if err := m.browser.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("browser: failed to close browser: %w", err)
		}
		m.browser = nil
	}
	if m.pw != nil {
		if err := m.pw.Stop(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("browser: failed to stop Playwright: %w", err)
		}
		m.pw = nil
	}
	return firstErr
}

// ensureLaunched starts the browser on first use. Idempotent — subsequent
// calls just return nil. The mutex is held by the caller.
//
// The error returned here is the single source of truth for "is the browser
// installed?" — every tool method funnels through here, so the not-installed
// detection logic only has to live in one place.
func (m *Manager) ensureLaunched() error {
	if m.closed {
		return fmt.Errorf("browser: manager is closed")
	}
	if m.launched && m.page != nil {
		return nil
	}

	// First-time launch path. We deliberately use a short timeout for the
	// launch itself — if Chromium isn't installed, the executable lookup
	// fails almost immediately, and we don't want a 30s default timeout
	// to mask that as a generic hang.
	pw, err := playwright.Run()
	if err != nil {
		return wrapLaunchError(err)
	}

	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: &m.headless,
	})
	if err != nil {
		// Tear down pw immediately — leaving it running is a leak.
		_ = pw.Stop()
		return wrapLaunchError(err)
	}

	// Create initial context and page.
	context, err := browser.NewContext()
	if err != nil {
		_ = browser.Close()
		_ = pw.Stop()
		return fmt.Errorf("browser: failed to create new context: %w", err)
	}

	page, err := context.NewPage()
	if err != nil {
		_ = context.Close()
		_ = browser.Close()
		_ = pw.Stop()
		return fmt.Errorf("browser: failed to open new page after launch: %w", err)
	}

	m.pw = pw
	m.browser = browser
	m.context = context
	m.page = page
	m.launched = true
	return nil
}

// wrapLaunchError translates the two error shapes we actually see from
// Playwright into something actionable. The free-tier mimo model never sees
// this raw error string, but a human reading the transcript absolutely
// should — "browser failed to start" is useless; "run `playwright install
// chromium`" is fixable.
func wrapLaunchError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "executable doesn't exist"),
		strings.Contains(lower, "browser is not installed"),
		strings.Contains(lower, "no such file"):
		return fmt.Errorf("%w (root cause: %v)", ErrBrowserNotInstalled, err)
	default:
		return fmt.Errorf("browser: failed to launch Chromium: %w", err)
	}
}

// ---------------------------------------------------------------------------
// Work 4 Phase 4 — Session & State Isolation
// ---------------------------------------------------------------------------

// ResetContext creates a new browser context and page, closing the previous
// ones. This implements the "fresh context per task" pattern:
//   - Cookies, localStorage, sessionStorage are all reset.
//   - Navigation history is cleared (new page starts at about:blank).
//   - If savedStorage was set via SaveStorageState, the new context is
//     seeded with that state, preserving login sessions across tasks.
//
// This is the recommended boundary for state isolation — call it between
// distinct tasks rather than between every tool call (which would be
// expensive) or never (which would leak state indefinitely).
//
// The mutex is NOT acquired here — callers must hold m.mu. This is an
// internal method; the public API is ExecuteResetContext.
func (m *Manager) resetContextUnlocked() error {
	if err := m.ensureLaunched(); err != nil {
		return err
	}

	// Close the old page and context.
	if m.page != nil {
		_ = m.page.Close()
		m.page = nil
	}
	if m.context != nil {
		_ = m.context.Close()
		m.context = nil
	}

	// Create new context. If we have saved storage state, load it.
	var ctxOpts playwright.BrowserNewContextOptions
	if m.savedStorage != nil {
		ctxOpts.StorageState = m.savedStorage
	}

	context, err := m.browser.NewContext(ctxOpts)
	if err != nil {
		return fmt.Errorf("browser: failed to create new context: %w", err)
	}

	page, err := context.NewPage()
	if err != nil {
		_ = context.Close()
		return fmt.Errorf("browser: failed to create new page: %w", err)
	}

	m.context = context
	m.page = page
	return nil
}

// ExecuteResetContext creates a fresh browser context, closing the old one.
// This is the public API for task-boundary state isolation.
func (m *Manager) ExecuteResetContext() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.resetContextUnlocked(); err != nil {
		return "", err
	}

	msg := "browser_reset_context: fresh context created"
	if m.savedStorage != nil {
		msg += " (with saved storage state restored)"
	}
	return msg, nil
}

// SaveStorageState captures the current context's cookies and localStorage
// as a JSON string, stored in memory. The next ResetContext call will
// restore this state into the new context, preserving login sessions.
//
// This is the deliberate "save your login" mechanism — Coder must
// explicitly call this after logging in if the login should persist
// across task boundaries. Without it, ResetContext wipes everything.
//
// Returns the JSON string for informational purposes.
func (m *Manager) ExecuteSaveStorageState() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.ensureLaunched(); err != nil {
		return "", err
	}
	if m.context == nil {
		return "", fmt.Errorf("browser: no active context")
	}

	state, err := m.context.StorageState()
	if err != nil {
		return "", fmt.Errorf("browser: failed to get storage state: %w", err)
	}

	m.savedStorage = state.ToOptionalStorageState()
	return fmt.Sprintf("browser_save_storage_state: captured %d cookies, %d origins", len(state.Cookies), len(state.Origins)), nil
}

// ClearSavedStorage clears any previously saved storage state. The next
// ResetContext will create a truly empty context with no login state.
func (m *Manager) ExecuteClearSavedStorage() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.savedStorage = nil
	return "browser_clear_saved_storage: saved storage state cleared", nil
}

// CurrentURL returns the current page URL. Useful for debugging and
// for Coder to verify navigation state.
func (m *Manager) CurrentURL() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.page == nil {
		return ""
	}
	return m.page.URL()
}

// HasSavedStorage reports whether a storage state has been saved that
// would be restored on the next ResetContext call.
func (m *Manager) HasSavedStorage() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.savedStorage != nil
}
