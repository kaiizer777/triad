// Browser-tool integration test (docs/work2.md §4.2.4).
//
// This test exercises the FULL propose→Reviewer→execute approval
// cycle for browser_* tools end-to-end. Coder proposes a
// browser_navigate, Reviewer approves, the browser launches and
// loads the test page. Coder then proposes browser_get_text,
// Reviewer approves, the browser reads text back. Coder then
// proposes task_complete and the session ends cleanly.
//
// The test requires the Playwright Chromium binary to be installed;
// if it isn't, it's skipped (rather than failed) — the install is
// a one-time `playwright install chromium` step that doesn't belong
// in a Go test that needs to run on every commit.
package loop_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kaiizer777/triad/internal/agent"
	"github.com/kaiizer777/triad/internal/browser"
	"github.com/kaiizer777/triad/internal/loop"
	"github.com/kaiizer777/triad/internal/transcript"
)

// chromeInstalled reports whether the Playwright Chromium binary is
// present on disk. The exact path is OS-specific, so we just look
// for the well-known install root.
func chromeInstalledLoop() bool {
	dir := os.Getenv("PLAYWRIGHT_BROWSERS_PATH")
	if dir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			candidates := []string{
				filepath.Join(home, "AppData", "Local", "ms-playwright"),
				filepath.Join(home, ".cache", "ms-playwright"),
			}
			for _, c := range candidates {
				if st, err := os.Stat(c); err == nil && st.IsDir() {
					dir = c
					break
				}
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

// TestBrowserTool_FullApprovalLoop runs the propose→Reviewer→execute
// cycle for browser_navigate and browser_get_text. This is the test
// 4.2.4 in work2.md calls for: "give Coder a task that requires
// checking live documentation or a running local dev server ...,
// confirm the browser tools work through the full propose→approve→
// execute cycle."
func TestBrowserTool_FullApprovalLoop(t *testing.T) {
	if !chromeInstalledLoop() {
		t.Skip("Playwright Chromium binary not installed; run `playwright install chromium` to enable this test")
	}

	// Set up the local HTTP server (the "running local dev server"
	// the spec uses as an example). Serves a small page that the
	// browser tools will read.
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html>
<html><head><title>Loop Test Page</title></head>
<body>
  <h1 id="banner">Loop approval-loop test</h1>
  <p>This page exists so the browser-tool loop test can navigate to
     it without needing external network access.</p>
</body></html>`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	workDir := t.TempDir()
	sessionDir := t.TempDir()
	sessionPath := filepath.Join(sessionDir, "session_test.jsonl")

	// Set up the browser manager and attach it to the loop.
	mgr := browser.NewManager()
	t.Cleanup(func() {
		if err := mgr.Close(); err != nil {
			t.Logf("warning: browser close failed: %v", err)
		}
	})

	// Set up the mock client with the four agent turns:
	//   1. Coder proposes browser_navigate
	//   2. Reviewer approves
	//   3. Coder proposes browser_get_text
	//   4. Reviewer approves
	//   5. Coder proposes task_complete
	//   6. Reviewer approves
	mc := newMockClient()
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall(
			"browser_navigate",
			fmt.Sprintf(`{"url":%q}`, srv.URL+"/"),
		)},
	}})
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{
		Text: "APPROVED. Local test server, no external data exposure.",
	}})
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall(
			"browser_get_text",
			`{"selector":"h1#banner"}`,
		)},
	}})
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{
		Text: "APPROVED. Read-only query.",
	}})
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("task_complete", "{}")},
	}})
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{
		Text: "APPROVED. Done.",
	}})

	tr := transcript.NewTranscript(sessionPath)
	coderCfg := agent.AgentConfig{
		Name:     "Coder",
		BaseURL:  "http://test",
		Model:    "test-model",
		HasTools: true,
	}
	reviewerCfg := agent.AgentConfig{
		Name:     "Reviewer",
		BaseURL:  "http://test",
		Model:    "test-model",
		HasTools: false,
	}
	l := loop.New(tr, coderCfg, reviewerCfg, mc, workDir)
	l.SetBrowser(mgr)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	taskChan := make(chan string, 1)
	taskChan <- "check the local test page banner"
	close(taskChan)

	if err := l.Run(ctx, taskChan); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Verify the transcript contains the expected entries:
	//   - proposed_action: browser_navigate
	//   - action_result:  browser_navigate success
	//   - proposed_action: browser_get_text
	//   - action_result:  browser_get_text with "Loop approval-loop test"
	//   - proposed_action: task_complete
	entries := tr.Entries()

	var sawNavigateProposal, sawNavigateResult bool
	var sawGetTextProposal, sawGetTextResult bool
	var sawTaskCompleteProposal bool
	for _, e := range entries {
		switch e.Type {
		case transcript.TypeProposedAction:
			switch {
			case strings.Contains(e.Content, "browser_navigate"):
				sawNavigateProposal = true
			case strings.Contains(e.Content, "browser_get_text"):
				sawGetTextProposal = true
			case strings.Contains(e.Content, "task_complete"):
				sawTaskCompleteProposal = true
			}
		case transcript.TypeActionResult:
			switch {
			case strings.Contains(e.Content, "loaded") && strings.Contains(e.Content, "Loop Test Page"):
				sawNavigateResult = true
			case strings.Contains(e.Content, "Loop approval-loop test"):
				sawGetTextResult = true
			}
		}
	}

	if !sawNavigateProposal {
		t.Errorf("expected browser_navigate proposed_action in transcript")
	}
	if !sawNavigateResult {
		t.Errorf("expected browser_navigate action_result with page title in transcript")
	}
	if !sawGetTextProposal {
		t.Errorf("expected browser_get_text proposed_action in transcript")
	}
	if !sawGetTextResult {
		t.Errorf("expected browser_get_text action_result with banner text in transcript")
	}
	if !sawTaskCompleteProposal {
		t.Errorf("expected task_complete proposed_action in transcript")
	}
}

// TestBrowserTool_NoManager confirms the loop's safety net: an
// approved browser_* tool call without a configured manager
// surfaces a clean "browser not configured" error rather than
// crashing the session. After the error, Coder acknowledges with
// task_complete so the loop ends cleanly.
func TestBrowserTool_NoManager(t *testing.T) {
	workDir := t.TempDir()
	sessionDir := t.TempDir()
	sessionPath := filepath.Join(sessionDir, "session_test.jsonl")

	mc := newMockClient()
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall(
			"browser_navigate",
			`{"url":"https://example.com/"}`,
		)},
	}})
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{
		Text: "APPROVED. Public site.",
	}})
	// After the failed browser_navigate, Coder proposes task_complete
	// so the loop ends. Without this, the loop spins forever on the
	// repeated mock responses (the mock replays its last entry when
	// the queue is exhausted, which is the documented behaviour for
	// tests, not a real production scenario).
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("task_complete", "{}")},
	}})
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{
		Text: "APPROVED. Done.",
	}})

	tr := transcript.NewTranscript(sessionPath)
	coderCfg := agent.AgentConfig{
		Name:     "Coder",
		BaseURL:  "http://test",
		Model:    "test-model",
		HasTools: true,
	}
	reviewerCfg := agent.AgentConfig{
		Name:     "Reviewer",
		BaseURL:  "http://test",
		Model:    "test-model",
		HasTools: false,
	}
	l := loop.New(tr, coderCfg, reviewerCfg, mc, workDir)
	// Intentionally do NOT call l.SetBrowser.

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	taskChan := make(chan string, 1)
	taskChan <- "navigate"
	close(taskChan)

	// We expect Run to return cleanly (no error — the browser
	// failure is captured in the transcript, the loop continues,
	// and task_complete ends the session).
	if err := l.Run(ctx, taskChan); err != nil {
		t.Fatalf("Run: %v", err)
	}

	entries := tr.Entries()
	var sawError bool
	for _, e := range entries {
		if e.Type == transcript.TypeActionResult && strings.Contains(e.Content, "browser") && strings.Contains(e.Content, "configured") {
			sawError = true
			break
		}
	}
	if !sawError {
		t.Errorf("expected browser-not-configured error in action_result, got entries:")
		for _, e := range entries {
			t.Logf("  [%s] %s: %s", e.Speaker, e.Type, e.Content)
		}
	}
}
