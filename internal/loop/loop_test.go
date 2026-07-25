package loop_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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

// RespondWithKey is like Respond but always pulls from the queue for
// the given logical key, ignoring cfg.Name. Used by the subagent
// wire-up test to route any "Subagent:<id>" call to a single shared
// queue (since the auto-generated ID isn't known up front).
func (m *mockClient) RespondWithKey(_ context.Context, key string, _ agent.AgentConfig, _ []transcript.Entry) (agent.AgentResponse, error) {
	queue := m.queues[key]
	idx := m.calls[key]
	m.calls[key]++

	if len(queue) == 0 {
		return agent.AgentResponse{}, errors.New("mockClient: no responses configured for " + key)
	}
	if idx >= len(queue) {
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
	// Force ModeTriad so all pre-Phase-4 tests exercise the Coder→Reviewer
	// approval path without going through orchestrator routing. Before Phase 4,
	// ModeOrchestrator was a pass-through to Triad; now it does real routing,
	// so any test that wants Triad behavior must set the mode explicitly.
	l.CurrentMode = loop.ModeTriad
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

	// Phase 3 (clarify): the original task "create a file" is bare-action
	// ambiguous, so the loop will surface a clarify round before starting
	// the active cycle. Send a /proceed on taskChan as the second message
	// to unblock the loop, then the pre-loaded InputChan interjection will
	// be drained during the active cycle.
	taskChan := make(chan string, 2)
	taskChan <- "create a file"
	taskChan <- "/proceed"
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

// ---------------------------------------------------------------------------
// Auto-commit tests (docs/work2.md §2.2.8, §2.2.9)
// ---------------------------------------------------------------------------

// newRepoWorkDir creates a temp dir that is already a git repo with
// user.name/email set, and an initial empty commit so `git log` works.
// Mirrors the helper in internal/gitcommit but lives here to keep the
// loop test self-contained.
func newRepoWorkDir(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH; skipping")
	}
	dir := t.TempDir()
	devNull := "NUL"
	if _, err := os.Stat(devNull); err != nil {
		devNull = "/dev/null"
	}
	env := append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+devNull,
		"GIT_CONFIG_SYSTEM="+devNull,
	)
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...) //nolint:gosec
		cmd.Dir = dir
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "--local", "user.email", "test@example.com")
	run("config", "--local", "user.name", "Triad Test")
	run("commit", "--allow-empty", "-q", "-m", "initial")
	return dir
}

func newLoopInRepo(t *testing.T, mc *mockClient, workDir string) (*loop.Loop, *transcript.Transcript, chan string) {
	t.Helper()
	tr := transcript.NewTranscript(filepath.Join(workDir, "test_session.jsonl"))
	coderCfg := agent.AgentConfig{Name: "Coder", HasTools: true}
	reviewerCfg := agent.AgentConfig{Name: "Reviewer", HasTools: false}
	l := loop.New(tr, coderCfg, reviewerCfg, mc, workDir)
	// Buffered for two messages so tests using the Phase 3 clarify
	// step can enqueue both the original task and a /proceed reply
	// without blocking.
	taskChan := make(chan string, 2)
	return l, tr, taskChan
}

// lastCommitSubject runs `git log -1 --pretty=%s` in workDir and returns
// the trimmed subject. Used to verify the auto-commit message format.
func lastCommitSubject(t *testing.T, workDir string) string {
	t.Helper()
	cmd := exec.Command("git", "log", "-1", "--pretty=%s") //nolint:gosec
	cmd.Dir = workDir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// lastCommitShortHash returns the short hash of the HEAD commit.
func lastCommitShortHash(t *testing.T, workDir string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "--short", "HEAD") //nolint:gosec
	cmd.Dir = workDir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// Test 2.2.8 — Approved write_file produces exactly one commit, with
// the file actually changed and a useful message.
func TestAutoCommit_WriteFileProducesOneCommit(t *testing.T) {
	mc := newMockClient()
	workDir := newRepoWorkDir(t)

	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("write_file", `{"path":"hello.txt","content":"hi"}`)},
	}})
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("task_complete", "{}")},
	}})
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{
		Text: "APPROVED.",
	}})
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{
		Text: "APPROVED. Done.",
	}})

	l, tr, taskChan := newLoopInRepo(t, mc, workDir)
	if err := runLoop(t, l, taskChan, "create hello.txt"); err != nil {
		t.Fatalf("runLoop: %v", err)
	}

	// File on disk
	if _, err := os.Stat(filepath.Join(workDir, "hello.txt")); err != nil {
		t.Fatalf("expected hello.txt on disk: %v", err)
	}

	// Exactly one new commit (the initial one is the repo's bootstrap).
	// The initial commit's subject is "initial"; the new one starts with [triad].
	subject := lastCommitSubject(t, workDir)
	if !strings.HasPrefix(subject, "[triad] entry #") {
		t.Errorf("expected subject to start with [triad] entry #, got %q", subject)
	}
	if !strings.Contains(subject, "hello.txt") && !strings.Contains(subject, "write") {
		t.Errorf("expected subject to mention hello.txt or write, got %q", subject)
	}

	// Transcript must contain a System entry recording the commit hash.
	found := false
	for _, e := range tr.Entries() {
		if e.Speaker == transcript.SpeakerSystem && strings.Contains(e.Content, "auto-commit ") {
			if !strings.Contains(e.Content, lastCommitShortHash(t, workDir)) {
				t.Errorf("auto-commit System entry does not reference HEAD hash:\n%s", e.Content)
			}
			found = true
		}
	}
	if !found {
		t.Error("expected a System auto-commit entry in the transcript")
	}
}

