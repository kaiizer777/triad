package browser

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIsChromiumInstalled_OverridePath exercises the
// PLAYWRIGHT_BROWSERS_PATH override, which is the documented
// escape hatch for users who want the binary somewhere other than
// the default location. The override is checked first, so an
// existing default install doesn't make this test pass for the
// wrong reason.
func TestIsChromiumInstalled_OverridePath(t *testing.T) {
	dir := t.TempDir()

	// Empty override dir — no chromium-* subdir — must return
	// false. (If the real default install on this machine is
	// also missing, the test still passes for the right reason.)
	t.Setenv("PLAYWRIGHT_BROWSERS_PATH", dir)
	if IsChromiumInstalled() {
		t.Fatal("IsChromiumInstalled should return false for an empty override dir")
	}

	// Now drop a chromium-* subdir and confirm it flips to true.
	if err := os.Mkdir(filepath.Join(dir, "chromium-9999"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if !IsChromiumInstalled() {
		t.Fatal("IsChromiumInstalled should return true when override dir contains chromium-*")
	}

	// Other names that start with "chromium" but aren't versioned
	// (e.g. a user-created "chromium" dir) must NOT count — the
	// binary the Playwright driver actually looks for lives in
	// a versioned subdir. We document this in the helper.
	if err := os.Mkdir(filepath.Join(dir, "chromium"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Set up a fresh empty override dir so only "chromium" is
	// present, and confirm it doesn't trick us.
	dir2 := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir2, "chromium"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Setenv("PLAYWRIGHT_BROWSERS_PATH", dir2)
	if IsChromiumInstalled() {
		t.Fatal("IsChromiumInstalled should not match a bare 'chromium' dir; only versioned chromium-*")
	}
}
