package browser

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mxschmitt/playwright-go"
)

// MaxResultBytes caps the size of any string we put in the transcript
// (browser_get_text, browser_navigate, browser_screenshot, etc.). The
// transcript is a long-lived log; we don't want a single 50MB page
// payload to bloat it. We truncate, then suffix a marker so the human
// reader knows there's more.
const MaxResultBytes = 32 * 1024

// MaxScreenshotBytes is a separate, lower cap for screenshot output —
// a 32KB image is unreadable, so we default to ~512KB which is enough
// to actually see what the page looked like while keeping the
// transcript from blowing up.
const MaxScreenshotBytes = 512 * 1024

// IsBrowserTool reports whether a given tool name is one of the
// browser_* tools this package implements. The loop / TUI use this
// to decide whether to intercept a tool call (browser tools need the
// long-lived Manager) or hand it off to agent.ExecuteTool as usual.
func IsBrowserTool(name string) bool {
	switch name {
	case "browser_navigate",
		"browser_click",
		"browser_type",
		"browser_get_text",
		"browser_screenshot",
		"browser_wait_for":
		return true
	}
	return false
}

// truncateResult clamps a string to max bytes, appending a marker so
// the reader knows the result was clipped. We work in bytes (not
// runes) because the transcript is plain text and Playwright returns
// raw UTF-8; cutting mid-rune is technically wrong but harmless for
// display purposes and avoids a full UTF-8 decoder round-trip here.
func truncateResult(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + fmt.Sprintf("\n\n[truncated: result was %d bytes, kept first %d]", len(s), max)
}

// NavigateArgs is the decoded argument struct for browser_navigate.
// The model sends URL as a string; we parse and validate it here.
type NavigateArgs struct {
	URL string `json:"url"`
}

// ClickArgs is the decoded argument struct for browser_click. Selector
// is a CSS / text / Playwright selector — Playwright accepts the
// engine-specific syntax directly. Strategy is an optional hint
// (Work 4 Phase 1.3) that tells the browser which Playwright Locator
// factory to use: "role", "text", "label", "placeholder", "testid",
// "title", "alt", or "css" (default).
type ClickArgs struct {
	Selector string        `json:"selector"`
	Strategy SelectStrategy `json:"strategy,omitempty"`
}

// TypeArgs is the decoded argument struct for browser_type. We use
// `text` rather than `value` so the model isn't tempted to confuse
// this with the read_file `path` argument; the schema description
// spells out the distinction. Strategy mirrors ClickArgs.Strategy.
type TypeArgs struct {
	Selector string        `json:"selector"`
	Text     string        `json:"text"`
	Strategy SelectStrategy `json:"strategy,omitempty"`
}

// GetTextArgs is the decoded argument struct for browser_get_text.
// Strategy mirrors ClickArgs.Strategy.
type GetTextArgs struct {
	Selector string        `json:"selector"`
	Strategy SelectStrategy `json:"strategy,omitempty"`
}

// ScreenshotArgs is the decoded argument struct for browser_screenshot.
// `path` is optional — if empty, the PNG is returned base64-encoded
// in the result (capped to MaxScreenshotBytes). If set, it's written
// to that path inside the manager's working directory and the
// absolute path is returned.
type ScreenshotArgs struct {
	Path     string `json:"path"`
	FullPage bool   `json:"full_page"`
}

