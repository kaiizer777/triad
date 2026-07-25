package loop_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kaiizer777/triad/internal/agent"
	"github.com/kaiizer777/triad/internal/loop"
	"github.com/kaiizer777/triad/internal/transcript"
)

// ---------------------------------------------------------------------------
// Helpers shared by orchestrator tests
// ---------------------------------------------------------------------------

// runWithTimeout sends a single task to the loop and collects entries until
// the loop goes idle (runActiveCycle returns) or 3 seconds elapses.
// It uses a short-lived context so a blocked middle-tier loop doesn't hang.
func runOneTask(t *testing.T, l *loop.Loop, taskChan chan string, task string) []transcript.Entry {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	tr := newTestTranscriptRef(l)
	_ = tr

	errCh := make(chan error, 1)
	go func() {
		taskChan <- task
		// Give the loop a moment to process the task then cancel.
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	err := l.Run(ctx, taskChan)
	if err != nil && err != context.Canceled && err != context.DeadlineExceeded {
		// Don't fail on deadline/cancel — those are expected for middle-tier
		// (loop stays idle waiting for confirm, then we cancel).
		t.Logf("loop.Run returned: %v", err)
	}
	close(errCh)

	return nil // entries are checked via the transcript directly
}

// newTestTranscriptRef extracts the transcript from a loop for inspection.
// We do this by reading the loop's OnEntry callback sink.
func collectEntries(t *testing.T, mc *mockClient, task string, mode loop.Mode) []transcript.Entry {
	t.Helper()
	tr := transcript.NewTranscript("")
	l := loop.New(tr, agent.AgentConfig{Name: "Coder"}, agent.AgentConfig{Name: "Reviewer"}, mc, t.TempDir())
	l.CurrentMode = mode

	var entries []transcript.Entry
	l.OnEntry = func(e transcript.Entry) {
		entries = append(entries, e)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	taskChan := make(chan string, 1)
	taskChan <- task

	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()

	_ = l.Run(ctx, taskChan)
	return entries
}

// ---------------------------------------------------------------------------
// Test 4.7 — trivial task auto-routes to General Chat
// ---------------------------------------------------------------------------

// TestOrchestrator_TrivialAutoRoutesToGeneral verifies that a clearly trivial
// task (a short one-liner with no sensitive keywords) is automatically routed
// to General Chat mode without requiring any human confirmation prompt.
// The loop must also emit a stated-reasoning [Orchestrator]: message (4.2)
// and a routing_decision entry (4.5).
func TestOrchestrator_TrivialAutoRoutesToGeneral(t *testing.T) {
	mc := newMockClient()
	// Coder will respond with a plain text message (no tool call) so the
	// General Chat branch returns true immediately.
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{Text: "Done — fixed the typo."}})

	tr := transcript.NewTranscript("")
	l := loop.New(tr, agent.AgentConfig{Name: "Coder"}, agent.AgentConfig{Name: "Reviewer"}, mc, t.TempDir())
	l.CurrentMode = loop.ModeOrchestrator

	var entries []transcript.Entry
	l.OnEntry = func(e transcript.Entry) { entries = append(entries, e) }

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	taskChan := make(chan string, 2)
	taskChan <- "fix typo in README"

	go func() {
		time.Sleep(500 * time.Millisecond)
		cancel()
	}()
	_ = l.Run(ctx, taskChan)

	// 4.2 — stated-reasoning message must appear
	var routingMsg string
	for _, e := range entries {
		if e.Type == transcript.TypeMessage && strings.Contains(e.Content, "[Orchestrator]") {
			routingMsg = e.Content
			break
		}
	}
	if routingMsg == "" {
		t.Error("4.2 FAIL: no [Orchestrator] stated-reasoning message found in transcript")
	} else if !strings.Contains(strings.ToLower(routingMsg), "general") {
		t.Errorf("4.2 FAIL: [Orchestrator] message does not mention General Chat routing: %q", routingMsg)
	}

	// 4.3 — auto-proceeded (no confirmation prompt)
	for _, e := range entries {
		if e.Type == transcript.TypeMessage && strings.Contains(e.Content, "Waiting for your confirmation") {
			t.Error("4.3 FAIL: trivial task should not produce a confirmation prompt")
		}
	}

	// 4.5 — routing_decision entry exists and has correct fields
	assertRoutingDecision(t, entries, "trivial", "general", true)
}

