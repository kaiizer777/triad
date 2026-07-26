package browser

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// FindChrome returns the path to the user's real Chrome executable.
// Search order:
//  1. CHROME_PATH environment variable (explicit override, always wins)
//  2. Well-known Windows installation paths (Program Files, LocalAppData)
//  3. Well-known Linux paths (/usr/bin/google-chrome, etc.)
//  4. macOS application bundle
//
// Returns an error with a clear actionable message if Chrome is not found.
func FindChrome() (string, error) {
	// 1. Explicit env override — always wins regardless of OS.
	if p := os.Getenv("CHROME_PATH"); p != "" {
		if err := checkExecutable(p); err != nil {
			return "", fmt.Errorf("CHROME_PATH is set to %q but: %w", p, err)
		}
		return p, nil
	}

	candidates := chromeCandidates()
	for _, p := range candidates {
		if checkExecutable(p) == nil {
			return p, nil
		}
	}

	return "", fmt.Errorf(
		"real Chrome not found in any known location; set CHROME_PATH environment variable or install Chrome",
	)
}

// chromeCandidates returns OS-specific candidate paths in priority order.
func chromeCandidates() []string {
	switch runtime.GOOS {
	case "windows":
		return windowsChromePaths()
	case "darwin":
		return []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		}
	default: // Linux and other Unix-likes
		return []string{
			"/usr/bin/google-chrome",
			"/usr/bin/google-chrome-stable",
			"/usr/bin/chromium-browser",
			"/usr/bin/chromium",
			"/snap/bin/chromium",
		}
	}
}

// windowsChromePaths returns candidate Chrome paths on Windows, checking
// standard install locations and the user-local AppData install.
func windowsChromePaths() []string {
	var paths []string

	// Standard machine-wide install locations.
	for _, base := range []string{
		os.Getenv("PROGRAMFILES"),
		os.Getenv("PROGRAMFILES(X86)"),
		os.Getenv("PROGRAMW6432"),
	} {
		if base != "" {
			paths = append(paths,
				filepath.Join(base, "Google", "Chrome", "Application", "chrome.exe"),
			)
		}
	}

	// User-local install (common for managed/restricted machines).
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		paths = append(paths,
			filepath.Join(local, "Google", "Chrome", "Application", "chrome.exe"),
		)
	}

	// Edge as a fallback — also Chromium-based and supports CDP.
	for _, base := range []string{
		os.Getenv("PROGRAMFILES"),
		os.Getenv("PROGRAMFILES(X86)"),
	} {
		if base != "" {
			paths = append(paths,
				filepath.Join(base, "Microsoft", "Edge", "Application", "msedge.exe"),
			)
		}
	}

	return paths
}

// checkExecutable verifies that a path points to a regular, executable file.
// Uses exec.LookPath for paths that are just a binary name (no slashes),
// and os.Stat for absolute paths.
func checkExecutable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("not found: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("%q is a directory, not an executable", path)
	}
	return nil
}

// ChromePath returns the located Chrome path, or empty string if not found.
// This is the non-error variant for callers that just want a best-effort result
// (e.g. startup notices).
func ChromePath() string {
	p, _ := FindChrome()
	return p
}

// IsRealChromeAvailable reports whether a real Chrome executable can be found.
// Cheap — just a filesystem stat, no process launch.
func IsRealChromeAvailable() bool {
	_, err := FindChrome()
	return err == nil
}

// lookPath wraps exec.LookPath for testability.
var lookPath = exec.LookPath

// chromeUserDataDir returns the path to the user's default Chrome profile
// directory. Using an explicit --user-data-dir prevents Chrome from delegating
// a new launch to an already-running instance (which would cause the process
// to exit immediately without opening a CDP debug port).
//
// Returns "" if the directory cannot be determined — in that case LaunchRealChrome
// omits the flag and Chrome uses its own default, which may still work.
func chromeUserDataDir() string {
	switch runtime.GOOS {
	case "windows":
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			return filepath.Join(local, "Google", "Chrome", "User Data")
		}
	case "darwin":
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, "Library", "Application Support", "Google", "Chrome")
		}
	default: // Linux
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, ".config", "google-chrome")
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Phase 2 — CDP Launch & Connect
// ---------------------------------------------------------------------------

// KillExistingChrome terminates any running Chrome/Chromium processes and
// cleans up the profile lock files left behind by the force-kill, so that
// Chrome can restart cleanly with our --remote-debugging-port flags.
//
// Three-step process:
//  1. Kill all chrome.exe processes (taskkill /F on Windows)
//  2. Wait 1.5s for the OS to release all file handles
//  3. Delete SingletonLock / SingletonSocket from the user data dir so
//     Chrome doesn't show "Chrome didn't shut down correctly" on restart
//
// Errors are silently ignored — if Chrome isn't running, that's fine too.
func KillExistingChrome() {
	// Step 1: kill the processes.
	var killCmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		killCmd = exec.Command("taskkill", "/F", "/IM", "chrome.exe")
	case "darwin":
		killCmd = exec.Command("pkill", "-f", "Google Chrome")
	default:
		killCmd = exec.Command("pkill", "-f", "chromium")
	}
	_ = killCmd.Run()

	// Step 2: wait for OS to fully release file handles.
	time.Sleep(1500 * time.Millisecond)

	// Step 3: remove Chrome's singleton lock files so it doesn't think
	// another instance is still running or show crash-recovery dialogs.
	cleanChromeLockFiles()
}