// Test 2.2.9 — run_command that touches multiple files commits them
// together as a single commit.
func TestAutoCommit_RunCommandMultiFile(t *testing.T) {
	mc := newMockClient()
	workDir := newRepoWorkDir(t)

	// Pre-create a file so the run_command can modify it alongside
	// creating a new one.
	preExisting := filepath.Join(workDir, "a.txt")
	if err := os.WriteFile(preExisting, []byte("original"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	// And commit it as the starting point so `git status` is clean
	// before the run_command fires.
	for _, args := range [][]string{
		{"add", "--", "a.txt"},
		{"commit", "-q", "-m", "seed"},
	} {
		c := exec.Command("git", args...) //nolint:gosec
		c.Dir = workDir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	// run_command that creates a new file and modifies a.txt.
	cmd := `echo first > b.txt && echo changed > a.txt`
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("run_command", `{"command":"`+cmd+`"}`)},
	}})
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("task_complete", "{}")},
	}})
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{Text: "APPROVED."}})
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{Text: "APPROVED. Done."}})

	l, _, taskChan := newLoopInRepo(t, mc, workDir)
	// Phase 3 (clarify): "modify and create files" is bare-action
	// ambiguous. Send /proceed on taskChan as the second message
	// to unblock the active cycle.
	taskChan <- "modify and create files"
	taskChan <- "/proceed"
	close(taskChan)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := l.Run(ctx, taskChan); err != nil {
		t.Fatalf("runLoop: %v", err)
	}

	// Both files should be in the most recent commit together.
	diffCmd := exec.Command("git", "show", "--name-only", "--pretty=format:", "HEAD") //nolint:gosec
	diffCmd.Dir = workDir
	out, err := diffCmd.Output()
	if err != nil {
		t.Fatalf("git show: %v", err)
	}
	names := strings.TrimSpace(string(out))
	if !strings.Contains(names, "a.txt") {
		t.Errorf("expected a.txt in commit, got:\n%s", names)
	}
	if !strings.Contains(names, "b.txt") {
		t.Errorf("expected b.txt in commit, got:\n%s", names)
	}
}

// Test 2.2.6 — A write_file with identical content produces no commit.
func TestAutoCommit_IdenticalContentNoCommit(t *testing.T) {
	mc := newMockClient()
	workDir := newRepoWorkDir(t)

	// Seed a file and commit it as the starting point.
	if err := os.WriteFile(filepath.Join(workDir, "stable.txt"), []byte("same"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	for _, args := range [][]string{
		{"add", "--", "stable.txt"},
		{"commit", "-q", "-m", "seed stable"},
	} {
		c := exec.Command("git", args...) //nolint:gosec
		c.Dir = workDir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	// Snapshot HEAD so we can confirm no new commit is created.
	headBefore := lastCommitShortHash(t, workDir)

	// write_file with identical content.
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("write_file", `{"path":"stable.txt","content":"same"}`)},
	}})
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("task_complete", "{}")},
	}})
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{Text: "APPROVED."}})
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{Text: "APPROVED. Done."}})

	l, tr, taskChan := newLoopInRepo(t, mc, workDir)
	if err := runLoop(t, l, taskChan, "rewrite same content"); err != nil {
		t.Fatalf("runLoop: %v", err)
	}

	headAfter := lastCommitShortHash(t, workDir)
	if headBefore != headAfter {
		t.Errorf("HEAD changed (%s -> %s) for a no-op write_file", headBefore, headAfter)
	}
	// And the transcript must NOT contain a "[triad] entry" reference
	// in any System entry (no commit was created, so no commit note).
	for _, e := range tr.Entries() {
		if e.Speaker == transcript.SpeakerSystem && strings.Contains(e.Content, "auto-commit ") {
			t.Errorf("unexpected auto-commit System entry for no-op: %s", e.Content)
		}
	}
}

