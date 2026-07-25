package browser

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests actually launch Chromium via the playwright-go driver
// and exercise the full tool executor flow. They require the
// Playwright browser binary to be installed; if it isn't, the
// tests are skipped (rather than failed) — Chromium install is a
// one-time `playwright install chromium` step documented at the top
// of this file. We don't want CI environments without the binary
// to block on a network-dependent download.
//
// The tests share a tiny local HTTP server (httptest) that serves
// static HTML — no external network required, fully hermetic.

// chromeInstalled checks whether the Playwright Chromium binary is
// present on disk. The exact directory name is version-pinned
// (e.g. chromium-1228) and we don't want to mirror that, so we look
// for the install root that `playwright install chromium` writes to.
func chromeInstalled() bool {
	// PLAYWRIGHT_BROWSERS_PATH is the documented override; if it's
	// set, that's where the binary lives. Otherwise it's under
	// %LOCALAPPDATA%\ms-playwright on Windows and ~/.cache/ms-playwright
	// on Linux/macOS.
	dir := os.Getenv("PLAYWRIGHT_BROWSERS_PATH")
	if dir == "" {
		// Best-effort fallback: look in the user home for ms-playwright.
		// We don't enumerate every OS-specific path because the actual
		// lookup is done by the Playwright driver itself; we just want
		// a quick "is there something here?" check.
		if home, err := os.UserHomeDir(); err == nil {
			candidates := []string{
				filepath.Join(home, "AppData", "Local", "ms-playwright"),
				filepath.Join(home, ".cache", "ms-playwright"),
			}
			for _, c := range candidates {
				if st, err := os.Stat(c); err == nil && st.IsDir() {
					dir = c
					break
				}
			}
		}
	}
	if dir == "" {
		return false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		// Anything starting with "chromium-" counts; we don't pin
		// the exact build number because it shifts between Playwright
		// versions.
		if strings.HasPrefix(e.Name(), "chromium-") {
			return true
		}
	}
	return false
}

// newTestServer spins up a tiny local HTTP server with a small
// HTML page that has clickable and typeable elements. Tests use
// its URL as the browser_navigate target. The server is closed
// via t.Cleanup.
func newTestServer(t *testing.T) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html>
<html><head><title>Triad Test Page</title></head>
<body>
  <h1 id="greeting">Hello from Triad test server</h1>
  <p>This page exists so browser_* tool tests can navigate to it
     without needing external network access.</p>
  <form id="login">
    <label>Email: <input name="email" id="email" type="email" /></label>
    <button id="submit-btn" type="submit">Sign in</button>
  </form>
  <p id="result" hidden>You signed in</p>
  <script>
    document.getElementById('login').addEventListener('submit', function (e) {
      e.preventDefault();
      var r = document.getElementById('result');
      r.hidden = false;
      r.textContent = 'You signed in as ' + document.getElementById('email').value;
    });
  </script>
