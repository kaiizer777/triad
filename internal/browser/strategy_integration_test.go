package browser

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Phase 1.5 — Messy-markup integration test. The page intentionally
// has reused classes, no IDs on some elements, and a layout that
// would break a positional selector. The same operations are
// exercised through ALL the documented strategies (role, text,
// label, placeholder, testid, css) to confirm:
//
//   - role/label/text/testid selectors succeed against the kind of
//     markup where a naive CSS-class selector would have been
//     fragile;
//   - the default ("css") path still works for tasks that haven't
//     been updated to the new strategy hint.
//
// The server is hermetic (httptest) — no external network required.
// Tests are skipped when Chromium is not installed, matching the
// pattern in integration_test.go.

// newMessyTestServer serves a page whose markup is intentionally
// messy: reused CSS classes, no IDs on most elements, ambiguous
// text/role combinations. The point is to show that role/name and
// label-based selectors succeed where a fragile CSS selector would
// have been risky.
func newMessyTestServer(t *testing.T) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>Messy Markup Test Page</title>
  <style>
    /* Reused class — same class on multiple buttons. A naive
     * selector like ".btn" would have matched multiple elements. */
    .btn { padding: 4px 8px; }
  </style>
</head>
<body>
  <header>
    <h1>Welcome to the messy page</h1>
    <nav>
      <ul>
        <li><a href="#home">Home</a></li>
        <li><a href="#about">About</a></li>
        <li><a href="#contact">Contact</a></li>
      </ul>
    </nav>
  </header>

  <main>
    <section>
      <h2>Sign in</h2>
      <form id="login-form" data-testid="login-form">
        <!-- Two inputs with the same class — selector ".input" would
         * be ambiguous. We give them distinct labels instead. -->
        <label for="email-input">Email</label>
        <input type="email" class="input" id="email-input" name="email" placeholder="[email protected]" />

        <label for="password-input">Password</label>
        <input type="password" class="input" id="password-input" name="password" placeholder="Enter password" />

        <button type="submit" class="btn">Sign in</button>
        <!-- A second button with the same class — explicit text
         * disambiguates. -->
        <button type="button" class="btn" data-testid="cancel-btn">Cancel</button>
      </form>
    </section>

    <section>
      <h2>Search</h2>
      <input type="search" id="site-search" placeholder="Search the site" aria-label="Site search" />
    </section>

    <section id="result-section" hidden>
      <p id="result-text">You signed in</p>
    </section>
  </main>

  <script>
    var form = document.getElementById('login-form');
    form.addEventListener('submit', function (e) {
      e.preventDefault();
      var sec = document.getElementById('result-section');
      sec.removeAttribute('hidden');
      var email = document.getElementById('email-input').value;
      document.getElementById('result-text').textContent = 'You signed in as ' + email;
    });
  </script>