// WaitCondition is the condition shape Coder passes to
// browser_wait_for. Work 4 Phase 2 covers three concrete cases that
// all map onto existing Playwright auto-waiting primitives, with an
// explicit timeout that surfaces as a clear failure when the signal
// never arrives:
//
//   - "text"   — wait until the page contains the given visible text
//     (substring match by default, exact-match when Exact is true).
//     Maps to page.GetByText(...).WaitFor().
//   - "visible" — wait until the element matching Selector+Strategy is
//     attached to the DOM and visible. Mirrors Phase 1's selector
//     plumbing so wait_for reuses the same stable, semantic
//     strategies. Maps to Locator.WaitFor().
//   - "url"    — wait until the page navigates to a URL matching the
//     given substring (e.g. "/dashboard", "?error=1"). Maps to
//     page.WaitForURL().
//
// "network_idle" was an obvious candidate (Playwright supports it
// natively) but we deliberately leave it out for now: it's the most
// expensive wait in real usage, and Coder rarely needs it. Easy to
// add later if a real use case shows up.
type WaitCondition struct {
	// Kind selects the wait primitive. One of: "text", "visible", "url".
	Kind string `json:"kind"`

	// Selector + Strategy are used when Kind == "visible". They reuse
	// the same SelectStrategy enum and LocatorForStrategy factory from
	// Phase 1, so wait_for and click target the same elements with the
	// same semantic discipline.
	Selector string         `json:"selector,omitempty"`
	Strategy SelectStrategy `json:"strategy,omitempty"`

	// Text is used when Kind == "text". Visible page text to wait for.
	Text string `json:"text,omitempty"`

	// Exact selects exact-match semantics for Kind == "text". Has no
	// effect on other kinds.
	Exact bool `json:"exact,omitempty"`

	// URL is used when Kind == "url". A substring match against the
	// page's current URL (page.WaitForURL's documented default).
	URL string `json:"url,omitempty"`

	// TimeoutMS overrides the default wait timeout for this call.
	// 0 means "use DefaultTimeout". Capped on the high side by
	// MaxWaitTimeout so a model can't ask us to hang for an hour.
	TimeoutMS int `json:"timeout_ms,omitempty"`
}

// MaxWaitTimeout is the upper bound for any browser_wait_for timeout,
// regardless of what the caller passes. Two minutes is enough for any
// realistic page render or navigation we care about, and prevents a
// pathological "timeout_ms: 99999999" from hanging the loop.
const MaxWaitTimeout = 2 * time.Minute

// resolveWaitTimeout returns the effective wait timeout for a
// browser_wait_for call. Honors TimeoutMS when in (0, MaxWaitTimeout],
// falls back to DefaultTimeout for 0 (unset), and clamps anything
// larger to MaxWaitTimeout so we never wait longer than that.
func resolveWaitTimeout(ms int) time.Duration {
	switch {
	case ms <= 0:
		return DefaultTimeout
	case time.Duration(ms)*time.Millisecond > MaxWaitTimeout:
		return MaxWaitTimeout
	default:
		return time.Duration(ms) * time.Millisecond
	}
}

// validateSelector does a minimal sanity check on a selector. Playwright
// accepts basically anything, so we don't try to parse CSS — we just
// reject empty strings and obvious nonsense (whitespace-only, very long
// garbage) so a model that emits `""` or `"  "` gets a clean error
// rather than a Playwright timeout.
func validateSelector(name, sel string) error {
	if strings.TrimSpace(sel) == "" {
		return fmt.Errorf("%s: required argument 'selector' is missing or empty", name)
	}
	if len(sel) > 4096 {
		return fmt.Errorf("%s: selector is suspiciously long (%d chars); refusing to send", name, len(sel))
	}
	return nil
}

// validateURL checks that the URL parses and has a scheme we accept.
// We deliberately allow http and https only — file:// and javascript:
// and data: are all things the model can easily misuse to do
// damage, and we don't have a use case for them yet.
func validateURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return errors.New("browser_navigate: required argument 'url' is missing or empty")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("browser_navigate: failed to parse url %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("browser_navigate: only http and https schemes are supported (got %q)", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("browser_navigate: url %q has no host", raw)
	}
	return nil
}

// ExecuteTool is the single entry point the loop / TUI use to run
// any approved browser_* tool call. It dispatches by tool name, so
// callers don't need to know which executor handles which tool —
// they just pass the tool name and raw arguments through. The
// function holds the Manager's mutex for the duration of the
// operation, so concurrent browser tool calls from the same
// session are serialised (which matches what a single human driving
// one browser tab would do).
//
// workDir is the project root — only used by browser_screenshot
// when writing to a file, to resolve the relative path. The other
// tools don't touch the filesystem.
//
// Returns an error if name is not a known browser_* tool. Callers
// should gate on IsBrowserTool(name) before calling.
func (m *Manager) ExecuteTool(workDir, name, rawArgs string) (string, error) {
	switch name {
	case "browser_navigate":
		return m.ExecuteNavigate(rawArgs)
	case "browser_click":
		return m.ExecuteClick(rawArgs)
	case "browser_type":
		return m.ExecuteType(rawArgs)
	case "browser_get_text":
		return m.ExecuteGetText(rawArgs)
	case "browser_screenshot":
		return m.ExecuteScreenshot(workDir, rawArgs)
	case "browser_wait_for":
		return m.ExecuteWaitFor(rawArgs)
	default:
		return "", fmt.Errorf("browser.ExecuteTool: %q is not a browser tool", name)
	}
}