// Test 2.4.1 — Rejected proposals never touch git.
func TestAutoCommit_RejectionDoesNotCommit(t *testing.T) {
	mc := newMockClient()
	workDir := newRepoWorkDir(t)

	// Coder proposes a bad write_file → Reviewer objects → Coder revises.
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("write_file", `{"path":"rejected.txt","content":"bad"}`)},
	}})
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("write_file", `{"path":"rejected.txt","content":"good"}`)},
	}})
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("task_complete", "{}")},
	}})
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{
		Text: "OBJECTION: content is wrong.",
	}})
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{
		Text: "APPROVED. Now correct.",
	}})
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{Text: "APPROVED. Done."}})

	l, _, taskChan := newLoopInRepo(t, mc, workDir)
	if err := runLoop(t, l, taskChan, "rejected then approved"); err != nil {
		t.Fatalf("runLoop: %v", err)
	}

	// Exactly ONE [triad] commit should exist on top of the initial commit
	// (for the approved revision). The rejected proposal must not have
	// created a commit. Walk the last 10 subjects and count [triad] ones.
	listCmd := exec.Command("git", "log", "--pretty=%s", "-n", "10") //nolint:gosec
	listCmd.Dir = workDir
	listOut, err := listCmd.Output()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	triadCount := 0
	for _, line := range strings.Split(strings.TrimSpace(string(listOut)), "\n") {
		if strings.HasPrefix(line, "[triad]") {
			triadCount++
		}
	}
	if triadCount != 1 {
		t.Errorf("expected exactly 1 [triad] commit (for approved revision), got %d. log:\n%s\n",
			triadCount, listOut)
	}
}

// ---------------------------------------------------------------------------
// Test 3.2.7 — Subagent end-to-end (docs/work2.md §3.2 wire-up)
// ---------------------------------------------------------------------------

