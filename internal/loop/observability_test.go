package loop_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kaiizer777/triad/internal/agent"
	"github.com/kaiizer777/triad/internal/loop"
	"github.com/kaiizer777/triad/internal/tracelog"
	"github.com/kaiizer777/triad/internal/transcript"
	"github.com/kaiizer777/triad/internal/twinsubagent"
)

// ---------------------------------------------------------------------------
// Phase 7 Observability Acceptance Tests (work.md §7.2.1 - §7.2.3).
// ---------------------------------------------------------------------------

// TestObservability_FullTraceSequence_7_2_1 verifies requirement 7.2.1:
// Run a task that undergoes Orchestrator routing, triggers a clarify round,
// and spawns a twin subagent; confirm /trace shows all events in correct
// chronological order in one place.
func TestObservability_FullTraceSequence_7_2_1(t *testing.T) {
	workDir := t.TempDir()
	sessionDir := t.TempDir()
	sessionPath := filepath.Join(sessionDir, "session_obs.jsonl")

	tr := transcript.NewTranscript(sessionPath)
	coderCfg := agent.AgentConfig{Name: "Coder", HasTools: true, BaseURL: "http://mock", Model: "mock"}
	reviewerCfg := agent.AgentConfig{Name: "Reviewer", HasTools: false, BaseURL: "http://mock", Model: "mock"}

	mc := newMockClient()
	l := loop.New(tr, coderCfg, reviewerCfg, mc, workDir)

	// 1. Trigger Orchestrator routing decision
	if err := l.AppendRoutingDecision("fix it — vague task across auth module", loop.TierMiddle, "twin", "medium complexity", false); err != nil {
		t.Fatalf("AppendRoutingDecision failed: %v", err)
	}

	// 2. Trigger clarify phase
	_ = twinsubagent.RunClarifyPhase("fix it", tr, nil)

	// 3. Spawn twin subagent
	mc.addResponse("Twin:obs-task-001", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("task_complete", "{}")},
	}})
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{
		Text: "APPROVED. Task complete.",
	}})

	client := &twinRoutedMock{base: mc, twinKey: "Twin:obs-task-001"}
	runner, err := twinsubagent.NewRunner(client, workDir, sessionDir, 0, 0)
	if err != nil {
		t.Fatalf("failed to create twin runner: %v", err)
	}

	parentCfg := agent.AgentConfig{BaseURL: "http://mock", Model: "mock-model"}
	res, err := runner.Run(context.Background(), "obs-task-001", "fix it — vague task", "", parentCfg)
	if err != nil {
		t.Fatalf("twin runner.Run failed: %v", err)
	}

	if res.Truncated {
		t.Errorf("expected clean twin completion, got truncated")
	}

	// Verify trace log contents
	tracePath := tracelog.TracePathForSession(sessionPath)
	entries, err := tracelog.LoadTrace(tracePath)
	if err != nil {
		t.Fatalf("LoadTrace failed: %v", err)
	}

	if len(entries) < 4 {
		t.Fatalf("expected at least 4 trace entries, got %d", len(entries))
	}

	// Find indices of first occurrences of each required event
	idxRouting, idxClarify, idxTwinSpawn, idxTwinComplete := -1, -1, -1, -1
	for i, e := range entries {
		switch e.EventType {
		case tracelog.EventRoutingDecision:
			if idxRouting == -1 {
				idxRouting = i
			}
		case tracelog.EventClarifyTrigger:
			if idxClarify == -1 {
				idxClarify = i
			}
		case tracelog.EventTwinSpawn:
			if idxTwinSpawn == -1 {
				idxTwinSpawn = i
			}
		case tracelog.EventTwinComplete:
			if idxTwinComplete == -1 {
				idxTwinComplete = i
			}
		}
	}

	if idxRouting == -1 {
		t.Errorf("missing routing_decision in trace log")
	}
	if idxClarify == -1 {
		t.Errorf("missing clarify_trigger in trace log")
	}
	if idxTwinSpawn == -1 {
		t.Errorf("missing twin_spawn in trace log")
	}
	if idxTwinComplete == -1 {
		t.Errorf("missing twin_complete in trace log")
	}

	// Confirm chronological ordering: routing -> clarify -> twin_spawn -> twin_complete
	if !(idxRouting < idxClarify && idxClarify < idxTwinSpawn && idxTwinSpawn < idxTwinComplete) {
		t.Errorf("events out of order: routing=%d, clarify=%d, twin_spawn=%d, twin_complete=%d",
			idxRouting, idxClarify, idxTwinSpawn, idxTwinComplete)
	}

	formatted := tracelog.FormatTraceOutput(entries)
	if !strings.Contains(formatted, "Session Trace Log") {
		t.Errorf("FormatTraceOutput missing header: %q", formatted)
	}
}