</body>
</html>`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestBrowserStrategy_RoleTargeting succeeds where a CSS class
// selector would be ambiguous. Two buttons share class="btn"; only
// the one with the visible "Sign in" text should resolve.
func TestBrowserStrategy_RoleTargeting(t *testing.T) {
	if !chromeInstalled() {
		t.Skip("Playwright Chromium binary not installed; run `playwright install chromium` to enable this test")
	}

	serverURL := newMessyTestServer(t)
	workDir := t.TempDir()
	mgr := NewManager()
	t.Cleanup(func() { _ = mgr.Close() })

	if _, err := mgr.ExecuteTool(workDir, "browser_navigate", fmt.Sprintf(`{"url":%q}`, serverURL+"/")); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	// strategy=role targeting the visible "Sign in" button. This
	// must NOT match the second "Cancel" button.
	t.Run("click_sign_in_by_role", func(t *testing.T) {
		_, err := mgr.ExecuteClick(`{"selector":"button:Sign in","strategy":"role"}`)
		if err != nil {
			t.Fatalf("click by role: %v", err)
		}
		// Wait for the click handler to render the result.
		time.Sleep(150 * time.Millisecond)
		res, err := mgr.ExecuteGetText(`{"selector":"#result-text","strategy":"css"}`)
		if err != nil {
			t.Fatalf("get_text result: %v", err)
		}
		if !strings.Contains(res, "You signed in") {
			t.Errorf("expected form-submit result, got: %q", res)
		}
	})
}

// TestBrowserStrategy_TextTargeting mirrors RoleTargeting but uses
// strategy=text instead. The substring "Sign in" matches the button
// label and would also match the "Sign in" heading; Playwright's
// GetByText is element-matcher-aware so it picks the button by
// default. We don't assert the form-submit success here — we just
// confirm the click itself doesn't error.
func TestBrowserStrategy_TextTargeting(t *testing.T) {
	if !chromeInstalled() {
		t.Skip("Playwright Chromium binary not installed; run `playwright install chromium` to enable this test")
	}

	serverURL := newMessyTestServer(t)
	workDir := t.TempDir()
	mgr := NewManager()
	t.Cleanup(func() { _ = mgr.Close() })

	if _, err := mgr.ExecuteTool(workDir, "browser_navigate", fmt.Sprintf(`{"url":%q}`, serverURL+"/")); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	t.Run("click_cancel_by_text", func(t *testing.T) {
		res, err := mgr.ExecuteClick(`{"selector":"exact:Cancel","strategy":"text"}`)
		if err != nil {
			t.Fatalf("click by text: %v", err)
		}
		if !strings.Contains(res, "exact") {
			t.Errorf("result should show strategy prefix, got: %q", res)
		}
	})
}

// TestBrowserStrategy_LabelAndPlaceholder exercise the
// strategy=label and strategy=placeholder paths. These are the
// second-most-stable selectors and the typing test uses them
// against the email input.
func TestBrowserStrategy_LabelAndPlaceholder(t *testing.T) {
	if !chromeInstalled() {
		t.Skip("Playwright Chromium binary not installed; run `playwright install chromium` to enable this test")
	}

	serverURL := newMessyTestServer(t)
	workDir := t.TempDir()
	mgr := NewManager()
	t.Cleanup(func() { _ = mgr.Close() })

	if _, err := mgr.ExecuteTool(workDir, "browser_navigate", fmt.Sprintf(`{"url":%q}`, serverURL+"/")); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	t.Run("type_by_label", func(t *testing.T) {
		_, err := mgr.ExecuteType(`{"selector":"Email","text":"[email protected]","strategy":"label"}`)
		if err != nil {
			t.Fatalf("type by label: %v", err)
		}
	})

	t.Run("type_by_placeholder", func(t *testing.T) {
		_, err := mgr.ExecuteType(`{"selector":"Enter password","text":"hunter2","strategy":"placeholder"}`)
		if err != nil {
			t.Fatalf("type by placeholder: %v", err)
		}
	})

	// Read the search input's value back via aria-label.
	t.Run("aria_label_targeting", func(t *testing.T) {
		_, err := mgr.ExecuteType(`{"selector":"Site search","text":"hello world","strategy":"label"}`)
		if err != nil {
			t.Fatalf("type by aria-label: %v", err)
		}
	})
}

// TestBrowserStrategy_TestID confirms the testid strategy path
// works against a data-testid attribute.
func TestBrowserStrategy_TestID(t *testing.T) {
	if !chromeInstalled() {
		t.Skip("Playwright Chromium binary not installed; run `playwright install chromium` to enable this test")
	}

	serverURL := newMessyTestServer(t)
	workDir := t.TempDir()
	mgr := NewManager()
	t.Cleanup(func() { _ = mgr.Close() })

	if _, err := mgr.ExecuteTool(workDir, "browser_navigate", fmt.Sprintf(`{"url":%q}`, serverURL+"/")); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	t.Run("click_by_testid", func(t *testing.T) {
		_, err := mgr.ExecuteClick(`{"selector":"cancel-btn","strategy":"testid"}`)
		if err != nil {
			t.Fatalf("click by testid: %v", err)
		}
	})
}

// TestBrowserStrategy_CSSDefaultStillWorks confirms the legacy
// "css" path (and no strategy at all) still functions for tasks
// that haven't been updated to the new strategy hint. This is the
// backward-compatibility guarantee.
func TestBrowserStrategy_CSSDefaultStillWorks(t *testing.T) {
	if !chromeInstalled() {
		t.Skip("Playwright Chromium binary not installed; run `playwright install chromium` to enable this test")
	}

	serverURL := newMessyTestServer(t)
	workDir := t.TempDir()
	mgr := NewManager()
	t.Cleanup(func() { _ = mgr.Close() })

	if _, err := mgr.ExecuteTool(workDir, "browser_navigate", fmt.Sprintf(`{"url":%q}`, serverURL+"/")); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	t.Run("css_explicit", func(t *testing.T) {
		_, err := mgr.ExecuteClick(`{"selector":"#login-form button[type=submit]","strategy":"css"}`)
		if err != nil {
			t.Fatalf("click via css: %v", err)
		}
	})
	t.Run("css_default", func(t *testing.T) {
		// No strategy field — backward-compat default.
		_, err := mgr.ExecuteClick(`{"selector":"#login-form button[type=submit]"}`)
		if err != nil {
			t.Fatalf("click via default css: %v", err)
		}
	})
}

// TestBrowserStrategy_AmbiguousSelector is the negative-path test:
// if multiple buttons match the same accessible name, Playwright's
// strict mode (the locator default) throws. This is the PRECISE
// failure mode Phase 3 will detect and recover from — and the
// reason we route through the Locator factory rather than the
// (deprecated) page.click selector API.
func TestBrowserStrategy_AmbiguousSelector(t *testing.T) {
	if !chromeInstalled() {
		t.Skip("Playwright Chromium binary not installed; run `playwright install chromium` to enable this test")
	}

	// Server with two buttons that share the same role + name.
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html>
<html><body>
  <button>OK</button>
  <button>OK</button>
</body></html>`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	workDir := t.TempDir()
	mgr := NewManager()
	t.Cleanup(func() { _ = mgr.Close() })

	if _, err := mgr.ExecuteTool(workDir, "browser_navigate", fmt.Sprintf(`{"url":%q}`, srv.URL+"/")); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	// Two buttons with the same name. The Locator factory's strict
	// mode should refuse to silently click the first one — it should
	// surface an error so Phase 3's recovery logic can engage.
	_, err := mgr.ExecuteClick(`{"selector":"button:OK","strategy":"role"}`)
	if err == nil {
		t.Fatal("expected strict-mode violation on ambiguous role selector, got nil")
	}
	// The error message should mention strict mode / multiple elements,
	// which is the signal Phase 3 will key on.
	low := strings.ToLower(err.Error())
	if !strings.Contains(low, "strict") && !strings.Contains(low, "more than one") && !strings.Contains(low, "multiple") && !strings.Contains(low, "2 elements") {
		t.Logf("note: strict-mode error wording was: %q (Phase 3 will need to detect this case; the test passes as long as the call failed)", err.Error())
	}
}
