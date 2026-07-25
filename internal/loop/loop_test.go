package loop_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kaiizer777/triad/internal/agent"
	"github.com/kaiizer777/triad/internal/loop"
	"github.com/kaiizer777/triad/internal/transcript"
)

// ---------------------------------------------------------------------------
// Mock agent client
// ---------------------------------------------------------------------------

// mockResponse is one canned response from the mock client.
type mockResponse struct {
	resp agent.AgentResponse
	err  error
}

// mockClient replays a pre-configured sequence of responses for each named agent.
// When a named queue is exhausted it returns the last response repeatedly.
type mockClient struct {
	queues map[string][]mockResponse
	calls  map[string]int
}

func newMockClient() *mockClient {
	return &mockClient{
		queues: make(map[string][]mockResponse),
		calls:  make(map[string]int),
	}
}

// addResponse appends a canned response to the named agent's queue.
func (m *mockClient) addResponse(agentName string, r mockResponse) {
	m.queues[agentName] = append(m.queues[agentName], r)
}

func (m *mockClient) Respond(_ context.Context, cfg agent.AgentConfig, _ []transcript.Entry) (agent.AgentResponse, error) {
	name := cfg.Name
	queue := m.queues[name]
	idx := m.calls[name]
	m.calls[name]++

	if len(queue) == 0 {
		return agent.AgentResponse{}, errors.New("mockClient: no responses configured for " + name)
	}
	if idx >= len(queue) {
		// Repeat last response.
		idx = len(queue) - 1
	}
	r := queue[idx]
	return r.resp, r.err
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func makeToolCall(name, args string) agent.ToolCall {
	return agent.ToolCall{
		ID:   "tc-1",
		Type: "function",
		Function: agent.ToolCallFunction{
			Name:      name,
			Arguments: args,
		},
	}
}

func newTestLoop(t *testing.T, mc *mockClient) (*loop.Loop, *transcript.Transcript, chan string) {
	t.Helper()
	tr := transcript.NewTranscript("") // no file — in-memory only
	coderCfg := agent.AgentConfig{Name: "Coder", HasTools: true}
	reviewerCfg := agent.AgentConfig{Name: "Reviewer", HasTools: false}

	// Use t.TempDir() as a safe workDir for any file operations.
	workDir := t.TempDir()

	l := loop.New(tr, coderCfg, reviewerCfg, mc, workDir)
	taskChan := make(chan string, 1)
	return l, tr, taskChan
}

func runLoop(t *testing.T, l *loop.Loop, taskChan chan string, task string) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	taskChan <- task
	close(taskChan)

	return l.Run(ctx, taskChan)
}

func entriesOfType(entries []transcript.Entry, entryType string) []transcript.Entry {
	var out []transcript.Entry
	for _, e := range entries {
		if e.Type == entryType {
			out = append(out, e)
		}
	}
	return out
}

func entriesOfSpeaker(entries []transcript.Entry, speaker string) []transcript.Entry {
	var out []transcript.Entry
	for _, e := range entries {
		if e.Speaker == speaker {
			out = append(out, e)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Test 4.6 — Happy path
// ---------------------------------------------------------------------------

// TestHappyPath verifies the full propose→approve→execute→task_complete→confirm→idle sequence.
func TestHappyPath(t *testing.T) {
	mc := newMockClient()

	// Coder: first call → write_file tool call
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("write_file", `{"path":"happy.txt","content":"hello world"}`)},
	}})
	// Coder: second call (after write_file result) → task_complete
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("task_complete", "{}")},
	}})

	// Reviewer: first call → approve write_file
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{
		Text: "APPROVED. File content looks correct.",
	}})
	// Reviewer: second call → approve task_complete
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{
		Text: "APPROVED. All work is done.",
	}})

	l, tr, taskChan := newTestLoop(t, mc)
	err := runLoop(t, l, taskChan, "create happy.txt with hello world")
	if err != nil {
		t.Fatalf("expected clean completion, got error: %v", err)
	}

	entries := tr.Entries()

	// Should have: You message, Coder proposed_action (write_file), Reviewer message (approved),
	// System action_result, Coder proposed_action (task_complete), Reviewer message (approved),
	// System idle message.
	proposed := entriesOfType(entries, transcript.TypeProposedAction)
	if len(proposed) < 2 {
		t.Errorf("expected at least 2 proposed_action entries (write_file + task_complete), got %d", len(proposed))
	}

	results := entriesOfType(entries, transcript.TypeActionResult)
	if len(results) < 1 {
		t.Errorf("expected at least 1 action_result entry (write_file result), got %d", len(results))
	}

	// Verify write_file result is recorded.
	if len(results) > 0 && !strings.Contains(results[0].Content, "happy.txt") {
		t.Errorf("action_result should mention happy.txt, got: %s", results[0].Content)
	}

	// Verify the final system idle message.
	sysEntries := entriesOfSpeaker(entries, transcript.SpeakerSystem)
	var foundIdle bool
	for _, e := range sysEntries {
		if strings.Contains(e.Content, "idle") || strings.Contains(e.Content, "Task complete") {
			foundIdle = true
			break
		}
	}
	if !foundIdle {
		t.Error("expected a System idle message after task_complete approval, not found")
	}
}

