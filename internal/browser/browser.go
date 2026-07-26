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
//
// Work 5 Phase 2: The Manager now supports a real-Chrome CDP mode.
// When realChrome is true, ensureLaunched connects to the user's own
// Chrome via ConnectOverCDP instead of launching a private Chromium.
type Manager struct {
	mu sync.Mutex

	pw      *playwright.Playwright
	browser playwright.Browser
	context playwright.BrowserContext
	page    playwright.Page

	// headless controls whether Chromium runs with a visible UI.
	headless bool

	// realChrome, when true, makes ensureLaunched use ConnectOverCDP
	// to attach to the user's real Chrome instead of launching Playwright's
	// own Chromium. cdpPort is the port Chrome is listening on.
	realChrome bool
	cdpPort    int

	// chromeCmd is the *exec.Cmd for a Chrome process we launched ourselves
	// (via LaunchRealChrome). Nil if we connected to an already-running
	// Chrome. Used by Close() to decide whether to kill the process.
	chromeCmd interface{ Wait() error }

	// launched is true once Manager has successfully launched a browser
	// process. We don't track "current URL" here because playwright.Page
	// already does — we just check `page != nil` when we need it.
	launched bool

	// closed is set by Close() so we can fail fast on later calls without
	// trying to talk to a torn-down browser.
	closed bool

	// savedStorage holds the serialized storageState (cookies + localStorage)
	// from a previous SaveStorageState call.
	savedStorage *playwright.OptionalStorageState
}

// NewManager returns a Manager configured for headless Playwright Chromium.
// The first call to a tool method will trigger the launch.
func NewManager() *Manager {
	return &Manager{headless: true}
}

// NewRealChromeManager returns a Manager that will connect to the user's real
// Chrome browser via CDP on the given port. Call this instead of NewManager
// when you want Triad to control visible Chrome with the user's real logins.
// Pass CDPDefaultPort (9222) as the port unless you have a reason to differ.
func NewRealChromeManager(cdpPort int) *Manager {
	return &Manager{realChrome: true, cdpPort: cdpPort}
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
	// Only kill Chrome if we launched it ourselves. If the user had Chrome
	// open already (IsCDPRunning returned true before we launched), chromeCmd
	// is nil and we leave their browser alone.
	if m.chromeCmd != nil {
		// We don't care about the exit error — the process may already be gone.
		_ = m.chromeCmd.Wait()
		m.chromeCmd = nil
	}
	return firstErr
}

// ensureLaunched starts the browser on first use. Idempotent — subsequent
// calls just return nil. The mutex is held by the caller.
//
// Two launch paths:
//   - Default (headless=true): launch Playwright's own Chromium.
//   - Real Chrome (realChrome=true): attach to the user's Chrome via CDP.
func (m *Manager) ensureLaunched() error {
	if m.closed {
		return fmt.Errorf("browser: manager is closed")
	}
	if m.launched && m.page != nil {
		return nil
	}

	if m.realChrome {
		return m.ensureLaunchedCDP()
	}
	return m.ensureLaunchedHeadless()
}

// ensureLaunchedHeadless is the original path: launch Playwright's own Chromium.
func (m *Manager) ensureLaunchedHeadless() error {
	pw, err := playwright.Run()
	if err != nil {
		return wrapLaunchError(err)
	}

	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: &m.headless,
	})
	if err != nil {
		_ = pw.Stop()
		return wrapLaunchError(err)
	}

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

// ensureLaunchedCDP is the real-Chrome path:
//  1. If Chrome is already listening on cdpPort, connect to it directly.
//  2. Otherwise, launch Chrome ourselves with --remote-debugging-port,
//     wait for the CDP endpoint, then connect.
func (m *Manager) ensureLaunchedCDP() error {
	pw, err := playwright.Run()
	if err != nil {
		return fmt.Errorf("browser: failed to start Playwright runtime: %w", err)
	}

	// If Chrome isn't already running with CDP, launch it.
	if !IsCDPRunning(m.cdpPort) {
		// Kill any existing Chrome processes so the profile isn't locked
		// and Chrome doesn't show the profile picker or delegate to itself.
		KillExistingChrome()

		cmd, err := LaunchRealChrome(m.cdpPort)
		if err != nil {
			_ = pw.Stop()
			return fmt.Errorf("browser: %w", err)
		}
		m.chromeCmd = cmd

		// Wait for the debug server to be ready.
		if err := WaitForCDP(m.cdpPort, CDPReadyTimeout); err != nil {
			_ = pw.Stop()
			return fmt.Errorf("browser: %w", err)
		}
	}

	endpointURL := fmt.Sprintf("http://localhost:%d", m.cdpPort)
	browser, err := pw.Chromium.ConnectOverCDP(endpointURL)
	if err != nil {
		_ = pw.Stop()
		return fmt.Errorf("browser: ConnectOverCDP failed: %w", err)
	}

	// ConnectOverCDP gives us the existing contexts/pages. Reuse the first
	// available context+page if present, otherwise create fresh ones.
	var context playwright.BrowserContext
	var page playwright.Page

	if ctxs := browser.Contexts(); len(ctxs) > 0 {
		context = ctxs[0]
		if pages := context.Pages(); len(pages) > 0 {
			page = pages[0]
		}
	}
	if context == nil {
		context, err = browser.NewContext()
		if err != nil {
			_ = browser.Close()
			_ = pw.Stop()
			return fmt.Errorf("browser: failed to create context on real Chrome: %w", err)
		}
	}
	if page == nil {
		page, err = context.NewPage()
		if err != nil {
			_ = browser.Close()
			_ = pw.Stop()
			return fmt.Errorf("browser: failed to open page on real Chrome: %w", err)
		}
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
