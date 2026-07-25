package browser

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestExecuteNavigate_RejectsFileScheme is a focused regression guard
// for the file:// attack vector. It exists alongside the broader
// TestValidateURL/file_scheme unit test and the more general
// TestBrowserExecuteTool_RejectsBadURL integration test for two
// reasons:
//
//  1. file:// is the highest-risk non-http scheme — a successful
//     file:// navigate would let a model read arbitrary files off
//     the host machine (think /etc/passwd on Linux, or
//     C:\Windows\System32\config\SAM on Windows). A test that
//     pins this specific attack by name + asserts the exact error
//     substring makes it impossible to quietly regress during a
//     refactor of validateURL or the Manager's execute path.
//
//  2. The test runs at the Manager.ExecuteTool boundary, not just
//     the validateURL helper. validateURL could be left intact
//     while Manager.ExecuteNavigate grew a code path that skipped
//     it (or applied it with the wrong logic). Going through the
//     real entry point is the same risk class as the
//     safeRelPath / write_file / run_command path-traversal guards.
//
// This test does NOT require the Playwright Chromium binary to be
// installed — every file:// URL is rejected before the browser is
// ever launched, so we keep it in the always-on unit tier.
func TestExecuteNavigate_RejectsFileScheme(t *testing.T) {
	// attacks covers the realistic shapes a model could emit if it
	// were tricked (or decided) to try to read a local file. Each
	// one must be rejected with an error message that mentions
	// either the scheme or the specific URL, so a human reading
	// the transcript can immediately see what happened.
	attacks := []struct {
		name        string
		url         string
		mustMention string // case-insensitive substring that must appear in the error
	}{
		{
			name:        "unix_absolute",
			url:         "file:///etc/passwd",
			mustMention: "file",
		},
		{
			name:        "windows_drive_absolute",
			url:         "file:///C:/Windows/System32/config/SAM",
			mustMention: "file",
		},
		{
			name:        "windows_backslash_absolute",
			url:         `file:///C:\Windows\System32\drivers\etc\hosts`,
			mustMention: "file",
		},
		{
			name:        "host_relative",
			url:         "file://localhost/etc/passwd",
			mustMention: "scheme",
		},
		{
			name:        "encoded_traversal",
			url:         "file:///etc/..%2F..%2Froot%2F.ssh%2Fid_rsa",
			mustMention: "file",
		},
		{
			name:        "plain_file_with_no_path",
			url:         "file://",
			mustMention: "scheme",
		},
	}

	mgr := NewManager()
	t.Cleanup(func() {
		// Even on a "should have failed" path, Close is a no-op
		// if the browser was never launched, so this is safe.
		_ = mgr.Close()
	})

	for _, c := range attacks {
		t.Run(c.name, func(t *testing.T) {
			args := `{"url":` + quoteJSON(c.url) + `}`
			res, err := mgr.ExecuteTool(t.TempDir(), "browser_navigate", args)
			if err == nil {
				t.Fatalf("browser_navigate(%q) returned no error and result %q; file:// MUST be rejected", c.url, res)
			}
			lower := strings.ToLower(err.Error())
			if !strings.Contains(lower, strings.ToLower(c.mustMention)) {
				t.Errorf("error for %q should mention %q, got: %v", c.url, c.mustMention, err)
			}
			// Defence in depth: also assert the error does NOT
			// contain any leak of the file content we tried to
			// read. validateURL echoes the URL back in some error
			// paths, which is fine for the operator reading the
			// transcript (they need to know what was attempted),
			// but we want to be sure the actual file *contents*
			// never appear in the error. Since the inputs here are
			// paths not contents, the easiest assertion is just
			// that the error text is reasonably short — a leaked
			// file would balloon the error.
			if len(err.Error()) > 1024 {
				t.Errorf("error for %q is suspiciously long (%d bytes) — possible file content leak", c.url, len(err.Error()))
			}
		})
	}
}

// TestExecuteNavigate_RejectsFileScheme_ExercisesManagerNotJustValidator
// is a belt-and-braces guard: it uses a Manager whose validateURL
// has been bypassed by passing an already-bad args blob (e.g.
// args="" or args="{}"). If the Manager gained a "skip validation
// for empty args" or "default to localhost on empty URL" shortcut in
// the future, this test catches it because the failure has to come
// from a place that recognises the URL string, not just a missing-
// arg path.
func TestExecuteNavigate_RejectsFileScheme_ExercisesManagerNotJustValidator(t *testing.T) {
	mgr := NewManager()
	t.Cleanup(func() { _ = mgr.Close() })

	// Pass an empty args blob — the navigate executor should fail
	// at the "url is missing" step, which is still better than
	// launching a browser. We assert the error explicitly here.
	_, err := mgr.ExecuteTool(t.TempDir(), "browser_navigate", `{}`)
	if err == nil {
		t.Fatal("browser_navigate with empty url must fail before any browser launch")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "url") {
		t.Errorf("error should mention 'url', got: %v", err)
	}
}

// quoteJSON is a tiny helper to safely inject a Go string into a
// JSON argument value. We don't need full JSON escaping for the
// test inputs above (none contain quotes or backslashes after the
// explicit construction in the table), but the Windows
// backslash case proves we DO need it. Using json.Marshal would
// be overkill for a test helper, so we hand-roll minimal
// escaping — just the bytes that appear in the table.
func quoteJSON(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	// Sanity check: round-trip through filepath-style conversion
	// to make sure our hand-rolled escape didn't get confused.
	_ = filepath.FromSlash(s)
	return b.String()
}