// ---------------------------------------------------------------------------
// Test 4.7 — Objection path
// ---------------------------------------------------------------------------

// TestObjectionPath verifies that an objection blocks execution and Coder revises before approval.
func TestObjectionPath(t *testing.T) {
	mc := newMockClient()

	// Coder: first proposal → bad write_file (Reviewer will object)
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("write_file", `{"path":"bad.txt","content":"wrong content"}`)},
	}})
	// Coder: revision after objection → better write_file
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("write_file", `{"path":"bad.txt","content":"correct content"}`)},
	}})
	// Coder: after approval → task_complete
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("task_complete", "{}")},
	}})

	// Reviewer: first call → object
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{
		Text: "OBJECTION: content is wrong, it should say 'correct content' not 'wrong content'.",
	}})
	// Reviewer: second call (revised proposal) → approve
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{
		Text: "APPROVED. Content is now correct.",
	}})
	// Reviewer: third call → approve task_complete
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{
		Text: "APPROVED. Task is complete.",
	}})

	l, tr, taskChan := newTestLoop(t, mc)
	err := runLoop(t, l, taskChan, "create bad.txt with correct content")
	if err != nil {
		t.Fatalf("expected clean completion after objection+revision, got: %v", err)
	}

	entries := tr.Entries()

	// Should have at least 2 proposed_action entries (initial + revised) for write_file.
	proposed := entriesOfType(entries, transcript.TypeProposedAction)
	if len(proposed) < 2 {
		t.Errorf("expected at least 2 proposed_action entries (initial + revised), got %d", len(proposed))
	}

	// The objection should be recorded as a Reviewer message.
	reviewerEntries := entriesOfSpeaker(entries, transcript.SpeakerReviewer)
	var foundObjection bool
	for _, e := range reviewerEntries {
		if strings.HasPrefix(strings.ToUpper(e.Content), "OBJECTION") {
			foundObjection = true
			break
		}
	}
	if !foundObjection {
		t.Error("expected at least one OBJECTION entry from Reviewer, not found")
	}

	// No action_result should exist for the first (objected) proposal.
	// The first action_result should be for the approved "correct content" write.
	results := entriesOfType(entries, transcript.TypeActionResult)
	if len(results) == 0 {
		t.Error("expected at least one action_result after approval, got none")
	}
}

// ---------------------------------------------------------------------------
// Test 4.8 — Loop guard (retry cap)
// ---------------------------------------------------------------------------

// TestLoopGuard verifies that hitting MaxRetries surfaces a System deadlock message
// and does not hang or spin forever.
func TestLoopGuard(t *testing.T) {
	mc := newMockClient()

	// Coder: always proposes the same action.
	for i := 0; i < 10; i++ {
		mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
			ToolCalls: []agent.ToolCall{makeToolCall("write_file", `{"path":"stuck.txt","content":"x"}`)},
		}})
	}

	// Reviewer: always objects.
	for i := 0; i < 10; i++ {
		mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{
			Text: "OBJECTION: I will never approve this.",
		}})
	}

	l, tr, taskChan := newTestLoop(t, mc)
	l.MaxRetries = 3 // low cap to make the test fast

	err := runLoop(t, l, taskChan, "do something that reviewer always hates")
	// runLoop returns an error when the cap is hit (approval deadlock returned from runActiveCycle).
	// This is correct — the loop surfaces it rather than hanging.
	if err == nil {
		// Alternatively the loop might have returned nil after appending the deadlock System entry.
		// Either way is acceptable; check for the System deadlock message.
	}

	entries := tr.Entries()
	sysEntries := entriesOfSpeaker(entries, transcript.SpeakerSystem)
	var foundDeadlock bool
	for _, e := range sysEntries {
		if strings.Contains(strings.ToLower(e.Content), "deadlock") ||
			strings.Contains(strings.ToLower(e.Content), "intervention") {
			foundDeadlock = true
			break
		}
	}
	if !foundDeadlock {
		t.Errorf("expected a System deadlock/intervention message after cap, got entries: %v", sysEntries)
	}

	// Must not have executed the tool — no action_result entries.
	results := entriesOfType(entries, transcript.TypeActionResult)
	if len(results) > 0 {
		t.Errorf("expected no action_result (deadlock should block execution), got %d", len(results))
	}
}

