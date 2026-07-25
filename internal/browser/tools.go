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
		"browser_screenshot":
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
// engine-specific syntax directly.
type ClickArgs struct {
	Selector string `json:"selector"`
}

// TypeArgs is the decoded argument struct for browser_type. We use
// `text` rather than `value` so the model isn't tempted to confuse
// this with the read_file `path` argument; the schema description
// spells out the distinction.
type TypeArgs struct {
	Selector string `json:"selector"`
	Text     string `json:"text"`
}

// GetTextArgs is the decoded argument struct for browser_get_text.
type GetTextArgs struct {
	Selector string `json:"selector"`
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
// default timeout.
func (m *Manager) ExecuteClick(rawArgs string) (string, error) {
	var args ClickArgs
	if err := unmarshalArgs("browser_click", rawArgs, &args); err != nil {
		return "", err
	}
	if err := validateSelector("browser_click", args.Selector); err != nil {
		return "", err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensureLaunched(); err != nil {
		return "", err
	}

	if err := m.page.Click(args.Selector, playwright.PageClickOptions{
		Timeout: playwright.Float(float64(DefaultTimeout.Milliseconds())),
	}); err != nil {
		return "", fmt.Errorf("browser_click: failed to click %q: %w", args.Selector, err)
	}
	return fmt.Sprintf("browser_click: clicked %q", args.Selector), nil
}

// ExecuteType fills the element matching selector with text. Uses
// Playwright's Fill, which clears the field first — matches what a
// human would expect from "type into this field". For append-only
// typing (e.g. into a contenteditable), this is wrong, but we don't
// have a use case for that yet and it's easy to add a separate
// `browser_append` tool later if it comes up.
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

	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensureLaunched(); err != nil {
		return "", err
	}

	if err := m.page.Fill(args.Selector, args.Text, playwright.PageFillOptions{
		Timeout: playwright.Float(float64(DefaultTimeout.Milliseconds())),
	}); err != nil {
		return "", fmt.Errorf("browser_type: failed to fill %q: %w", args.Selector, err)
	}
	return fmt.Sprintf("browser_type: filled %q with %d chars", args.Selector, len(args.Text)), nil
}

// ExecuteGetText returns the text content of the first element
// matching selector, or the page body's text if selector is empty /
// "body". We cap the result at MaxResultBytes to keep the transcript
// from blowing up on a giant page.
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

	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensureLaunched(); err != nil {
		return "", err
	}

	text, err := m.page.InnerText(selector, playwright.PageInnerTextOptions{
		Timeout: playwright.Float(float64(DefaultTimeout.Milliseconds())),
	})
	if err != nil {
		return "", fmt.Errorf("browser_get_text: failed to read %q: %w", selector, err)
	}
	return fmt.Sprintf("browser_get_text: %q =\n%s", selector, truncateResult(text, MaxResultBytes)), nil
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