// ExecuteNavigate navigates the shared page to URL. Returns a short
// confirmation including the final URL (after any redirects) and the
// page title. Long page bodies aren't included here — use
// browser_get_text for that.
func (m *Manager) ExecuteNavigate(rawArgs string) (string, error) {
	var args NavigateArgs
	if err := unmarshalArgs("browser_navigate", rawArgs, &args); err != nil {
		return "", err
	}
	if err := validateURL(args.URL); err != nil {
		return "", err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensureLaunched(); err != nil {
		return "", err
	}

	resp, err := m.page.Goto(args.URL, playwright.PageGotoOptions{
		Timeout:   playwright.Float(float64(DefaultTimeout.Milliseconds())),
		WaitUntil: playwright.WaitUntilStateLoad,
	})
	if err != nil {
		return "", fmt.Errorf("browser_navigate: failed to load %q: %w", args.URL, err)
	}

	finalURL := args.URL
	var status int
	if resp != nil {
		finalURL = resp.URL()
		status = resp.Status()
	}
	title, _ := m.page.Title()

	return fmt.Sprintf("browser_navigate: loaded %s (status %d, title %q)", finalURL, status, title), nil
}

// ExecuteClick clicks the element matching selector. Waits for the
// element to be visible before clicking (Playwright default), with the
// default timeout. If args.Strategy is set, the selector is resolved
// through a Playwright Locator factory (Phase 1.3) for the more
// stable role/label/text/testid/etc. targeting.
//
// Phase 1.4: pure positional CSS selectors (e.g. "li:nth-child(3) > a")
// are rejected up front instead of being silently executed — the
// Reviewer prompt also instructs Reviewer to object to such proposals,
// so this refusal is the first of two safety nets.
func (m *Manager) ExecuteClick(rawArgs string) (string, error) {
	var args ClickArgs
	if err := unmarshalArgs("browser_click", rawArgs, &args); err != nil {
		return "", err
	}
	if err := validateSelector("browser_click", args.Selector); err != nil {
		return "", err
	}
	if !validStrategy(args.Strategy) {
		return "", fmt.Errorf("browser_click: unknown strategy %q (valid: css, role, text, label, placeholder, testid, title, alt)", args.Strategy)
	}
	if err := rejectPositionalCSS("browser_click", args.Strategy, args.Selector); err != nil {
		return "", err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensureLaunched(); err != nil {
		return "", err
	}

	loc, err := LocatorForStrategy(m.page, args.Strategy, args.Selector, "browser_click")
	if err != nil {
		return "", err
	}
	if err := loc.Click(playwright.LocatorClickOptions{
		Timeout: playwright.Float(float64(DefaultTimeout.Milliseconds())),
	}); err != nil {
		return "", fmt.Errorf("browser_click: failed to click [%s] %q: %w", args.Strategy, args.Selector, err)
	}
	return fmt.Sprintf("browser_click: clicked [%s] %q", args.Strategy, args.Selector), nil
}

// ExecuteType fills the element matching selector with text. Uses
// Playwright's Fill, which clears the field first — matches what a
// human would expect from "type into this field". For append-only
// typing (e.g. into a contenteditable), this is wrong, but we don't
// have a use case for that yet and it's easy to add a separate
// `browser_append` tool later if it comes up. Strategy mirrors
// ExecuteClick. Phase 1.4 positional-rejection mirrors ExecuteClick.
func (m *Manager) ExecuteType(rawArgs string) (string, error) {
	var args TypeArgs
	if err := unmarshalArgs("browser_type", rawArgs, &args); err != nil {
		return "", err
	}
	if err := validateSelector("browser_type", args.Selector); err != nil {
		return "", err
	}
	if args.Text == "" {
		// Empty string is technically valid (clear a field) but is
		// almost always a model mistake. We refuse rather than
		// silently clearing the target.
		return "", errors.New("browser_type: required argument 'text' is empty; if you want to clear a field, use browser_click + a delete-key approach or call this with a single space")
	}
	if !validStrategy(args.Strategy) {
		return "", fmt.Errorf("browser_type: unknown strategy %q (valid: css, role, text, label, placeholder, testid, title, alt)", args.Strategy)
	}
	if err := rejectPositionalCSS("browser_type", args.Strategy, args.Selector); err != nil {
		return "", err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensureLaunched(); err != nil {
		return "", err
	}

	loc, err := LocatorForStrategy(m.page, args.Strategy, args.Selector, "browser_type")
	if err != nil {
		return "", err
	}
	if err := loc.Fill(args.Text, playwright.LocatorFillOptions{
		Timeout: playwright.Float(float64(DefaultTimeout.Milliseconds())),
	}); err != nil {
		return "", fmt.Errorf("browser_type: failed to fill [%s] %q: %w", args.Strategy, args.Selector, err)
	}
	return fmt.Sprintf("browser_type: filled [%s] %q with %d chars", args.Strategy, args.Selector, len(args.Text)), nil
}

// ExecuteGetText returns the text content of the first element
// matching selector, or the page body's text if selector is empty /
// "body". We cap the result at MaxResultBytes to keep the transcript
// from blowing up on a giant page. Strategy mirrors ExecuteClick.
// Phase 1.4 positional-rejection mirrors ExecuteClick.
func (m *Manager) ExecuteGetText(rawArgs string) (string, error) {
	var args GetTextArgs
	if err := unmarshalArgs("browser_get_text", rawArgs, &args); err != nil {
		return "", err
	}
	// An empty / "body" selector is allowed for "give me the whole
	// visible text of the page" — that's a reasonable use case.
	selector := args.Selector
	if strings.TrimSpace(selector) == "" {
		selector = "body"
	} else if err := validateSelector("browser_get_text", selector); err != nil {
		return "", err
	}
	if !validStrategy(args.Strategy) {
		return "", fmt.Errorf("browser_get_text: unknown strategy %q (valid: css, role, text, label, placeholder, testid, title, alt)", args.Strategy)
	}
	if err := rejectPositionalCSS("browser_get_text", args.Strategy, selector); err != nil {
		return "", err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensureLaunched(); err != nil {
		return "", err
	}

	// For the empty-selector defaults-to-body shortcut, we still go
	// through the CSS locator path so the strategy plumbing stays
	// uniform. Using loc.InnerText() is the documented locator-based
	// way to read text (Playwright-go marks the page.InnerText
	// selector variant as deprecated).
	loc, err := LocatorForStrategy(m.page, args.Strategy, selector, "browser_get_text")
	if err != nil {
		return "", err
	}
	text, err := loc.InnerText(playwright.LocatorInnerTextOptions{
		Timeout: playwright.Float(float64(DefaultTimeout.Milliseconds())),
	})
	if err != nil {
		return "", fmt.Errorf("browser_get_text: failed to read [%s] %q: %w", args.Strategy, selector, err)
	}
	return fmt.Sprintf("browser_get_text: [%s] %q =\n%s", args.Strategy, selector, truncateResult(text, MaxResultBytes)), nil
}

// rejectPositionalCSS refuses purely positional CSS selectors. The
// detection (IsPositionalSelector) only flags the CSS strategy path —
// role/text/label/placeholder/testid/title/alt are inherently semantic
// and don't have positional selectors, so they bypass this check.
//
// The error message is intentionally Reviewer-friendly: it states what
// Coder did wrong, why it matters, and what to do instead. When the
// loop surfaces this error, the Reviewer sees the corrected guidance
// and is in a position to keep objecting or to approve the corrected
// re-proposal.
func rejectPositionalCSS(toolName string, strategy SelectStrategy, selector string) error {
	if strategy != "" && strategy != StrategyCSS {
		// role / text / label / placeholder / testid / title / alt
		// are not subject to positional pseudo-classes.
		return nil
	}
	if !IsPositionalSelector(selector) {
		return nil
	}
	return fmt.Errorf(
		"%s: positional CSS selector rejected (%q) — layout-coupled selectors break the moment the page is restyled. "+
			"Replace with one of: "+
			"strategy=\"role\" with selector=\"role:name\" (e.g. \"button:Sign in\"), "+
			"strategy=\"text\" with selector=visible text, "+
			"strategy=\"label\" with selector=label/aria-label text, "+
			"strategy=\"placeholder\" with selector=placeholder text, "+
			"strategy=\"testid\" with selector=data-testid, "+
			"or strategy=\"css\" with a stable id like \"#submit-btn\" rather than a positional chain.",
		toolName, selector,
	)
}

// ExecuteScreenshot takes a PNG screenshot. If args.Path is non-empty,
// writes to that path (relative to workDir) and returns the absolute
// path. Otherwise returns the PNG as base64, capped to
// MaxScreenshotBytes — large enough to be useful, small enough to
// keep the transcript readable.
func (m *Manager) ExecuteScreenshot(workDir, rawArgs string) (string, error) {
	var args ScreenshotArgs
	if err := unmarshalArgs("browser_screenshot", rawArgs, &args); err != nil {
		return "", err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensureLaunched(); err != nil {
		return "", err
	}

	png, err := m.page.Screenshot(playwright.PageScreenshotOptions{
		FullPage: playwright.Bool(args.FullPage),
	})
	if err != nil {
		return "", fmt.Errorf("browser_screenshot: failed to capture page: %w", err)
	}

	if args.Path == "" {
		// Cap the base64 length we put in the transcript. We could
		// also refuse entirely if the screenshot is too big, but
		// truncating a base64 PNG would produce un-decodable junk —
		// refuse and tell the caller to use a file path instead.
		if len(png) > MaxScreenshotBytes {
			return "", fmt.Errorf("browser_screenshot: page screenshot is %d bytes, exceeds %d-byte cap; pass `path` to write to a file instead", len(png), MaxScreenshotBytes)
		}
		encoded := base64.StdEncoding.EncodeToString(png)
		return fmt.Sprintf("browser_screenshot: %d bytes (base64-encoded)\n%s", len(png), encoded), nil
	}

	// Write to disk. We re-use safeRelPath-style validation: reject
	// absolute paths and ".." traversal. The workDir parameter is the
	// project root the loop is operating in.
	full, err := resolveScreenshotPath(workDir, args.Path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return "", fmt.Errorf("browser_screenshot: could not create parent dirs for %q: %w", args.Path, err)
	}
	if err := os.WriteFile(full, png, 0o644); err != nil {
		return "", fmt.Errorf("browser_screenshot: failed to write %q: %w", args.Path, err)
	}
	return fmt.Sprintf("browser_screenshot: wrote %d bytes to %s", len(png), full), nil
}

// resolveScreenshotPath mirrors the safeRelPath logic in
// internal/agent/tools.go but lives here so the browser package doesn't
// import agent (which would be a circular dep — agent already pulls
// in everything). Behaviour: reject absolute paths and ".." segments.
func resolveScreenshotPath(workDir, path string) (string, error) {
	if filepath.IsAbs(path) || strings.HasPrefix(path, "/") {
		return "", fmt.Errorf("browser_screenshot: path %q is absolute; use a project-relative path", path)
	}
	normalized := filepath.ToSlash(path)
	for _, seg := range strings.Split(normalized, "/") {
		if seg == ".." {
			return "", fmt.Errorf("browser_screenshot: '..' traversal is not allowed in path %q", path)
		}
	}
	full := filepath.Join(workDir, path)
	return full, nil
}

// unmarshalArgs decodes the raw JSON arguments string into the given
// struct. The model sometimes sends extra/unknown fields, so we don't
// use DisallowUnknownFields.
func unmarshalArgs(toolName, rawJSON string, dst any) error {
	if rawJSON == "" {
		return fmt.Errorf("%s: arguments are empty", toolName)
	}
	if err := json.Unmarshal([]byte(rawJSON), dst); err != nil {
		return fmt.Errorf("%s: failed to parse arguments %q: %w", toolName, rawJSON, err)
	}
	return nil
}

// ExecuteWaitFor is the Work 4 Phase 2.3 tool: an explicit,
// reviewable, condition-based wait. The point of having a dedicated
// tool — rather than asking Coder to silently sleep after a click — is
// that waiting shows up in the transcript as its own approve-or-reject
// action, with the condition Coder is waiting for visible to Reviewer
// (and to the human reading the log later). A fixed sleep, by
// contrast, is invisible and unbounded.
//
// The wait is bounded by an explicit timeout (DefaultTimeout, or the
// TimeoutMS override capped at MaxWaitTimeout) and surfaces a clear
// failure on timeout rather than hanging the loop. There are no fixed
// sleeps anywhere on this code path.
func (m *Manager) ExecuteWaitFor(rawArgs string) (string, error) {
	var args WaitCondition
	if err := unmarshalArgs("browser_wait_for", rawArgs, &args); err != nil {
		return "", err
	}

	switch args.Kind {
	case "text":
		if args.Text == "" {
			return "", errors.New("browser_wait_for: kind=\"text\" requires a non-empty 'text' field")
		}
	case "visible":
		if err := validateSelector("browser_wait_for", args.Selector); err != nil {
			return "", err
		}
		if !validStrategy(args.Strategy) {
			return "", fmt.Errorf("browser_wait_for: unknown strategy %q (valid: css, role, text, label, placeholder, testid, title, alt)", args.Strategy)
		}
		if err := rejectPositionalCSS("browser_wait_for", args.Strategy, args.Selector); err != nil {
			return "", err
		}
	case "url":
		if args.URL == "" {
			return "", errors.New("browser_wait_for: kind=\"url\" requires a non-empty 'url' field")
		}
	default:
		return "", fmt.Errorf("browser_wait_for: kind %q is not one of: text, visible, url", args.Kind)
	}

	timeout := resolveWaitTimeout(args.TimeoutMS)

	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensureLaunched(); err != nil {
		return "", err
	}

	switch args.Kind {
	case "text":
		opts := playwright.PageGetByTextOptions{}
		if args.Exact {
			opts.Exact = boolPtr(true)
		}
		loc := m.page.GetByText(args.Text, opts)
		if err := loc.WaitFor(playwright.LocatorWaitForOptions{
			State:   playwright.WaitForSelectorStateVisible,
			Timeout: playwright.Float(float64(timeout.Milliseconds())),
		}); err != nil {
			return "", fmt.Errorf("browser_wait_for: timed out after %s waiting for text %q to appear", timeout, args.Text)
		}
		return fmt.Sprintf("browser_wait_for: text %q appeared within %s", args.Text, timeout), nil

	case "visible":
		loc, err := LocatorForStrategy(m.page, args.Strategy, args.Selector, "browser_wait_for")
		if err != nil {
			return "", err
		}
		if err := loc.WaitFor(playwright.LocatorWaitForOptions{
			State:   playwright.WaitForSelectorStateVisible,
			Timeout: playwright.Float(float64(timeout.Milliseconds())),
		}); err != nil {
			return "", fmt.Errorf("browser_wait_for: timed out after %s waiting for [%s] %q to become visible", timeout, args.Strategy, args.Selector)
		}
		return fmt.Sprintf("browser_wait_for: [%s] %q became visible within %s", args.Strategy, args.Selector, timeout), nil

	case "url":
		if err := m.page.WaitForURL(args.URL, playwright.PageWaitForURLOptions{
			Timeout: playwright.Float(float64(timeout.Milliseconds())),
		}); err != nil {
			return "", fmt.Errorf("browser_wait_for: timed out after %s waiting for URL to contain %q", timeout, args.URL)
		}
		finalURL := m.page.URL()
		return fmt.Sprintf("browser_wait_for: URL became %q (matched %q) within %s", finalURL, args.URL, timeout), nil
	}

	// Unreachable — the kind switch above already errored on unknown values.
	return "", fmt.Errorf("browser_wait_for: internal error, unknown kind %q after validation", args.Kind)
}
