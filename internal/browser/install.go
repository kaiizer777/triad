package browser

import (
	"os"
	"path/filepath"
	"strings"
)

// IsChromiumInstalled reports whether the Playwright Chromium binary
// appears to be present on disk. It's a best-effort filesystem
// probe, not a "can the browser actually launch" test — that's
// only knowable by calling Launch, which we deliberately do not
// do at startup (launching Chromium just to check would burn
// ~200ms and a process slot on every Triad start, even for
// sessions that never use browser_* tools).
//
// The check looks at the well-known install root:
//   - $PLAYWRIGHT_BROWSERS_PATH if set (documented Playwright override)
//   - %LOCALAPPDATA%\ms-playwright on Windows
//   - ~/.cache/ms-playwright on Linux/macOS
//
// It returns true if any subdirectory starting with "chromium-" is
// present. We don't pin the exact build number because the
// trailing digits shift between Playwright versions, and a user
// who installed Chromium once has it for the life of that
// install.
//
// The same logic lives (duplicated) in two test files so the
// skip-when-missing path is self-contained. Lifting it here
// removes the duplication and gives main.go a real exported
// helper to call for the startup notice (rough edge #8 in
// the final project summary).
func IsChromiumInstalled() bool {
	dir := os.Getenv("PLAYWRIGHT_BROWSERS_PATH")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return false
		}
		candidates := []string{
			filepath.Join(home, "AppData", "Local", "ms-playwright"),
			filepath.Join(home, ".cache", "ms-playwright"),
		}
		for _, c := range candidates {
			if st, statErr := os.Stat(c); statErr == nil && st.IsDir() {
				dir = c
				break
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
		if strings.HasPrefix(e.Name(), "chromium-") {
			return true
		}
	}
	return false
}
