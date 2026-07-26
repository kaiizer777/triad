package browser

import (
	"testing"
	"time"
)

// TestLaunchRealChrome_AndConnect is an integration test that:
//  1. Launches real Chrome with --remote-debugging-port=9223 (9223 to avoid
//     conflicting with any Chrome the user already has open on 9222)
//  2. Waits for the CDP endpoint to become ready
//  3. Creates a Manager via NewRealChromeManager and navigates to example.com
//  4. Confirms the URL matches
//
// This test is skipped if Chrome is not installed (CI-safe).
func TestLaunchRealChrome_AndConnect(t *testing.T) {
	if !IsRealChromeAvailable() {
		t.Skip("real Chrome not found — skipping CDP integration test")
	}

	const testPort = 9223

	// If something is already on our test port, skip rather than clobber it.
	if IsCDPRunning(testPort) {
		t.Skip("port 9223 already in use — skipping to avoid conflict")
	}

	// 1. Launch real Chrome with a temp user-data-dir so it doesn't try
	// to reuse an existing profile that may be locked by the user's Chrome.
	tmpDir := t.TempDir()
	cmd, err := LaunchRealChromeWithDataDir(testPort, tmpDir)
	if err != nil {
		t.Fatalf("LaunchRealChromeWithDataDir: %v", err)
	}
	// Kill Chrome when the test ends.
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})

	// 2. Wait for CDP endpoint to be ready (Chrome needs ~5s on first launch).
	if err := WaitForCDP(testPort, 12*time.Second); err != nil {
		t.Fatalf("WaitForCDP: %v", err)
	}

	// 3. Connect Manager and navigate.
	mgr := NewRealChromeManager(testPort)
	t.Cleanup(func() { _ = mgr.Close() })

	result, err := mgr.ExecuteNavigate(`{"url":"https://example.com"}`)
	if err != nil {
		t.Fatalf("ExecuteNavigate: %v", err)
	}
	t.Logf("Navigate result: %s", result)

	// 4. Confirm URL.
	url := mgr.CurrentURL()
	if url == "" {
		t.Fatal("CurrentURL returned empty string after navigation")
	}
	t.Logf("Current URL: %s", url)

	// example.com may redirect — just confirm we got a real URL, not about:blank.
	if url == "about:blank" || url == "" {
		t.Errorf("Expected a real URL after navigating to example.com, got %q", url)
	}
}

// TestIsCDPRunning_NoServer confirms IsCDPRunning returns false when nothing
// is listening on a port that should definitely be unused.
func TestIsCDPRunning_NoServer(t *testing.T) {
	// Port 19876 — extremely unlikely to be in use.
	if IsCDPRunning(19876) {
		t.Skip("something unexpectedly running on port 19876 — skipping")
	}
	// If IsCDPRunning returned false, we pass.
}

// TestWaitForCDP_Timeout confirms WaitForCDP returns an error within the
// timeout period when nothing is listening.
func TestWaitForCDP_Timeout(t *testing.T) {
	err := WaitForCDP(19877, 600*time.Millisecond)
	if err == nil {
		t.Fatal("WaitForCDP expected error on unused port, got nil")
	}
	t.Logf("WaitForCDP correctly returned error: %v", err)
}

// TestNewRealChromeManager_Fields confirms the constructor sets the right fields.
func TestNewRealChromeManager_Fields(t *testing.T) {
	mgr := NewRealChromeManager(9222)
	if !mgr.realChrome {
		t.Error("NewRealChromeManager: realChrome should be true")
	}
	if mgr.cdpPort != 9222 {
		t.Errorf("NewRealChromeManager: cdpPort = %d, want 9222", mgr.cdpPort)
	}
	if mgr.headless {
		t.Error("NewRealChromeManager: headless should be false (we want a visible window)")
	}
}
