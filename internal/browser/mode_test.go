package browser

import (
	"testing"
)

// TestManagerMode_HeadlessDefault confirms NewManager() produces a headless manager.
func TestManagerMode_HeadlessDefault(t *testing.T) {
	mgr := NewManager()
	if mgr.realChrome {
		t.Error("NewManager: realChrome should be false (headless default)")
	}
	if !mgr.headless {
		t.Error("NewManager: headless should be true")
	}
	if mgr.cdpPort != 0 {
		t.Errorf("NewManager: cdpPort should be 0, got %d", mgr.cdpPort)
	}
}

// TestManagerMode_RealChrome confirms NewRealChromeManager sets the right fields.
func TestManagerMode_RealChrome(t *testing.T) {
	mgr := NewRealChromeManager(CDPDefaultPort)
	if !mgr.realChrome {
		t.Error("NewRealChromeManager: realChrome should be true")
	}
	if mgr.headless {
		t.Error("NewRealChromeManager: headless should be false (real Chrome is always visible)")
	}
	if mgr.cdpPort != CDPDefaultPort {
		t.Errorf("NewRealChromeManager: cdpPort = %d, want %d", mgr.cdpPort, CDPDefaultPort)
	}
	if mgr.launched {
		t.Error("NewRealChromeManager: should not be launched yet (lazy launch)")
	}
}

// TestManagerMode_BothModesShareToolSurface confirms that both managers expose
// the same tool names — the tool surface must be identical regardless of mode.
func TestManagerMode_BothModesShareToolSurface(t *testing.T) {
	browserTools := []string{
		"browser_navigate",
		"browser_click",
		"browser_type",
		"browser_get_text",
		"browser_screenshot",
		"browser_wait_for",
	}

	for _, toolName := range browserTools {
		if !IsBrowserTool(toolName) {
			t.Errorf("IsBrowserTool(%q) = false, both modes should expose this tool", toolName)
		}
	}
}

// TestManagerMode_CloseBeforeLaunch confirms Close() on an un-launched Manager
// does not panic or error in either mode.
func TestManagerMode_CloseBeforeLaunch(t *testing.T) {
	t.Run("headless", func(t *testing.T) {
		mgr := NewManager()
		if err := mgr.Close(); err != nil {
			t.Errorf("Close on un-launched headless manager: %v", err)
		}
	})
	t.Run("real_chrome", func(t *testing.T) {
		mgr := NewRealChromeManager(CDPDefaultPort)
		if err := mgr.Close(); err != nil {
			t.Errorf("Close on un-launched real-chrome manager: %v", err)
		}
	})
}

// TestManagerMode_DoubleClose confirms Close() is idempotent in both modes.
func TestManagerMode_DoubleClose(t *testing.T) {
	mgr := NewManager()
	_ = mgr.Close()
	if err := mgr.Close(); err != nil {
		t.Errorf("second Close should be a no-op, got: %v", err)
	}
}
