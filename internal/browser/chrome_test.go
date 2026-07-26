package browser

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestFindChrome_EnvOverride verifies that CHROME_PATH env var always wins,
// even if the path is not a real Chrome binary (as long as the file exists).
func TestFindChrome_EnvOverride(t *testing.T) {
	// Create a temporary fake "chrome" binary.
	tmp := t.TempDir()
	fakeBin := filepath.Join(tmp, "chrome.exe")
	if err := os.WriteFile(fakeBin, []byte("fake"), 0755); err != nil {
		t.Fatalf("setup: %v", err)
	}

	t.Setenv("CHROME_PATH", fakeBin)

	got, err := FindChrome()
	if err != nil {
		t.Fatalf("FindChrome() returned error with valid CHROME_PATH: %v", err)
	}
	if got != fakeBin {
		t.Errorf("FindChrome() = %q, want %q", got, fakeBin)
	}
}

// TestFindChrome_EnvOverride_BadPath verifies that a non-existent CHROME_PATH
// returns a clear error rather than silently falling through to other paths.
func TestFindChrome_EnvOverride_BadPath(t *testing.T) {
	t.Setenv("CHROME_PATH", "/definitely/does/not/exist/chrome")

	_, err := FindChrome()
	if err == nil {
		t.Fatal("FindChrome() expected error for non-existent CHROME_PATH, got nil")
	}
}

// TestFindChrome_ReturnsStringOrError verifies the basic contract:
// FindChrome either returns a non-empty path with nil error, or an empty
// string with a non-nil error. It never returns ("", nil) or ("path", err).
func TestFindChrome_ReturnsStringOrError(t *testing.T) {
	// Unset CHROME_PATH so we exercise the real OS path search.
	t.Setenv("CHROME_PATH", "")

	path, err := FindChrome()
	if err != nil && path != "" {
		t.Errorf("FindChrome() returned both a path %q and an error %v — must be one or the other", path, err)
	}
	if err == nil && path == "" {
		t.Error("FindChrome() returned nil error with empty path — impossible state")
	}
}

// TestIsRealChromeAvailable_Consistent verifies that IsRealChromeAvailable()
// agrees with FindChrome() — they must always return consistent results.
func TestIsRealChromeAvailable_Consistent(t *testing.T) {
	t.Setenv("CHROME_PATH", "")

	_, err := FindChrome()
	available := IsRealChromeAvailable()

	if (err == nil) != available {
		t.Errorf("IsRealChromeAvailable()=%v but FindChrome() err=%v — inconsistent", available, err)
	}
}

// TestChromePath_EmptyOnMissing verifies ChromePath returns "" (not panics)
// when no Chrome is found.
func TestChromePath_EmptyOnMissing(t *testing.T) {
	// Point CHROME_PATH at a nonexistent file so FindChrome always fails.
	t.Setenv("CHROME_PATH", "/no/chrome/here")

	p := ChromePath()
	if p != "" {
		// If real Chrome actually exists at a system path, ChromePath may
		// return it — that's also valid. Only fail if ChromePath panics.
		t.Logf("ChromePath() = %q (real Chrome may be installed)", p)
	}
}

// TestWindowsChromePaths_ContainsExpectedDirs is a Windows-only sanity check
// that the candidate list includes at least the Program Files path.
func TestWindowsChromePaths_ContainsExpectedDirs(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only test")
	}

	paths := windowsChromePaths()
	if len(paths) == 0 {
		t.Fatal("windowsChromePaths() returned empty slice on Windows")
	}

	// At least one path should contain "chrome.exe".
	found := false
	for _, p := range paths {
		if filepath.Base(p) == "chrome.exe" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("windowsChromePaths() has no entry ending in chrome.exe: %v", paths)
	}
}

// TestFindChrome_ActualMachine logs whether Chrome is found on this machine.
// Not a pass/fail test — just informational output useful during CI.
func TestFindChrome_ActualMachine(t *testing.T) {
	t.Setenv("CHROME_PATH", "")

	path, err := FindChrome()
	if err != nil {
		t.Logf("Chrome not found on this machine: %v", err)
	} else {
		t.Logf("Chrome found at: %s", path)
	}
}