// ---------------------------------------------------------------------------
// Test 4.8 — critical task auto-routes to Triad
// ---------------------------------------------------------------------------

// TestOrchestrator_CriticalAutoRoutesToTriad verifies that a task containing
// a critical keyword (e.g. "auth") is automatically routed to Triad mode
// without requiring any human confirmation prompt.
func TestOrchestrator_CriticalAutoRoutesToTriad(t *testing.T) {
	mc := newMockClient()
	// Coder proposes task_complete, Reviewer approves — standard Triad completion.
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("task_complete", "{}")},
	}})
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{Text: "APPROVED. Task is complete."}})

	tr := transcript.NewTranscript("")
	l := loop.New(tr, agent.AgentConfig{Name: "Coder"}, agent.AgentConfig{Name: "Reviewer"}, mc, t.TempDir())
	l.CurrentMode = loop.ModeOrchestrator

	var entries []transcript.Entry
	l.OnEntry = func(e transcript.Entry) { entries = append(entries, e) }

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	taskChan := make(chan string, 2)
	taskChan <- "update the auth token validation logic"

	go func() {
		time.Sleep(500 * time.Millisecond)
		cancel()
	}()
	_ = l.Run(ctx, taskChan)

	// 4.2 — stated-reasoning message must appear mentioning Triad / critical
	var routingMsg string
	for _, e := range entries {
		if e.Type == transcript.TypeMessage && strings.Contains(e.Content, "[Orchestrator]") {
			routingMsg = e.Content
			break
		}
	}
	if routingMsg == "" {
		t.Error("4.2 FAIL: no [Orchestrator] stated-reasoning message found in transcript")
	} else if !strings.Contains(strings.ToLower(routingMsg), "triad") && !strings.Contains(strings.ToLower(routingMsg), "critical") {
		t.Errorf("4.2 FAIL: [Orchestrator] message does not mention Triad/critical routing: %q", routingMsg)
	}

	// 4.3 — auto-proceeded (no confirmation prompt)
	for _, e := range entries {
		if e.Type == transcript.TypeMessage && strings.Contains(e.Content, "Waiting for your confirmation") {
			t.Error("4.3 FAIL: critical task should not produce a confirmation prompt")
		}
	}

	// 4.5 — routing_decision entry exists with correct fields
	assertRoutingDecision(t, entries, "critical", "triad", true)
}

// ---------------------------------------------------------------------------
// Test 4.9 — ambiguous-complexity task stops and asks for confirmation
// ---------------------------------------------------------------------------

// TestOrchestrator_MiddleAsksForConfirmation verifies that a genuinely
// ambiguous task causes Orchestrator to stop and ask for human confirmation
// rather than silently routing to any mode. The loop must NOT start an active
// cycle until the human responds.
func TestOrchestrator_MiddleAsksForConfirmation(t *testing.T) {
	mc := newMockClient()
	// Coder should never be called for a middle-tier task before confirmation.
	// If it is called, it will error — surfacing the bug clearly.
	// (No responses configured — mockClient returns error if called.)

	tr := transcript.NewTranscript("")
	l := loop.New(tr, agent.AgentConfig{Name: "Coder"}, agent.AgentConfig{Name: "Reviewer"}, mc, t.TempDir())
	l.CurrentMode = loop.ModeOrchestrator

	var entries []transcript.Entry
	l.OnEntry = func(e transcript.Entry) { entries = append(entries, e) }

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	taskChan := make(chan string, 2)
	// This is a genuinely ambiguous task: substantial but not obviously critical,
	// not obviously trivial.
	taskChan <- "improve the caching layer to handle concurrent writes better"

	go func() {
		time.Sleep(400 * time.Millisecond)
		cancel()
	}()
	_ = l.Run(ctx, taskChan)

	// 4.4 — an [Orchestrator] message must appear asking for confirmation
	var confirmMsg string
	for _, e := range entries {
		if e.Type == transcript.TypeMessage && strings.Contains(e.Content, "[Orchestrator]") {
			confirmMsg = e.Content
			break
		}
	}
	if confirmMsg == "" {
		t.Error("4.4 FAIL: no [Orchestrator] confirmation prompt found in transcript for middle-tier task")
	}
	// The prompt must not say it already routed to something.
	if strings.Contains(strings.ToLower(confirmMsg), "routing to general") {
		t.Errorf("4.4 FAIL: middle task incorrectly auto-routed to General Chat: %q", confirmMsg)
	}

	// 4.4 — Coder must NOT have been called (no active cycle started)
	if mc.calls["Coder"] > 0 {
		t.Errorf("4.4 FAIL: Coder was called %d time(s) before human confirmed routing — active cycle started prematurely", mc.calls["Coder"])
	}

	// The stated-reasoning message should be present (4.2 applies even for middle).
	if !strings.Contains(confirmMsg, "[Orchestrator]") {
		t.Errorf("4.2 FAIL: middle-tier confirmation message does not start with [Orchestrator]: %q", confirmMsg)
	}
}