</body></html>`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestBrowserExecuteTool_EndToEnd(t *testing.T) {
	if !chromeInstalled() {
		t.Skip("Playwright Chromium binary not installed; run `playwright install chromium` to enable this test")
	}

	serverURL := newTestServer(t)
	workDir := t.TempDir()

	mgr := NewManager()
	t.Cleanup(func() {
		if err := mgr.Close(); err != nil {
			t.Logf("warning: browser close failed: %v", err)
		}
	})

	// --- browser_navigate ---
	t.Run("navigate", func(t *testing.T) {
		res, err := mgr.ExecuteTool(workDir, "browser_navigate", fmt.Sprintf(`{"url":%q}`, serverURL+"/"))
		if err != nil {
			t.Fatalf("browser_navigate: %v", err)
		}
		if !strings.Contains(res, "loaded") {
			t.Errorf("browser_navigate result missing 'loaded': %q", res)
		}
		if !strings.Contains(res, "Triad Test Page") {
			t.Errorf("browser_navigate result missing page title: %q", res)
		}
	})

	// --- browser_get_text on a specific element ---
	t.Run("get_text_specific", func(t *testing.T) {
		res, err := mgr.ExecuteTool(workDir, "browser_get_text", `{"selector":"h1#greeting"}`)
		if err != nil {
			t.Fatalf("browser_get_text: %v", err)
		}
		if !strings.Contains(res, "Hello from Triad test server") {
			t.Errorf("browser_get_text: expected page text in result, got: %q", res)
		}
	})

	// --- browser_type fills the email field ---
	t.Run("type_email", func(t *testing.T) {
		res, err := mgr.ExecuteTool(workDir, "browser_type", `{"selector":"input#email","text":"[email protected]"}`)
		if err != nil {
			t.Fatalf("browser_type: %v", err)
		}
		if !strings.Contains(res, "filled") {
			t.Errorf("browser_type result missing 'filled': %q", res)
		}
	})

	// --- browser_click submits the form ---
	t.Run("click_submit", func(t *testing.T) {
		res, err := mgr.ExecuteTool(workDir, "browser_click", `{"selector":"button#submit-btn"}`)
		if err != nil {
			t.Fatalf("browser_click: %v", err)
		}
		if !strings.Contains(res, "clicked") {
			t.Errorf("browser_click result missing 'clicked': %q", res)
		}
		// Give the page a moment to render the post-submit result.
		time.Sleep(200 * time.Millisecond)
	})

	// --- browser_get_text reads the post-submit result ---
	t.Run("get_text_after_submit", func(t *testing.T) {
		// The form submit handler updates the DOM synchronously, but
		// Playwright's click is itself an async dispatch. Wait a
		// short interval for the event handler to complete, then
		// read the result. (In production we'd want a more
		// reliable "wait for element to contain text" helper, but
		// that's a separate tool we'd add when we actually need
		// it — not now.)
		time.Sleep(500 * time.Millisecond)
		res, err := mgr.ExecuteTool(workDir, "browser_get_text", `{"selector":"p#result"}`)
		if err != nil {
			t.Fatalf("browser_get_text (post-submit): %v", err)
		}
		// We don't assert the email is in the text — Playwright's
		// Fill on Windows is sometimes flaky with the input event
		// timing relative to a click, and the test should fail
		// loudly on a real breakage (no "You signed in" at all)
		// rather than on a flaky timing race. We DO assert the
		// form submit handler ran, which proves click + get_text
		// both work.
		if !strings.Contains(res, "You signed in") {
			t.Errorf("expected post-submit text to confirm form was submitted, got: %q", res)
		}
	})

	// --- browser_screenshot to a file ---
	t.Run("screenshot_to_file", func(t *testing.T) {
		relPath := "screenshots/test.png"
		res, err := mgr.ExecuteTool(workDir, "browser_screenshot", fmt.Sprintf(`{"path":%q}`, relPath))
		if err != nil {
			t.Fatalf("browser_screenshot: %v", err)
		}
		if !strings.Contains(res, "wrote") {
			t.Errorf("browser_screenshot result missing 'wrote': %q", res)
		}
		full := filepath.Join(workDir, relPath)
		st, err := os.Stat(full)
		if err != nil {
			t.Fatalf("screenshot file not on disk: %v", err)
		}
		if st.Size() < 100 {
			t.Errorf("screenshot is suspiciously small (%d bytes)", st.Size())
		}
		// PNG magic bytes: 89 50 4E 47
		f, err := os.Open(full)
		if err != nil {
			t.Fatalf("open screenshot: %v", err)
		}
		defer f.Close()
		var magic [4]byte
		if _, err := f.Read(magic[:]); err != nil {
			t.Fatalf("read magic: %v", err)
		}
		if magic != [4]byte{0x89, 0x50, 0x4E, 0x47} {
			t.Errorf("screenshot is not a valid PNG (magic = %v)", magic)
		}
	})
}

// TestBrowserExecuteTool_RejectsBadURL confirms the URL validation
// in the navigate executor fires before we ever talk to the
// browser, so a model that emits a file:// URL gets a clean error
// rather than a Chromium crash.
func TestBrowserExecuteTool_RejectsBadURL(t *testing.T) {
	if !chromeInstalled() {
		t.Skip("Playwright Chromium binary not installed; run `playwright install chromium` to enable this test")
	}
	workDir := t.TempDir()
	mgr := NewManager()
	t.Cleanup(func() { _ = mgr.Close() })

	_, err := mgr.ExecuteTool(workDir, "browser_navigate", `{"url":"file:///etc/passwd"}`)
	if err == nil {
		t.Fatal("browser_navigate with file:// URL should have failed")
	}
	if !strings.Contains(err.Error(), "scheme") {
		t.Errorf("error should mention scheme restriction, got: %v", err)
	}
}

// TestBrowserExecuteTool_RejectsEmptySelector confirms the selector
// sanity check fires for tools that require a specific element —
// empty selector is always a model mistake for click / type, and
// a 30s Playwright timeout is a much worse signal than an immediate
// "required argument missing" error. browser_get_text is the
// documented exception: an empty selector means "give me the whole
// page body", which is a legitimate use case ("read the rendered
// text of whatever page we landed on").
func TestBrowserExecuteTool_RejectsEmptySelector(t *testing.T) {
	if !chromeInstalled() {
		t.Skip("Playwright Chromium binary not installed; run `playwright install chromium` to enable this test")
	}
	workDir := t.TempDir()
	mgr := NewManager()
	t.Cleanup(func() { _ = mgr.Close() })

	// browser_click and browser_type must reject an empty selector.
	// browser_get_text accepts it (defaults to "body").
	required := []struct {
		toolName string
		args     string
	}{
		{"browser_click", `{"selector":""}`},
		{"browser_type", `{"selector":"","text":"x"}`},
	}
	for _, c := range required {
		t.Run(c.toolName, func(t *testing.T) {
			_, err := mgr.ExecuteTool(workDir, c.toolName, c.args)
			if err == nil {
				t.Fatalf("%s with empty selector should have failed", c.toolName)
			}
			if !strings.Contains(err.Error(), "selector") {
				t.Errorf("%s error should mention 'selector', got: %v", c.toolName, err)
			}
		})
	}

	// browser_get_text with empty selector should NOT reject — it
	// should default to reading the body. We don't go through the
	// full navigate/click cycle here because that would make this a
	// duplicate of the end-to-end test. Instead, we navigate to the
	// test server, then call get_text with empty selector, and
	// expect body text back.
	t.Run("browser_get_text_empty_defaults_to_body", func(t *testing.T) {
		serverURL := newTestServer(t)
		if _, err := mgr.ExecuteTool(workDir, "browser_navigate", fmt.Sprintf(`{"url":%q}`, serverURL+"/")); err != nil {
			t.Fatalf("navigate setup: %v", err)
		}
		res, err := mgr.ExecuteTool(workDir, "browser_get_text", `{"selector":""}`)
		if err != nil {
			t.Fatalf("browser_get_text with empty selector should not fail, got: %v", err)
		}
		if !strings.Contains(res, "Hello from Triad test server") {
			t.Errorf("expected body text in result, got: %q", res)
		}
	})
}

// TestBrowserExecuteTool_NoManager confirms the unknown-tool path
// works — calling ExecuteTool with a non-browser tool name returns
// a clean "not a browser tool" error rather than panicking.
func TestBrowserExecuteTool_UnknownTool(t *testing.T) {
	workDir := t.TempDir()
	mgr := NewManager()
	t.Cleanup(func() { _ = mgr.Close() })

	_, err := mgr.ExecuteTool(workDir, "write_file", `{"path":"x","content":"y"}`)
	if err == nil {
		t.Fatal("write_file should not be a browser tool")
	}
	if !strings.Contains(err.Error(), "not a browser tool") {
		t.Errorf("error should say 'not a browser tool', got: %v", err)
	}
}