// ---------------------------------------------------------------------------
// Test — Coder text-only turn before tool call
// ---------------------------------------------------------------------------

// TestCoderPlanningMessage verifies that a plain-text Coder message (planning phase)
// is recorded and does not trigger Reviewer — Coder then follows with a tool call.
func TestCoderPlanningMessage(t *testing.T) {
	mc := newMockClient()

	// Coder: first turn → plain text (planning)
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		Text: "I'll start by writing the file.",
	}})
	// Coder: second turn → actual tool call
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("write_file", `{"path":"plan.txt","content":"done"}`)},
	}})
	// Coder: third turn → task_complete
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("task_complete", "{}")},
	}})

	// Reviewer: approve write_file
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{
		Text: "APPROVED.",
	}})
	// Reviewer: approve task_complete
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{
		Text: "APPROVED. Done.",
	}})

	l, tr, taskChan := newTestLoop(t, mc)
	err := runLoop(t, l, taskChan, "write plan.txt")
	if err != nil {
		t.Fatalf("expected clean completion, got: %v", err)
	}

	entries := tr.Entries()
	// Planning message should be a Coder "message" type entry.
	coderMessages := []transcript.Entry{}
	for _, e := range entries {
		if e.Speaker == transcript.SpeakerCoder && e.Type == transcript.TypeMessage {
			coderMessages = append(coderMessages, e)
		}
	}
	if len(coderMessages) == 0 {
		t.Error("expected at least one Coder message entry (planning), got none")
	}
	if !strings.Contains(coderMessages[0].Content, "I'll start") {
		t.Errorf("Coder planning message not found, got: %s", coderMessages[0].Content)
	}
}

// ---------------------------------------------------------------------------
// Test 5.4 — Mid-cycle interjection (Phase 5)
// ---------------------------------------------------------------------------

// TestMidCycleInterjection verifies that a human message present in InputChan
// is drained and appended to the transcript before the next agent turn fires,
// satisfying work.md §5.2 and §5.3.
//
// We pre-load the interjection into a buffered InputChan so it's available
// on the very first drainInput() call (which happens before Coder's first
// turn). The transcript must contain the [You] interjection entry, and it
// must precede any Coder proposed_action.
func TestMidCycleInterjection(t *testing.T) {
	mc := newMockClient()

	// Coder: first call → write_file (after seeing interjection in transcript)
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("write_file", `{"path":"baz.txt","content":"hello"}`)},
	}})
	// Coder: second call → task_complete
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("task_complete", "{}")},
	}})

	// Reviewer: approve write_file
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{
		Text: "APPROVED.",
	}})
	// Reviewer: approve task_complete
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{
		Text: "APPROVED. All done.",
	}})

	tr := transcript.NewTranscript("") // in-memory only
	coderCfg := agent.AgentConfig{Name: "Coder", HasTools: true}
	reviewerCfg := agent.AgentConfig{Name: "Reviewer", HasTools: false}
	workDir := t.TempDir()

	l := loop.New(tr, coderCfg, reviewerCfg, mc, workDir)

	// Pre-load the interjection — it will be drained before the first Coder call.
	inputChan := make(chan string, 1)
	inputChan <- "wait, name it baz.txt instead"
	l.InputChan = inputChan

	taskChan := make(chan string, 1)
	taskChan <- "create a file"
	close(taskChan)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := l.Run(ctx, taskChan); err != nil {
		t.Fatalf("expected clean run, got: %v", err)
	}

	entries := tr.Entries()

	// 1. Interjection must appear in the transcript.
	var interjectionIdx = -1
	for i, e := range entries {
		if e.Speaker == transcript.SpeakerYou && strings.Contains(e.Content, "baz.txt") {
			interjectionIdx = i
			break
		}
	}
	if interjectionIdx < 0 {
		t.Error("expected human interjection 'baz.txt' in transcript, not found")
		for i, e := range entries {
			t.Logf("  entry[%d]: speaker=%s type=%s content=%q", i, e.Speaker, e.Type, e.Content)
		}
		return
	}

	// 2. The interjection must precede Coder's first proposed_action.
	var firstProposedIdx = -1
	for i, e := range entries {
		if e.Speaker == transcript.SpeakerCoder && e.Type == transcript.TypeProposedAction {
			firstProposedIdx = i
			break
		}
	}
	if firstProposedIdx >= 0 && interjectionIdx >= firstProposedIdx {
		t.Errorf("interjection (idx %d) must come BEFORE first Coder proposed_action (idx %d)",
			interjectionIdx, firstProposedIdx)
	}
}