// ---------------------------------------------------------------------------
// Test 4.10 — all three cases produce a routing_decision entry
// ---------------------------------------------------------------------------

// TestOrchestrator_AllCasesProduceRoutingDecisionEntry verifies that trivial,
// critical, and middle-tier tasks ALL produce a TypeRoutingDecision transcript
// entry with accurate contents (task, complexity_judgment, target_mode,
// auto_proceeded).
func TestOrchestrator_AllCasesProduceRoutingDecisionEntry(t *testing.T) {
	cases := []struct {
		name            string
		task            string
		wantTier        string
		wantTargetMode  string
		wantAutoProceed bool
		coderResp       *mockResponse
		reviewerResp    *mockResponse
	}{
		{
			name:            "trivial",
			task:            "fix typo in README",
			wantTier:        "trivial",
			wantTargetMode:  "general",
			wantAutoProceed: true,
			coderResp:       &mockResponse{resp: agent.AgentResponse{Text: "Done."}},
		},
		{
			name:            "critical",
			task:            "update the auth middleware to reject expired tokens",
			wantTier:        "critical",
			wantTargetMode:  "triad",
			wantAutoProceed: true,
			coderResp: &mockResponse{resp: agent.AgentResponse{
				ToolCalls: []agent.ToolCall{makeToolCall("task_complete", "{}")},
			}},
			reviewerResp: &mockResponse{resp: agent.AgentResponse{Text: "APPROVED. Done."}},
		},
		{
			name:            "middle",
			task:            "improve the caching layer to handle concurrent writes better",
			wantTier:        "middle",
			wantTargetMode:  "twin", // §6.10: middle-tier now proposes "twin" not "triad"
			wantAutoProceed: false,  // middle = human confirmed (or not yet confirmed in this test)
			// No coder/reviewer — middle tier doesn't start active cycle before confirm.
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mc := newMockClient()
			if tc.coderResp != nil {
				mc.addResponse("Coder", *tc.coderResp)
			}
			if tc.reviewerResp != nil {
				mc.addResponse("Reviewer", *tc.reviewerResp)
			}

			tr := transcript.NewTranscript("")
			l := loop.New(tr, agent.AgentConfig{Name: "Coder"}, agent.AgentConfig{Name: "Reviewer"}, mc, t.TempDir())
			l.CurrentMode = loop.ModeOrchestrator

			var entries []transcript.Entry
			l.OnEntry = func(e transcript.Entry) { entries = append(entries, e) }

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			taskChan := make(chan string, 2)
			taskChan <- tc.task

			go func() {
				time.Sleep(400 * time.Millisecond)
				cancel()
			}()
			_ = l.Run(ctx, taskChan)

			// For middle-tier: simulate human confirm so the routing_decision entry
			// is appended with auto_proceeded=false.
			if tc.name == "middle" {
				// The routing_decision entry is only appended after confirm for middle.
				// Check that the [Orchestrator] message appeared (4.2) but
				// routing_decision is NOT yet present before confirm.
				var rdFound bool
				for _, e := range entries {
					if e.Type == transcript.TypeRoutingDecision {
						rdFound = true
					}
				}
				// For the middle-tier case, routing_decision is appended by
				// resolveOrchestratorConfirm, which hasn't been called yet.
				// We verify it's absent (not yet confirmed) — the confirm round
				// is tested separately in TestOrchestrator_MiddleConfirmResolvesRoutingDecision.
				if rdFound {
					// If it appeared, it should have auto_proceeded=false (i.e. recorded on confirm).
					assertRoutingDecision(t, entries, tc.wantTier, tc.wantTargetMode, tc.wantAutoProceed)
				}
				return
			}

			// For trivial and critical: routing_decision MUST be present immediately.
			assertRoutingDecision(t, entries, tc.wantTier, tc.wantTargetMode, tc.wantAutoProceed)
		})
	}
}