// TestSpawnSubagent_FullLoop runs the full propose→approve→execute
// cycle where the Coder proposes a spawn_subagent, the Reviewer
// approves, the subagent runs and returns a summary, and ONLY the
// summary bubbles up to the main transcript. The subagent's own
// transcript file is created at <sessionDir>/subagents/<id>.jsonl
// and is NOT visible in the main transcript.
func TestSpawnSubagent_FullLoop(t *testing.T) {
	workDir := t.TempDir()
	mc := newMockClient()

	// Coder turn 1: propose spawn_subagent.
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall(
			"spawn_subagent",
			`{"task":"find existing auth handler","context":"look in internal/handler/"}`,
		)},
	}})
	// Reviewer turn 1: approve the spawn.
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{
		Text: "APPROVED. Reasonable read-only research task.",
	}})
	// Coder turn 2: after the subagent summary, propose task_complete.
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("task_complete", "{}")},
	}})
	// Reviewer turn 2: approve task_complete.
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{
		Text: "APPROVED. Done.",
	}})

	// Phase 3 (clarify): "find existing auth handler" mentions
	// "auth" which triggers the sensitive-surface clarification
	// signal. The test sends a /proceed on taskChan as the second
	// message to unblock the active cycle.

	// Subagent mock responses. The subagent's name is "Subagent:<id>"
	// — since we don't know the ID ahead of time, the mock's lookup
	// by exact name won't match. Instead, we register a response for
	// a "default" name (the mock's "any other agent" fallback) and
	// modify the mock to fall through to a global queue. Simpler
	// approach: use a custom mock that responds to ANY name
	// starting with "Subagent:".
	mc.addResponse("__SUBAGENT__", mockResponse{resp: agent.AgentResponse{
		// Subagent reads a file (which doesn't exist — runner will
		// surface the error to the subagent's own transcript and the
		// subagent will respond with a SUMMARY on the next turn).
		ToolCalls: []agent.ToolCall{makeToolCall(
			"read_file",
			`{"path":"nonexistent.go"}`,
		)},
	}})
	mc.addResponse("__SUBAGENT__", mockResponse{resp: agent.AgentResponse{
		Text: "I tried to read nonexistent.go but it doesn't exist.\nSUMMARY: No auth handler file found in the standard locations.",
	}})

	// Install a wrapper that routes Subagent:* calls to the
	// __SUBAGENT__ queue. We do this by replacing the mock's Respond
	// method via a small adapter struct.
	subMock := &subagentRoutedMock{
		base:        mc,
		subagentKey: "__SUBAGENT__",
	}

	// Run the loop with a real session file path so the subagent
	// has somewhere to put its JSONL.
	sessionDir := t.TempDir()
	sessionPath := filepath.Join(sessionDir, "session_test.jsonl")

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
	l := loop.New(tr, coderCfg, reviewerCfg, subMock, workDir)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	taskChan := make(chan string, 2)
	taskChan <- "find existing auth handler"
	taskChan <- "/proceed"
	close(taskChan)

	if err := l.Run(ctx, taskChan); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// 1) Main transcript should contain the summary, prefixed with "Subagent <id>:".
	entries := tr.Entries()
	var foundSummary bool
	var summaryEntry transcript.Entry
	for _, e := range entries {
		if e.Type == transcript.TypeActionResult && strings.HasPrefix(e.Content, "Subagent ") && strings.Contains(e.Content, "No auth handler file found") {
			foundSummary = true
			summaryEntry = e
			break
		}
	}
	if !foundSummary {
		t.Fatalf("expected main transcript to contain the subagent summary action_result, got entries: %+v", entries)
	}

	// 2) The summary should be the ONLY content the parent loop saw from
	// the subagent — none of the subagent's intermediate proposed_action
	// entries (the read_file) should appear in the main transcript.
	for _, e := range entries {
		if e.Type == transcript.TypeProposedAction && strings.Contains(e.Content, "read_file") && strings.Contains(e.Content, "nonexistent.go") {
			t.Errorf("main transcript leaked subagent's intermediate proposed_action: %s", e.Content)
		}
	}

	// 3) The subagent's own JSONL file should exist at
	// <sessionDir>/subagents/<id>.jsonl. The summary entry's
	// "Subagent <id>:" header tells us the ID.
	// Extract ID from "Subagent <id>: ..." in summaryEntry.Content.
	prefix := strings.TrimPrefix(summaryEntry.Content, "Subagent ")
	colonIdx := strings.Index(prefix, ":")
	if colonIdx < 0 {
		t.Fatalf("could not extract subagent id from summary: %q", summaryEntry.Content)
	}
	subID := strings.TrimSpace(prefix[:colonIdx])
	subPath := filepath.Join(sessionDir, "subagents", subID+".jsonl")
	if _, err := os.Stat(subPath); err != nil {
		t.Errorf("subagent transcript not created at %q: %v", subPath, err)
	}

	// 4) The subagent's own JSONL should contain BOTH the read_file
	// proposed_action AND the summary message — proves the parent
	// saw only the summary, but the subagent's intermediate
	// exploration is preserved on disk for debugging.
	subTr, err := transcript.LoadFromFile(subPath)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
	subEntries := subTr.Entries()
	foundRead := false
	for _, e := range subEntries {
		if e.Type == transcript.TypeProposedAction && strings.Contains(e.Content, "read_file") {
			foundRead = true
			break
		}
	}
	if !foundRead {
		t.Errorf("subagent transcript should contain the read_file proposed_action, got: %+v", subEntries)
	}
}

// subagentRoutedMock wraps a base mockClient and routes any agent
// name starting with "Subagent:" to a single subagent queue. Lets us
// write a wire-up test without knowing the subagent's auto-generated
// ID up front.
type subagentRoutedMock struct {
	base        *mockClient
	subagentKey string
}

func (s *subagentRoutedMock) Respond(ctx context.Context, cfg agent.AgentConfig, entries []transcript.Entry) (agent.AgentResponse, error) {
	if strings.HasPrefix(cfg.Name, transcript.SpeakerSubagent+":") {
		// Route to the subagent queue.
		return s.base.RespondWithKey(ctx, s.subagentKey, cfg, entries)
	}
	return s.base.Respond(ctx, cfg, entries)
}

// ---------------------------------------------------------------------------
// Test 3.2.6 — Rejected spawn_subagent proposals never run the subagent
// ---------------------------------------------------------------------------

