package browser

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestIsBrowserTool confirms the helper correctly classifies each
// browser_* tool and rejects non-browser tools. This is the gate the
// loop / TUI use to decide whether to intercept a tool call, so
// getting the set wrong silently would mean browser tools either get
// dropped (false negative) or escape into agent.ExecuteTool (false
// positive, which would surface as a "unknown tool" error).
func TestIsBrowserTool(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"navigate", "browser_navigate", true},
		{"click", "browser_click", true},
		{"type", "browser_type", true},
		{"get_text", "browser_get_text", true},
		{"screenshot", "browser_screenshot", true},
		{"wait_for", "browser_wait_for", true},
		{"write_file_rejected", "write_file", false},
		{"read_file_rejected", "read_file", false},
		{"run_command_rejected", "run_command", false},
		{"spawn_subagent_rejected", "spawn_subagent", false},
		{"empty_rejected", "", false},
		{"fake_rejected", "browser_fake", false},
		{"case_sensitive", "Browser_Navigate", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := IsBrowserTool(c.in)
			if got != c.want {
				t.Errorf("IsBrowserTool(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// TestValidateURL exercises the URL validation that gates
// browser_navigate. We accept only http and https with a non-empty
// host. file://, javascript:, and data: are dangerous enough that we
// refuse them up front rather than relying on Playwright to block
// them.
func TestValidateURL(t *testing.T) {
	cases := []struct {
		name      string
		url       string
		wantErr   bool
		errSubstr string
	}{
		{"https_ok", "https://api.example.com/docs", false, ""},
		{"http_ok", "http://localhost:8080/health", false, ""},
		{"empty", "", true, "missing or empty"},
		{"whitespace", "   ", true, "missing or empty"},
		{"file_scheme", "file:///etc/passwd", true, "scheme"},
		{"javascript_scheme", "javascript:alert(1)", true, "scheme"},
		{"data_scheme", "data:text/plain,hi", true, "scheme"},
		{"no_scheme", "example.com/docs", true, ""}, // url.Parse may not error; we should still fail
		{"https_with_path", "https://example.com/a/b/c?d=1", false, ""},
		{"https_with_userinfo", "https://user:pass@example.com/", false, ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateURL(c.url)
			if c.wantErr {
				if err == nil {
					t.Fatalf("validateURL(%q) = nil error, want error", c.url)
				}
				if c.errSubstr != "" && !strings.Contains(err.Error(), c.errSubstr) {
					t.Errorf("validateURL(%q) error = %q, want substring %q", c.url, err.Error(), c.errSubstr)
				}
			} else {
				if err != nil {
					t.Errorf("validateURL(%q) = %v, want nil", c.url, err)
				}
			}
		})
	}
}

// TestValidateSelector exercises the selector sanity check. Empty
// selectors are a model mistake, not a legitimate intent, and very
// long selectors are almost certainly garbage that would just hang
// Playwright waiting for a timeout.
func TestValidateSelector(t *testing.T) {
	cases := []struct {
		name     string
		toolName string
		sel      string
		wantErr  bool
	}{
		{"ok_simple", "browser_click", "button.submit", false},
		{"ok_text", "browser_click", "text=Sign in", false},
		{"empty", "browser_click", "", true},
		{"whitespace", "browser_click", "   ", true},
		{"way_too_long", "browser_click", strings.Repeat("a", 4097), true},
		{"just_under_cap", "browser_click", strings.Repeat("a", 4096), false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateSelector(c.toolName, c.sel)
			if c.wantErr && err == nil {
				t.Errorf("validateSelector(%q, %q) = nil, want error", c.toolName, c.sel)
			}
			if !c.wantErr && err != nil {
				t.Errorf("validateSelector(%q, %q) = %v, want nil", c.toolName, c.sel, err)
			}
		})
	}
}

// TestTruncateResult confirms the truncation helper doesn't lose
// data below the cap and adds a marker above it.
func TestTruncateResult(t *testing.T) {
	t.Run("under_cap_unchanged", func(t *testing.T) {
		s := "hello world"
		got := truncateResult(s, 100)
		if got != s {
			t.Errorf("truncateResult(%q, 100) = %q, want unchanged", s, got)
		}
	})

	t.Run("over_cap_truncated_with_marker", func(t *testing.T) {
		s := strings.Repeat("x", 200)
		got := truncateResult(s, 50)
		if !strings.HasPrefix(got, strings.Repeat("x", 50)) {
			t.Errorf("truncateResult: expected first 50 bytes to be %q, got prefix %q", strings.Repeat("x", 50), got[:50])
		}
		if !strings.Contains(got, "truncated") {
			t.Errorf("truncateResult: expected marker in output, got %q", got)
		}
	})

	t.Run("exactly_at_cap_unchanged", func(t *testing.T) {
		s := strings.Repeat("x", 50)
		got := truncateResult(s, 50)
		if got != s {
			t.Errorf("truncateResult: at-cap input should be unchanged, got %q", got)
		}
	})
}

// TestResolveScreenshotPath exercises the safeRelPath-style
// validation for browser_screenshot's file path argument.
func TestResolveScreenshotPath(t *testing.T) {
	workDir := t.TempDir()

	t.Run("relative_ok", func(t *testing.T) {
		got, err := resolveScreenshotPath(workDir, "screenshots/after.png")
		if err != nil {
			t.Fatalf("resolveScreenshotPath: %v", err)
		}
		// The exact path is platform-dependent (filepath.Join), so
		// just confirm it ends with the relative suffix and doesn't
		// escape. We accept both forward-slash (input) and OS-native
		// (output) separators.
		sep := string(filepath.Separator)
		if !strings.HasSuffix(got, "screenshots"+sep+"after.png") &&
			!strings.HasSuffix(got, "screenshots/after.png") {
			t.Errorf("resolveScreenshotPath: unexpected result %q", got)
		}
	})

	t.Run("absolute_windows_rejected", func(t *testing.T) {
		_, err := resolveScreenshotPath(workDir, `C:\Windows\System32\evil.png`)
		if err == nil {
			t.Fatalf("resolveScreenshotPath: expected error for absolute path")
		}
	})

	t.Run("absolute_unix_rejected", func(t *testing.T) {
		_, err := resolveScreenshotPath(workDir, "/etc/passwd")
		if err == nil {
			t.Fatalf("resolveScreenshotPath: expected error for absolute path")
		}
	})

	t.Run("dotdot_rejected", func(t *testing.T) {
		_, err := resolveScreenshotPath(workDir, "../outside.png")
		if err == nil {
			t.Fatalf("resolveScreenshotPath: expected error for .. traversal")
		}
	})

	t.Run("embedded_dotdot_rejected", func(t *testing.T) {
		_, err := resolveScreenshotPath(workDir, "screenshots/../../outside.png")
		if err == nil {
			t.Fatalf("resolveScreenshotPath: expected error for embedded .. traversal")
		}
	})
}

// TestUnmarshalArgsEmpty confirms the empty-arg case is rejected
// with a tool-name-prefixed error. The model occasionally emits an
// empty arguments string, and we want a clean diagnostic rather
// than a Go json.Unmarshal "unexpected end of JSON input" error.
func TestUnmarshalArgsEmpty(t *testing.T) {
	var dst NavigateArgs
	err := unmarshalArgs("browser_navigate", "", &dst)
	if err == nil {
		t.Fatal("unmarshalArgs(\"\") = nil, want error")
	}
	if !strings.Contains(err.Error(), "browser_navigate") {
		t.Errorf("error should mention tool name, got: %v", err)
	}
}

// TestUnmarshalArgsMalformedJSON confirms a malformed JSON string
// surfaces a tool-name-prefixed parse error rather than a raw
// json.Unmarshal error.
func TestUnmarshalArgsMalformedJSON(t *testing.T) {
	var dst NavigateArgs
	err := unmarshalArgs("browser_navigate", "{not json", &dst)
	if err == nil {
		t.Fatal("unmarshalArgs(\"{not json\") = nil, want error")
	}
	if !strings.Contains(err.Error(), "browser_navigate") {
		t.Errorf("error should mention tool name, got: %v", err)
	}
}