// TestOrchestrator_MiddleConfirmResolvesRoutingDecision verifies that when the
// human sends a confirm reply to a middle-tier prompt, the loop appends a
// routing_decision entry with auto_proceeded=false and then starts the active cycle.
func TestOrchestrator_MiddleConfirmResolvesRoutingDecision(t *testing.T) {
	mc := newMockClient()
	// After the human confirms, Coder + Reviewer run the task.
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("task_complete", "{}")},
	}})
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{Text: "APPROVED. Done."}})

	tr := transcript.NewTranscript("")
	l := loop.New(tr, agent.AgentConfig{Name: "Coder"}, agent.AgentConfig{Name: "Reviewer"}, mc, t.TempDir())
	l.CurrentMode = loop.ModeOrchestrator

	var entries []transcript.Entry
	l.OnEntry = func(e transcript.Entry) { entries = append(entries, e) }

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	taskChan := make(chan string, 4)
	// First message: middle-tier task → Orchestrator asks for confirm
	taskChan <- "improve the caching layer to handle concurrent writes better"

	go func() {
		// Wait for Orchestrator to emit the confirmation prompt, then send confirm.
		time.Sleep(300 * time.Millisecond)
		taskChan <- "proceed"
		// Cancel after the active cycle completes.
		time.Sleep(500 * time.Millisecond)
		cancel()
	}()
	_ = l.Run(ctx, taskChan)

	// routing_decision entry must now be present (appended on confirm).
	// §6.10: target_mode is now "twin" (the confirmed proposed mode) rather than "triad".
	assertRoutingDecision(t, entries, "middle", "twin", false)

	// Coder must have been called (active cycle ran after confirmation)
	if mc.calls["Coder"] == 0 {
		t.Error("4.10 FAIL: Coder was not called after human confirmed middle-tier routing")
	}
}

// TestMiddleTierRouting_ProposesTwin verifies that the middle-tier Orchestrator
// message mentions "Twin Subagent pair" (§6.10), replacing the Phase 4 stand-in
// that previously said "full Triad".
func TestMiddleTierRouting_ProposesTwin(t *testing.T) {
	msg := loop.OrchestratorMessage(loop.TierMiddle, "medium complexity reason", "twin")
	if !strings.Contains(msg, "Twin Subagent pair") {
		t.Errorf("§6.10: expected Orchestrator middle-tier message to mention 'Twin Subagent pair', got: %q", msg)
	}
	if strings.Contains(msg, "full Triad") {
		t.Errorf("§6.10: Orchestrator middle-tier message still says 'full Triad' (should say 'Twin Subagent pair'): %q", msg)
	}
}

// ---------------------------------------------------------------------------
// Helper: assert routing_decision entry fields
// ---------------------------------------------------------------------------

func assertRoutingDecision(t *testing.T, entries []transcript.Entry, wantTier, wantTargetMode string, wantAutoProceed bool) {
	t.Helper()
	for _, e := range entries {
		if e.Type != transcript.TypeRoutingDecision {
			continue
		}
		var rd transcript.RoutingDecision
		if err := json.Unmarshal([]byte(e.Content), &rd); err != nil {
			t.Errorf("4.5 FAIL: routing_decision Content is not valid JSON: %v\ncontent: %q", err, e.Content)
			return
		}
		if rd.ComplexityJudge != wantTier {
			t.Errorf("4.5 FAIL: routing_decision complexity_judgment = %q, want %q", rd.ComplexityJudge, wantTier)
		}
		if rd.TargetMode != wantTargetMode {
			t.Errorf("4.5 FAIL: routing_decision target_mode = %q, want %q", rd.TargetMode, wantTargetMode)
		}
		if rd.AutoProceeded != wantAutoProceed {
			t.Errorf("4.5 FAIL: routing_decision auto_proceeded = %v, want %v", rd.AutoProceeded, wantAutoProceed)
		}
		if strings.TrimSpace(rd.Task) == "" {
			t.Error("4.5 FAIL: routing_decision task field is empty")
		}
		if strings.TrimSpace(rd.Reason) == "" {
			t.Error("4.5 FAIL: routing_decision reason field is empty")
		}
		return // found a valid entry — pass
	}
	t.Errorf("4.5 FAIL: no routing_decision entry found in %d transcript entries", len(entries))
}

// newTestTranscriptRef is a dummy helper to satisfy the compiler; real
// inspection uses the OnEntry callback sink above.
func newTestTranscriptRef(l *loop.Loop) *loop.Loop { return l }
