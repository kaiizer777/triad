package loop_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/kaiizer777/triad/internal/agent"
	"github.com/kaiizer777/triad/internal/loop"
	"github.com/kaiizer777/triad/internal/transcript"
)

type loopRewriteTransport struct {
	targetURL string
	transport http.RoundTripper
}

func (t *loopRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	newReq, err := http.NewRequestWithContext(req.Context(), req.Method, t.targetURL, req.Body)
	if err != nil {
		return nil, err
	}
	newReq.Header = req.Header
	tr := t.transport
	if tr == nil {
		tr = http.DefaultTransport
	}
	return tr.RoundTrip(newReq)
}

func TestWebSearch_ProposeReviewExecute_NoAutoCommit(t *testing.T) {
	// 1. Setup mock Firecrawl HTTP server
	firecrawlMockJSON := `{
		"success": true,
		"data": [
			{
				"title": "Go Documentation",
				"url": "https://go.dev",
				"markdown": "# Go Documentation\nOfficial documentation for Go."
			}
		]
	}`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(firecrawlMockJSON))
	}))
	defer ts.Close()

	// Redirect default transport requests to mock server
	oldTransport := http.DefaultTransport
	http.DefaultTransport = &loopRewriteTransport{
		targetURL: ts.URL,
		transport: oldTransport,
	}
	defer func() {
		http.DefaultTransport = oldTransport
	}()

	// 2. Setup mock git repo to verify auto-commit is skipped
	workDir := t.TempDir()
	cmd := exec.Command("git", "init")
	cmd.Dir = workDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to init git repo: %v", err)
	}
	cmd = exec.Command("git", "config", "user.name", "Test User")
	cmd.Dir = workDir
	_ = cmd.Run()
	cmd = exec.Command("git", "config", "user.email", "test@example.com")
	cmd.Dir = workDir
	_ = cmd.Run()

	tr := transcript.NewTranscript("")

	coderCfg := agent.AgentConfig{Name: "Coder", HasTools: true}
	reviewerCfg := agent.AgentConfig{Name: "Reviewer", HasTools: false}

	mc := newMockClient()
	// Turn 1: Coder proposes web_search
	mc.addResponse("Coder", mockResponse{
		resp: agent.AgentResponse{
			ToolCalls: []agent.ToolCall{
				makeToolCall("web_search", `{"query":"go docs"}`),
			},
		},
	})
	// Turn 1: Reviewer approves
	mc.addResponse("Reviewer", mockResponse{
		resp: agent.AgentResponse{
			Text: "APPROVED: search query looks reasonable.",
		},
	})
	// Turn 2: Coder calls task_complete
	mc.addResponse("Coder", mockResponse{
		resp: agent.AgentResponse{
			ToolCalls: []agent.ToolCall{
				makeToolCall("task_complete", `{}`),
			},
		},
	})
	// Turn 2: Reviewer approves task_complete
	mc.addResponse("Reviewer", mockResponse{
		resp: agent.AgentResponse{
			Text: "APPROVED: task is complete.",
		},
	})

	lp := loop.New(tr, coderCfg, reviewerCfg, mc, workDir)
	lp.SetSearchAPIKey("test-key")

	taskChan := make(chan string, 1)
	taskChan <- "Search for Go documentation"
	close(taskChan)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := lp.Run(ctx, taskChan)
	if err != nil {
		t.Fatalf("loop run failed: %v", err)
	}

	// 3. Verify transcript entries
	entries := tr.Entries()

	var foundProposed, foundResult bool
	for _, e := range entries {
		if e.Type == transcript.TypeProposedAction && strings.Contains(e.Content, "web_search") {
			foundProposed = true
		}
		if e.Type == transcript.TypeActionResult && strings.Contains(e.Content, "Go Documentation") {
			foundResult = true
		}
	}

	if !foundProposed {
		t.Errorf("expected proposed_action entry for web_search")
	}
	if !foundResult {
		t.Errorf("expected action_result entry containing 'Go Documentation'")
	}

	// 4. Verify no commits were made for web_search (git log returns 0 commits)
	cmd = exec.Command("git", "log", "-n", "1")
	cmd.Dir = workDir
	out, gitErr := cmd.CombinedOutput()
	if gitErr == nil && strings.Contains(string(out), "commit") {
		t.Errorf("expected no git commits for web_search, but found commit: %s", string(out))
	}
}

func TestWebSearch_QuotaExceededInLoop(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"error": "Quota exceeded"}`))
	}))
	defer ts.Close()

	oldTransport := http.DefaultTransport
	http.DefaultTransport = &loopRewriteTransport{
		targetURL: ts.URL,
		transport: oldTransport,
	}
	defer func() {
		http.DefaultTransport = oldTransport
	}()

	workDir := t.TempDir()
	tr := transcript.NewTranscript("")

	coderCfg := agent.AgentConfig{Name: "Coder", HasTools: true}
	reviewerCfg := agent.AgentConfig{Name: "Reviewer", HasTools: false}

	mc := newMockClient()
	mc.addResponse("Coder", mockResponse{
		resp: agent.AgentResponse{
			ToolCalls: []agent.ToolCall{
				makeToolCall("web_search", `{"query":"rate limited query"}`),
			},
		},
	})
	mc.addResponse("Reviewer", mockResponse{
		resp: agent.AgentResponse{
			Text: "APPROVED",
		},
	})
	mc.addResponse("Coder", mockResponse{
		resp: agent.AgentResponse{
			ToolCalls: []agent.ToolCall{
				makeToolCall("task_complete", `{}`),
			},
		},
	})
	mc.addResponse("Reviewer", mockResponse{
		resp: agent.AgentResponse{
			Text: "APPROVED",
		},
	})

	lp := loop.New(tr, coderCfg, reviewerCfg, mc, workDir)
	lp.SetSearchAPIKey("test-key")

	taskChan := make(chan string, 1)
	taskChan <- "Search for something"
	close(taskChan)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := lp.Run(ctx, taskChan)
	if err != nil {
		t.Fatalf("loop run failed: %v", err)
	}

	entries := tr.Entries()
	var foundQuotaMsg bool
	for _, e := range entries {
		if e.Type == transcript.TypeActionResult && strings.Contains(e.Content, "quota likely exceeded") {
			foundQuotaMsg = true
		}
	}

	if !foundQuotaMsg {
		t.Errorf("expected action_result to contain 'quota likely exceeded' message")
	}
}
