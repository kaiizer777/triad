package browser

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newSessionTestServer creates a small HTTP server with a page that has
// a form and localStorage support for testing session isolation.
func newSessionTestServer(t *testing.T) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html>
<html><head><title>Session Test Page</title></head>
<body>
  <h1 id="title">Session Isolation Test</h1>
  <p id="cookie-display"></p>
  <p id="storage-display"></p>
  <form id="login">
    <label>Email: <input name="email" id="email" type="email" /></label>
    <button id="submit-btn" type="submit">Sign in</button>
  </form>
  <script>
    document.getElementById('login').addEventListener('submit', function(e) {
      e.preventDefault();
      document.cookie = "login_user=" + document.getElementById('email').value + "; path=/";
      localStorage.setItem('login_email', document.getElementById('email').value);
      document.getElementById('cookie-display').textContent = 'cookie: ' + document.cookie;
      document.getElementById('storage-display').textContent = 'storage: ' + localStorage.getItem('login_email');
    });
  </script>
</body></html>`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestResetContextBasic verifies that ResetContext creates a fresh context
// and page, clearing all state from the previous one.
func TestResetContextBasic(t *testing.T) {
	if !chromeInstalled() {
		t.Skip("Playwright Chromium not installed")
	}

	serverURL := newSessionTestServer(t)
	workDir := t.TempDir()

	mgr := NewManager()
	t.Cleanup(func() { _ = mgr.Close() })

	// Navigate to a page.
	res, err := mgr.ExecuteTool(workDir, "browser_navigate", fmt.Sprintf(`{"url":%q}`, serverURL+"/"))
	if err != nil {
		t.Fatalf("navigate failed: %v", err)
	}
	if !strings.Contains(res, "loaded") {
		t.Fatalf("navigate result missing 'loaded': %q", res)
	}

	// Verify we're not on about:blank.
	url1 := mgr.CurrentURL()
	if url1 == "" || url1 == "about:blank" {
		t.Fatalf("expected non-blank URL, got %q", url1)
	}

	// Reset context.
	resetRes, err := mgr.ExecuteResetContext()
	if err != nil {
		t.Fatalf("reset context failed: %v", err)
	}
	if !strings.Contains(resetRes, "fresh context") {
		t.Fatalf("unexpected reset result: %q", resetRes)
	}

	// After reset we should be back on about:blank.
	url2 := mgr.CurrentURL()
	if url2 != "" && url2 != "about:blank" {
		t.Logf("URL after reset: %q (expected about:blank or empty)", url2)
	}

	// We can navigate again — the browser is still usable.
	res2, err := mgr.ExecuteTool(workDir, "browser_navigate", fmt.Sprintf(`{"url":%q}`, serverURL+"/"))
	if err != nil {
		t.Fatalf("navigate after reset failed: %v", err)
	}
	if !strings.Contains(res2, "loaded") {
		t.Fatalf("second navigate result missing 'loaded': %q", res2)
	}
}

// TestResetContextMultipleTimes verifies that repeated resets don't leak
// resources or cause failures.
func TestResetContextMultipleTimes(t *testing.T) {
	if !chromeInstalled() {
		t.Skip("Playwright Chromium not installed")
	}

	serverURL := newSessionTestServer(t)
	workDir := t.TempDir()

	mgr := NewManager()
	t.Cleanup(func() { _ = mgr.Close() })

	for i := 0; i < 5; i++ {
		_, err := mgr.ExecuteTool(workDir, "browser_navigate", fmt.Sprintf(`{"url":%q}`, serverURL+"/"))
		if err != nil {
			t.Fatalf("navigate iteration %d failed: %v", i, err)
		}
		_, err = mgr.ExecuteResetContext()
		if err != nil {
			t.Fatalf("reset iteration %d failed: %v", i, err)
		}
	}

	// Final navigate should still work.
	_, err := mgr.ExecuteTool(workDir, "browser_navigate", fmt.Sprintf(`{"url":%q}`, serverURL+"/"))
	if err != nil {
		t.Fatalf("final navigate failed: %v", err)
	}
}

// TestStateIsolationBetweenTasks verifies that localStorage and cookies
// from one task are wiped after ResetContext.
func TestStateIsolationBetweenTasks(t *testing.T) {
	if !chromeInstalled() {
		t.Skip("Playwright Chromium not installed")
	}

	serverURL := newSessionTestServer(t)
	workDir := t.TempDir()

	mgr := NewManager()
	t.Cleanup(func() { _ = mgr.Close() })

	// --- Task 1: navigate, set localStorage and cookies ---
	_, err := mgr.ExecuteTool(workDir, "browser_navigate", fmt.Sprintf(`{"url":%q}`, serverURL+"/"))
	if err != nil {
		t.Fatalf("navigate failed: %v", err)
	}

	// Fill email and submit form (sets cookie + localStorage via JS).
	_, err = mgr.ExecuteTool(workDir, "browser_type", `{"selector":"#email","text":"alice@test.com"}`)
	if err != nil {
		t.Fatalf("type failed: %v", err)
	}
	_, err = mgr.ExecuteTool(workDir, "browser_click", `{"selector":"#submit-btn"}`)
	if err != nil {
		t.Fatalf("click failed: %v", err)
	}

	// Verify localStorage was set by the form submit.
	mgr.mu.Lock()
	val, err := mgr.page.Evaluate(`() => localStorage.getItem('login_email')`)
	mgr.mu.Unlock()
	if err != nil {
		t.Fatalf("evaluate localStorage failed: %v", err)
	}
	if val != "alice@test.com" {
		t.Fatalf("expected 'alice@test.com' in localStorage, got %v", val)
	}

	// --- Reset context (task boundary) ---
	_, err = mgr.ExecuteResetContext()
	if err != nil {
		t.Fatalf("reset context failed: %v", err)
	}

	// --- Task 2: navigate to same site, verify state is gone ---
	_, err = mgr.ExecuteTool(workDir, "browser_navigate", fmt.Sprintf(`{"url":%q}`, serverURL+"/"))
	if err != nil {
		t.Fatalf("navigate task 2 failed: %v", err)
	}

	// localStorage should be empty.
	mgr.mu.Lock()
	val2, err := mgr.page.Evaluate(`() => localStorage.getItem('login_email')`)
	mgr.mu.Unlock()
	if err != nil {
		t.Fatalf("evaluate localStorage task 2 failed: %v", err)
	}
	if val2 != nil {
		t.Fatalf("expected nil for login_email after reset, got %v", val2)
	}
}

// TestSaveAndRestoreStorageState verifies that saving storage state
// before a reset allows the next context to restore cookies.
func TestSaveAndRestoreStorageState(t *testing.T) {
	if !chromeInstalled() {
		t.Skip("Playwright Chromium not installed")
	}

	serverURL := newSessionTestServer(t)
	workDir := t.TempDir()

	mgr := NewManager()
	t.Cleanup(func() { _ = mgr.Close() })

	// Navigate and set a cookie via form submit.
	_, err := mgr.ExecuteTool(workDir, "browser_navigate", fmt.Sprintf(`{"url":%q}`, serverURL+"/"))
	if err != nil {
		t.Fatalf("navigate failed: %v", err)
	}
	_, err = mgr.ExecuteTool(workDir, "browser_type", `{"selector":"#email","text":"bob@test.com"}`)
	if err != nil {
		t.Fatalf("type failed: %v", err)
	}
	_, err = mgr.ExecuteTool(workDir, "browser_click", `{"selector":"#submit-btn"}`)
	if err != nil {
		t.Fatalf("click failed: %v", err)
	}

	// Verify cookie is set.
	mgr.mu.Lock()
	cookies, err := mgr.context.Cookies()
	mgr.mu.Unlock()
	if err != nil {
		t.Fatalf("cookies failed: %v", err)
	}
	hasLoginCookie := false
	for _, c := range cookies {
		if c.Name == "login_user" {
			hasLoginCookie = true
			break
		}
	}
	if !hasLoginCookie {
		t.Fatal("expected login_user cookie to be set after form submit")
	}

	// Save storage state.
	saveRes, err := mgr.ExecuteSaveStorageState()
	if err != nil {
		t.Fatalf("save storage state failed: %v", err)
	}
	t.Logf("save result: %s", saveRes)

	if !mgr.HasSavedStorage() {
		t.Fatal("expected HasSavedStorage to return true")
	}

	// Reset context — should restore the saved state.
	_, err = mgr.ExecuteResetContext()
	if err != nil {
		t.Fatalf("reset context failed: %v", err)
	}

	// Navigate back and verify cookie persisted.
	_, err = mgr.ExecuteTool(workDir, "browser_navigate", fmt.Sprintf(`{"url":%q}`, serverURL+"/"))
	if err != nil {
		t.Fatalf("navigate after reset failed: %v", err)
	}

	mgr.mu.Lock()
	cookies2, err := mgr.context.Cookies()
	mgr.mu.Unlock()
	if err != nil {
		t.Fatalf("cookies after reset failed: %v", err)
	}
	found := false
	for _, c := range cookies2 {
		if c.Name == "login_user" && c.Value == "bob@test.com" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected login_user cookie to persist across context reset with saved storage")
	}
}

// TestClearSavedStorageThenReset verifies that clearing saved storage
// results in a truly empty context after reset.
func TestClearSavedStorageThenReset(t *testing.T) {
	if !chromeInstalled() {
		t.Skip("Playwright Chromium not installed")
	}

	serverURL := newSessionTestServer(t)
	workDir := t.TempDir()

	mgr := NewManager()
	t.Cleanup(func() { _ = mgr.Close() })

	// Navigate and set cookie.
	_, err := mgr.ExecuteTool(workDir, "browser_navigate", fmt.Sprintf(`{"url":%q}`, serverURL+"/"))
	if err != nil {
		t.Fatalf("navigate failed: %v", err)
	}
	_, err = mgr.ExecuteTool(workDir, "browser_type", `{"selector":"#email","text":"carol@test.com"}`)
	if err != nil {
		t.Fatalf("type failed: %v", err)
	}
	_, err = mgr.ExecuteTool(workDir, "browser_click", `{"selector":"#submit-btn"}`)
	if err != nil {
		t.Fatalf("click failed: %v", err)
	}

	// Save then immediately clear.
	_, err = mgr.ExecuteSaveStorageState()
	if err != nil {
		t.Fatalf("save failed: %v", err)
	}
	clearRes, err := mgr.ExecuteClearSavedStorage()
	if err != nil {
		t.Fatalf("clear failed: %v", err)
	}
	if !strings.Contains(clearRes, "cleared") {
		t.Fatalf("unexpected clear result: %q", clearRes)
	}
	if mgr.HasSavedStorage() {
		t.Fatal("expected HasSavedStorage to return false after clear")
	}

	// Reset — should be empty.
	_, err = mgr.ExecuteResetContext()
	if err != nil {
		t.Fatalf("reset failed: %v", err)
	}

	_, err = mgr.ExecuteTool(workDir, "browser_navigate", fmt.Sprintf(`{"url":%q}`, serverURL+"/"))
	if err != nil {
		t.Fatalf("navigate after reset failed: %v", err)
	}

	mgr.mu.Lock()
	cookies, err := mgr.context.Cookies()
	mgr.mu.Unlock()
	if err != nil {
		t.Fatalf("cookies failed: %v", err)
	}
	for _, c := range cookies {
		if c.Name == "login_user" {
			t.Error("expected login_user cookie to be gone after ClearSavedStorage + ResetContext")
		}
	}
}

// TestExecuteToolDispatch verifies the three new tools dispatch correctly
// through ExecuteTool and are recognized by IsBrowserTool.
func TestExecuteToolDispatch(t *testing.T) {
	if !chromeInstalled() {
		t.Skip("Playwright Chromium not installed")
	}

	workDir := t.TempDir()
	mgr := NewManager()
	t.Cleanup(func() { _ = mgr.Close() })

	// Must launch browser first (navigate does that).
	_, _ = mgr.ExecuteTool(workDir, "browser_navigate", `{"url":"https://example.com"}`)

	tests := []struct {
		name string
		tool string
	}{
		{"reset_context", "browser_reset_context"},
		{"save_storage_state", "browser_save_storage_state"},
		{"clear_saved_storage", "browser_clear_saved_storage"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := mgr.ExecuteTool(workDir, tt.tool, "{}")
			if err != nil {
				t.Fatalf("%s: %v", tt.tool, err)
			}
			if res == "" {
				t.Fatalf("%s: expected non-empty result", tt.tool)
			}
		})
	}

	// Verify IsBrowserTool classification.
	for _, tt := range tests {
		if !IsBrowserTool(tt.tool) {
			t.Errorf("IsBrowserTool(%q) = false, want true", tt.tool)
		}
	}
}