// cleanChromeLockFiles removes the lock files Chrome leaves in the user
// data directory after a force-kill. Without this, Chrome may show
// "Chrome didn't shut down correctly" and delay (or refuse) CDP startup.
func cleanChromeLockFiles() {
	userDataDir := chromeUserDataDir()
	if userDataDir == "" {
		return
	}
	lockFiles := []string{
		filepath.Join(userDataDir, "SingletonLock"),
		filepath.Join(userDataDir, "SingletonSocket"),
		filepath.Join(userDataDir, "SingletonCookie"),
		filepath.Join(userDataDir, "Default", "SingletonLock"),
	}
	for _, f := range lockFiles {
		_ = os.Remove(f)
	}
}

// CDPDefaultPort is the well-known Chrome remote debugging port.
const CDPDefaultPort = 9222

// CDPReadyTimeout is how long we wait for Chrome's debug server to start
// accepting connections before giving up. 15s to handle cold starts with
// real user profiles (first-launch Chrome with a real profile takes longer).
const CDPReadyTimeout = 15 * time.Second

// LaunchRealChrome starts the user's real Chrome with remote-debugging enabled
// on the given port. It returns the running *exec.Cmd so the caller can kill
// the process when done (or leave it running if the user opened Chrome
// themselves). The function does NOT wait for the CDP endpoint to be ready —
// call WaitForCDP after this.
//
// Flags used:
//
//	--remote-debugging-port       — enables the CDP debug server
//	--no-first-run                — skip the "Welcome to Chrome" setup page
//	--no-default-browser-check    — don't nag about default browser
//	--profile-directory=Default   — use Default profile, skips profile picker
//	--disable-features=ProfilePicker — disable the profile picker popup
//	--user-data-dir               — explicit profile dir; prevents Chrome from
//	                                delegating to an already-running instance
func LaunchRealChrome(port int) (*exec.Cmd, error) {
	chromePath, err := FindChrome()
	if err != nil {
		return nil, fmt.Errorf("LaunchRealChrome: %w", err)
	}

	userDataDir := chromeUserDataDir()

	args := []string{
		fmt.Sprintf("--remote-debugging-port=%d", port),
		"--no-first-run",
		"--no-default-browser-check",
		"--profile-directory=Default",
		"--disable-features=ProfilePicker",
		// Suppress crash-recovery UI after a force-kill, so Chrome starts
		// the CDP server immediately rather than waiting for user interaction.
		"--disable-session-crashed-bubble",
		"--disable-infobars",
		"--no-restore-state",
	}
	if userDataDir != "" {
		args = append(args, fmt.Sprintf("--user-data-dir=%s", userDataDir))
	}

	cmd := exec.Command(chromePath, args...)
	// Start detached — we don't own its stdin/stdout.
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("LaunchRealChrome: failed to start Chrome process: %w", err)
	}
	return cmd, nil
}

// LaunchRealChromeWithDataDir is like LaunchRealChrome but uses an explicit
// user data directory instead of the default. This is useful for:
//   - Tests: pass a t.TempDir() to avoid locking the user's real profile.
//   - Isolation: run Triad-controlled Chrome alongside the user's normal Chrome.
func LaunchRealChromeWithDataDir(port int, userDataDir string) (*exec.Cmd, error) {
	chromePath, err := FindChrome()
	if err != nil {
		return nil, fmt.Errorf("LaunchRealChromeWithDataDir: %w", err)
	}

	args := []string{
		fmt.Sprintf("--remote-debugging-port=%d", port),
		"--no-first-run",
		"--no-default-browser-check",
		fmt.Sprintf("--user-data-dir=%s", userDataDir),
	}

	cmd := exec.Command(chromePath, args...)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("LaunchRealChromeWithDataDir: failed to start Chrome: %w", err)
	}
	return cmd, nil
}

// WaitForCDP polls the CDP HTTP endpoint at http://localhost:<port>/json/version
// until it responds with 200 OK or the timeout elapses. This is necessary
// because Chrome takes a moment to start its debug server after the process
// starts — connecting too early results in a "connection refused" error.
//
// Returns nil when ready, error if the timeout elapsed without a response.
func WaitForCDP(port int, timeout time.Duration) error {
	url := fmt.Sprintf("http://localhost:%d/json/version", port)
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 500 * time.Millisecond}

	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}

	return fmt.Errorf(
		"WaitForCDP: Chrome CDP endpoint not ready after %s on port %d; "+
			"Chrome may have failed to start or the port is blocked",
		timeout, port,
	)
}

// IsCDPRunning does a single quick probe to check whether a Chrome instance
// is already listening on the given port. Useful to avoid double-launching.
func IsCDPRunning(port int) bool {
	url := fmt.Sprintf("http://localhost:%d/json/version", port)
	client := &http.Client{Timeout: 300 * time.Millisecond}
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