// TestSpawnSubagent_RejectionDoesNotSpawn verifies that if the
// Reviewer objects to a spawn_subagent proposal, the subagent is
// never invoked (the parent's review gate still works for
// delegations). Important because the whole point of having a
// Reviewer is that risky decisions get a second look — spawning a
// subagent that itself reads files / runs commands is a way to
// route around the loop, so the Reviewer's veto must still bite.
func TestSpawnSubagent_RejectionDoesNotSpawn(t *testing.T) {
	mc := newMockClient()

	// Coder turn 1: propose spawn_subagent.
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall(
			"spawn_subagent",
			`{"task":"do something risky"}`,
		)},
	}})
	// Reviewer turn 1: OBJECTION.
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{
		Text: "OBJECTION: this task is too vague; be more specific.",
	}})
	// Coder turn 2: revise to a more specific task.
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall(
			"spawn_subagent",
			`{"task":"find the file at internal/handler/foo.go"}`,
		)},
	}})
	// Reviewer turn 2: APPROVE the revised spawn.
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{
		Text: "APPROVED. Specific enough now.",
	}})
	// Subagent queue: respond with a summary.
	mc.addResponse("__SUBAGENT__", mockResponse{resp: agent.AgentResponse{
		Text: "Found it.\nSUMMARY: internal/handler/foo.go exists and contains a single FooHandler.",
	}})
	// Coder turn 3: task_complete.
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("task_complete", "{}")},
	}})
	// Reviewer turn 3: approve task_complete.
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{
		Text: "APPROVED. Done.",
	}})

	subMock := &subagentRoutedMock{base: mc, subagentKey: "__SUBAGENT__"}

	workDir := t.TempDir()
	sessionDir := t.TempDir()
	sessionPath := filepath.Join(sessionDir, "session_test.jsonl")
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
	l := loop.New(tr, coderCfg, reviewerCfg, subMock, workDir)
	// This test exercises the Triad approval path (Coder→Reviewer veto).
	// Force ModeTriad so orchestrator routing doesn't intercept the task.
	l.CurrentMode = loop.ModeTriad


	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	taskChan := make(chan string, 1)
	taskChan <- "find handler file"
	close(taskChan)

	if err := l.Run(ctx, taskChan); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The subagent was invoked EXACTLY ONCE (for the approved revised
	// proposal). The first (rejected) proposal must not have called
	// the subagent at all.
	subMock.subagentKey = "__SUBAGENT__" // no-op; just for clarity
	// Inspect the base mock's call counts via the per-key map.
	// (We piggyback on the existing mockClient.calls field.)
	// Count Subagent invocations by walking entries — easier than
	// exposing internals.
	entries := tr.Entries()
	spawnCalls := 0
	for _, e := range entries {
		if e.Type == transcript.TypeProposedAction && strings.Contains(e.Content, "spawn_subagent") {
			spawnCalls++
		}
	}
	if spawnCalls != 2 {
		t.Errorf("expected 2 spawn_subagent proposals in transcript (1 rejected + 1 approved), got %d", spawnCalls)
	}

	// Confirm the subagent summary appears exactly once (proving the
	// rejected proposal did NOT trigger a subagent run).
	summaryCount := 0
	for _, e := range entries {
		if e.Type == transcript.TypeActionResult && strings.Contains(e.Content, "Subagent ") && strings.Contains(e.Content, "foo.go exists") {
			summaryCount++
		}
	}
	if summaryCount != 1 {
		t.Errorf("expected exactly 1 subagent summary in main transcript, got %d", summaryCount)
	}
}

func TestModeGeneralSingleAgentNoReviewer(t *testing.T) {
	mc := newMockClient()
	l, tr, taskChan := newTestLoop(t, mc)
	l.CurrentMode = loop.ModeGeneral

	mc.addResponse("Coder", mockResponse{
		resp: agent.AgentResponse{Text: "Here is a simple answer in general mode."},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	taskChan <- "explain hello"
	close(taskChan)

	if err := l.Run(ctx, taskChan); err != nil {
		t.Fatalf("Run: %v", err)
	}

	entries := tr.Entries()
	hasReviewer := false
	for _, e := range entries {
		if e.Speaker == transcript.SpeakerReviewer {
			hasReviewer = true
		}
	}
	if hasReviewer {
		t.Errorf("expected no Reviewer entries in ModeGeneral, but found Reviewer entry")
	}
}
