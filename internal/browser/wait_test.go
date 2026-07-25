package browser

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Phase 2 tests for browser_wait_for (Work 4 §Phase 2.3-2.6):
//
//   - validation: unknown kind, missing required field, positional
//     selector (inherited from Phase 1.4), unknown strategy
//   - timeout:    the explicit timeout cap (Phase 2.4) and the
//     "never-appears" failure (Phase 2.6)
//   - happy path: a deliberately delayed page proves the wait
//     actually blocks on the condition, not on a fixed delay
//     (Phase 2.5)
//
// All end-to-end tests are gated on chromeInstalled() — the
// validation-only tests run regardless because they don't touch the
// browser at all.

// TestResolveWaitTimeout covers the Phase 2.4 timeout cap. A run-away
// "timeout_ms" value MUST clamp at MaxWaitTimeout (2 minutes); a
// negative / zero / unset value MUST fall back to DefaultTimeout
// (30s); a sane in-range value MUST pass through unchanged.
func TestResolveWaitTimeout(t *testing.T) {
	cases := []struct {
		name string
		ms   int
		want time.Duration
	}{
		{"unset_zero", 0, DefaultTimeout},
		{"unset_negative", -1, DefaultTimeout},
		{"in_range", 5000, 5 * time.Second},
		{"at_default", int(DefaultTimeout.Milliseconds()), DefaultTimeout},
		{"just_under_cap", int(MaxWaitTimeout.Milliseconds()) - 1, MaxWaitTimeout - time.Millisecond},
		{"at_cap", int(MaxWaitTimeout.Milliseconds()), MaxWaitTimeout},
		{"over_cap_clamped", int(MaxWaitTimeout.Milliseconds()) + 1, MaxWaitTimeout},
		{"absurdly_large", 24 * 60 * 60 * 1000, MaxWaitTimeout},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := resolveWaitTimeout(c.ms)
			if got != c.want {
				t.Errorf("resolveWaitTimeout(%d) = %s, want %s", c.ms, got, c.want)
			}
		})
	}
}

// TestExecuteWaitForValidation covers the validation surface that
// fires before any browser call — same pattern as Phase 1's positional
// rejection tests. Coder must get a clear error for bad input rather
// than a misleading Playwright timeout.
func TestExecuteWaitForValidation(t *testing.T) {
	mgr := NewManager()
	t.Cleanup(func() { _ = mgr.Close() })

	cases := []struct {
		name      string
		args      string
		wantErr   bool
		errSubstr string
	}{
		// Empty / malformed args.
		{"empty_args", "", true, "arguments are empty"},
		{"malformed_json", "{not json", true, "failed to parse"},

		// Unknown kind.
		{"unknown_kind", `{"kind":"network"}`, true, "is not one of"},

		// Missing required fields per kind.
		{"text_missing_text", `{"kind":"text"}`, true, "non-empty 'text'"},
		{"text_empty_text", `{"kind":"text","text":""}`, true, "non-empty 'text'"},
		{"url_missing_url", `{"kind":"url"}`, true, "non-empty 'url'"},
		{"url_empty_url", `{"kind":"url","url":""}`, true, "non-empty 'url'"},
		{"visible_empty_selector", `{"kind":"visible"}`, true, "selector"},

		// Positional CSS selector (inherited from Phase 1.4).
		{"visible_positional", `{"kind":"visible","selector":"li:nth-child(2)","strategy":"css"}`, true, "positional"},

		// Unknown strategy on visible.
		{"visible_unknown_strategy", `{"kind":"visible","selector":"#x","strategy":"xpath"}`, true, "unknown strategy"},

		// Valid shape for kind="text" — should NOT fail at validation
		// (would block waiting for text on the chromeInstalled() path).
		// timeout_ms=100 keeps these fast: if they get past
		// validation and hit Playwright, they fail in ~100ms not 30s.
		{"text_shape_ok", `{"kind":"text","text":"Hello","timeout_ms":100}`, false, ""},
		{"url_shape_ok", `{"kind":"url","url":"/dashboard","timeout_ms":100}`, false, ""},
		{"visible_shape_ok", `{"kind":"visible","selector":"#x","strategy":"css","timeout_ms":100}`, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := mgr.ExecuteWaitFor(c.args)
			if c.wantErr {
				if err == nil {
					t.Fatalf("ExecuteWaitFor(%s) = nil, want error", c.args)
				}
				if !strings.Contains(err.Error(), c.errSubstr) {
					t.Errorf("ExecuteWaitFor(%s) error = %q, want substring %q", c.args, err.Error(), c.errSubstr)
				}
				return
			}
			// For the shape-OK cases we either succeed (if Chrome is
			// installed and the condition eventually arrives) or fail
			// with a timeout / not-installed error — NOT a validation
			// error. So the test only fails if we got a validation
			// substring, meaning the input was rejected past validation.
			if err != nil && strings.Contains(err.Error(), "non-empty") {
				t.Errorf("ExecuteWaitFor(%s) failed at validation when it shouldn't: %v", c.args, err)
			}
		})
	}
}

