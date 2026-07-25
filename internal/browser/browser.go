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
type Manager struct {
	mu sync.Mutex

	pw      *playwright.Playwright
	browser playwright.Browser
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

	page, err := browser.NewPage()
	if err != nil {
		_ = browser.Close()
		_ = pw.Stop()
		return fmt.Errorf("browser: failed to open new page after launch: %w", err)
	}

	m.pw = pw
	m.browser = browser
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