// TestObservability_TwinLifecycle_7_2_2 verifies requirement 7.2.2:
// Confirm twin subagent start (6.15 / twin_spawn) and completion (twin_complete)
// both appear as distinct, matched entries in the trace log.
func TestObservability_TwinLifecycle_7_2_2(t *testing.T) {
	workDir := t.TempDir()
	sessionDir := t.TempDir()

	twinID := "twin-lifecycle-001"
	mc := newMockClient()
	mc.addResponse("Twin:"+twinID, mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("task_complete", "{}")},
	}})
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{
		Text: "APPROVED",
	}})

	client := &twinRoutedMock{base: mc, twinKey: "Twin:" + twinID}
	runner, err := twinsubagent.NewRunner(client, workDir, sessionDir, 0, 0)
	if err != nil {
		t.Fatalf("failed to create twin runner: %v", err)
	}

	parentCfg := agent.AgentConfig{BaseURL: "http://mock", Model: "mock-model"}
	_, err = runner.Run(context.Background(), twinID, "test twin lifecycle", "", parentCfg)
	if err != nil {
		t.Fatalf("runner.Run failed: %v", err)
	}

	// Compute expected twin trace log path
	twinTranscriptPath := filepath.Join(sessionDir, "twins", twinID+".jsonl")
	tracePath := tracelog.TracePathForSession(twinTranscriptPath)

	entries, err := tracelog.LoadTrace(tracePath)
	if err != nil {
		t.Fatalf("LoadTrace failed for twin trace: %v", err)
	}

	var spawnFound, completeFound bool
	for _, e := range entries {
		if e.Entity == "twin:"+twinID {
			if e.EventType == tracelog.EventTwinSpawn {
				spawnFound = true
			}
			if e.EventType == tracelog.EventTwinComplete {
				completeFound = true
			}
		}
	}

	if !spawnFound {
		t.Errorf("§7.2.2 FAIL: twin_spawn event not found for twin:%s", twinID)
	}
	if !completeFound {
		t.Errorf("§7.2.2 FAIL: twin_complete event not found for twin:%s", twinID)
	}
}

// TestObservability_FlatAndScannable_7_2_3 verifies requirement 7.2.3:
// Confirm the trace log stays flat and scannable across multiple sessions and events.
func TestObservability_FlatAndScannable_7_2_3(t *testing.T) {
	sessionDir := t.TempDir()
	sessionPath := filepath.Join(sessionDir, "session_scannable.jsonl")
	tracePath := tracelog.TracePathForSession(sessionPath)

	events := []tracelog.Entry{
		{Entity: "orchestrator", EventType: tracelog.EventRoutingDecision, Description: "Routed task to general (trivial)"},
		{Entity: "clarify", EventType: tracelog.EventClarifyTrigger, Description: "Clarification requested (1 question(s))"},
		{Entity: "twin:sub-01", EventType: tracelog.EventTwinSpawn, Description: "Spawned twin subagent sub-01"},
		{Entity: "hook:blocklist", EventType: tracelog.EventHookIntervention, Description: "Blocked dangerous command"},
		{Entity: "twin:sub-01", EventType: tracelog.EventTwinComplete, Description: "Completed twin subagent sub-01"},
	}

	for _, ev := range events {
		if err := tracelog.Append(tracePath, ev); err != nil {
			t.Fatalf("Append failed: %v", err)
		}
		time.Sleep(2 * time.Millisecond)
	}

	loaded, err := tracelog.LoadTrace(tracePath)
	if err != nil {
		t.Fatalf("LoadTrace failed: %v", err)
	}

	if len(loaded) != 5 {
		t.Fatalf("expected 5 trace entries, got %d", len(loaded))
	}

	output := tracelog.FormatTraceOutput(loaded)
	lines := strings.Split(output, "\n")
	if len(lines) != 6 { // 1 header line + 5 entry lines
		t.Errorf("expected 6 lines in scannable trace output, got %d", len(lines))
	}

	for i, ev := range events {
		line := lines[i+1]
		if !strings.Contains(line, "["+ev.Entity+"]") || !strings.Contains(line, "("+ev.EventType+")") {
			t.Errorf("scannable line %d mismatch: got %q, want entity %q event %q", i, line, ev.Entity, ev.EventType)
		}
	}
}