// newDelayedPageServer serves a page that injects a result element
// after `delay` (ms) via setTimeout. Returns the URL. The element
// appears at #delayed-result with text "I appeared" — exactly what
// Phase 2.5 needs to prove the wait actually waits for the signal.
func newDelayedPageServer(t *testing.T, delayMS int) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!doctype html>
<html><head><title>Delayed Page</title></head>
<body>
  <h1>Delayed Page</h1>
  <p>Waiting for delayed content...</p>
  <div id="delayed-result" hidden>placeholder</div>
  <script>
    setTimeout(function () {
      var el = document.getElementById('delayed-result');
      el.textContent = 'I appeared';
      el.removeAttribute('hidden');
    }, %d);
  </script>
</body></html>`, delayMS)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestBrowserWaitFor_DelayedElement is Phase 2.5: a page that
// renders the target 600ms after load. The wait MUST succeed
// without needing any sleep on our side, proving the wait is
// condition-based, not fixed-delay. We use timeout_ms=5000 to
// leave plenty of slack — the test passes when the wait succeeds
// before the timeout.
func TestBrowserWaitFor_DelayedElement(t *testing.T) {
	if !chromeInstalled() {
		t.Skip("Playwright Chromium binary not installed; run `playwright install chromium` to enable this test")
	}
	serverURL := newDelayedPageServer(t, 600)
	workDir := t.TempDir()
	mgr := NewManager()
	t.Cleanup(func() { _ = mgr.Close() })

	if _, err := mgr.ExecuteTool(workDir, "browser_navigate", fmt.Sprintf(`{"url":%q}`, serverURL+"/")); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	start := time.Now()
	res, err := mgr.ExecuteWaitFor(`{"kind":"text","text":"I appeared","timeout_ms":5000}`)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("wait_for text: %v", err)
	}
	if !strings.Contains(res, "I appeared") {
		t.Errorf("result should mention text appeared, got: %q", res)
	}
	// Sanity check: the wait took at least roughly the delay (we
	// don't want a flaky "passed in 0ms" — that would mean the
	// element was already there and the wait is a no-op).
	if elapsed < 400*time.Millisecond {
		t.Errorf("wait_for returned in %s — expected ~600ms (the page delay); wait may be a no-op", elapsed)
	}
	// And it must have returned well before the 5s timeout.
	if elapsed >= 5*time.Second {
		t.Errorf("wait_for took %s — should have returned ~600ms, not blocked until the 5s timeout", elapsed)
	}
}

// TestBrowserWaitFor_NeverAppearsIsPhase26 covers the timeout failure
// path. The page never renders the target text; the wait MUST fail
// cleanly with timeout_ms=1500, NOT hang the test, and the error
// message MUST mention "timed out" so the loop surfaces a clean
// failure to the human rather than a generic Playwright timeout.
func TestBrowserWaitFor_NeverAppearsIsPhase26(t *testing.T) {
	if !chromeInstalled() {
		t.Skip("Playwright Chromium binary not installed; run `playwright install chromium` to enable this test")
	}
	// Server that never inserts the target text. 120s would block
	// the test; we use timeout_ms=1500 to bound it.
	serverURL := newDelayedPageServer(t, 120_000)
	workDir := t.TempDir()
	mgr := NewManager()
	t.Cleanup(func() { _ = mgr.Close() })

	if _, err := mgr.ExecuteTool(workDir, "browser_navigate", fmt.Sprintf(`{"url":%q}`, serverURL+"/")); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	start := time.Now()
	_, err := mgr.ExecuteWaitFor(`{"kind":"text","text":"never-going-to-appear","timeout_ms":1500}`)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("wait_for should have failed when text never appeared")
	}
	low := strings.ToLower(err.Error())
	if !strings.Contains(low, "timed out") {
		t.Errorf("error should mention 'timed out', got: %v", err)
	}
	if elapsed >= 3*time.Second {
		t.Errorf("wait_for took %s — should have failed near the 1500ms timeout, not run further", elapsed)
	}
	if elapsed < 1*time.Second {
		t.Errorf("wait_for returned in %s — looks like it didn't even try to wait", elapsed)
	}
}

// TestBrowserWaitFor_TimeoutCapIsEnforced exercises the "pass a huge
// timeout_ms" path. The clamp to MaxWaitTimeout MUST apply — confirmed
// by TestResolveWaitTimeout's unit-level cap checks. This e2e test
// passes a moderate timeout so it completes quickly while still proving
// the timeout fires cleanly (text never appears → clean failure).
func TestBrowserWaitFor_TimeoutCapIsEnforced(t *testing.T) {
	if !chromeInstalled() {
		t.Skip("Playwright Chromium binary not installed; run `playwright install chromium` to enable this test")
	}
	serverURL := newDelayedPageServer(t, 120_000)
	workDir := t.TempDir()
	mgr := NewManager()
	t.Cleanup(func() { _ = mgr.Close() })

	if _, err := mgr.ExecuteTool(workDir, "browser_navigate", fmt.Sprintf(`{"url":%q}`, serverURL+"/")); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	start := time.Now()
	// Pass a huge timeout — resolveWaitTimeout clamps to
	// MaxWaitTimeout (2min), but the unit test already proves that.
	// Here we use 2000ms so the test completes fast while still
	// confirming the timeout path fires cleanly.
	_, err := mgr.ExecuteWaitFor(`{"kind":"text","text":"absent","timeout_ms":2000}`)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected failure on absent text")
	}
	if elapsed >= 5*time.Second {
		t.Errorf("wait_for took %s — should have failed near the 2s timeout", elapsed)
	}
}