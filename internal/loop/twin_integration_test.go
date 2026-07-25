package loop_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kaiizer777/triad/internal/agent"
	"github.com/kaiizer777/triad/internal/loop"
	"github.com/kaiizer777/triad/internal/transcript"
)

// ---------------------------------------------------------------------------
// Phase 6 loop-integration tests (work.md §6.11, §6.17).
//
// These tests sit at the loop-package level — they drive the full Loop with
// mock agents and verify the end-to-end behavior:
//   - §6.11: A genuinely medium-complexity task makes Orchestrator propose
//     the twin-subagent route. On human confirm, the Coder (running under
//     ModeTriad) proposes spawn_twin_subagent, the Reviewer approves, and
//     only a clean summary lands in the main transcript. The twin's own
//     transcript at <sessionDir>/twins/<id>.jsonl is created and contains
//     the private propose→review→execute cycle.
//   - §6.17: A start-of-spawn log entry ("[System]: Twin subagent started
//     for task: ...") appears in the main transcript IMMEDIATELY when the
//     twin pair is spawned — well before the twin's eventual summary
//     arrives. This is the minimum-viable visibility fix from §6.15; full
//     cross-agent observability is Phase 7.
//
// The pattern follows TestSpawnSubagent_FullLoop (Test 3.2.7) — same mock
// plumbing, same wire-up approach, but for twins and with Orchestrator
// routing in front.
// ---------------------------------------------------------------------------

// twinRoutedMock routes any agent name starting with "Twin:" to a single
// twin queue. Mirrors subagentRoutedMock above; kept separate so the two
// delegation routes can be tested independently.
type twinRoutedMock struct {
	base      *mockClient
	twinKey   string
	twinCalls int
}

func (s *twinRoutedMock) Respond(ctx context.Context, cfg agent.AgentConfig, entries []transcript.Entry) (agent.AgentResponse, error) {
	if strings.HasPrefix(cfg.Name, transcript.SpeakerTwin+":") {
		// mini-Coder call — route to the twin queue.
		s.twinCalls++
		return s.base.RespondWithKey(ctx, s.twinKey, cfg, entries)
	}
	// Otherwise, route to the main Coder/Reviewer queues.
	// mini-Reviewer calls come through as cfg.Name "Reviewer-twin:<id>"
	// (built by MiniReviewerConfig) but the main-session Reviewer and
	// twin-Reviewer both speak APPROVED/OBJECTION: — the routing here
	// intentionally collapses them onto a single "Reviewer" queue to
	// keep the test wiring minimal. In production, both are the same
	// HasTools:false config and the review vocabulary is identical.
	if strings.HasPrefix(cfg.Name, transcript.SpeakerReviewer+"-twin:") {
		return s.base.RespondWithKey(ctx, transcript.SpeakerReviewer, cfg, entries)
	}
	return s.base.Respond(ctx, cfg, entries)
}

// ---------------------------------------------------------------------------
// §6.11 — Medium-complexity task → Orchestrator proposes twin → twin runs
// privately → only summary in main transcript
// ---------------------------------------------------------------------------// ---------------------------------------------------------------------------
// §6.11 — Medium-complexity task → Orchestrator proposes twin → twin runs
// privately → only summary in main transcript
// ---------------------------------------------------------------------------

// TestTwin_FullLoop_MediumTaskRoutesThroughTwin is the §6.11 acceptance
// test. It drives the full Orchestrator→Twin flow end-to-end:
//   1. Human sends a medium-complexity task.
//   2. Orchestrator emits a stated-reasoning message and asks for confirm
//      (4.2, 4.4).
//   3. Human sends "proceed".
//   4. Coder proposes spawn_twin_subagent (the active cycle runs under
//      ModeTriad per the §6.10 routing mapping).
//   5. Reviewer approves.
//   6. The twin pair runs its own private propose→review→execute cycle
//      (its own transcript file at <sessionDir>/twins/<id>.jsonl).
//   7. ONLY a single clean summary lands in the main transcript as an
//      action_result entry attributed to "Twin:<id>". The twin's
//      intermediate proposed_action entries (read_file etc.) do NOT
//      leak into the main transcript.
func TestTwin_FullLoop_MediumTaskRoutesThroughTwin(t *testing.T) {
	workDir := t.TempDir()
	sessionDir := t.TempDir()
	sessionPath := filepath.Join(sessionDir, "session_test.jsonl")

	mc := newMockClient()

	// Sequence: Orchestrator asks for confirm. Human sends "proceed".
	// Coder then proposes spawn_twin_subagent. Reviewer approves.
	// After the twin's summary, Coder proposes task_complete. Reviewer
	// approves.

	// Coder turn 1 (after human confirm): propose spawn_twin_subagent.
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall(
			"spawn_twin_subagent",
			`{"task":"add a rate-limiting middleware to internal/api","context":"use a token-bucket algorithm"}`,
		)},
	}})
	// Reviewer turn 1: approve the spawn.
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{
		Text: "APPROVED. Medium-complexity task, twin-subagent is the right tool.",
	}})
	// Coder turn 2 (after twin summary): propose task_complete.
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("task_complete", "{}")},
	}})
	// Reviewer turn 2: approve task_complete.
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{
		Text: "APPROVED. Done.",
	}})

	// Twin pair responses. The twin's mini-Coder will read a file (a
	// stub file we'll create so read_file succeeds), then call
	// task_complete; the mini-Reviewer will approve both.
	mc.addResponse("__TWIN__", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall(
			"read_file",
			`{"path":"stub.go"}`,
		)},
	}})
	// mini-Reviewer approves the read_file (against the
	// "Reviewer" queue, routed by twinRoutedMock).
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{
		Text: "APPROVED. read_file is safe.",
	}})
	// mini-Coder: after read_file result, call task_complete.
	mc.addResponse("__TWIN__", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("task_complete", "{}")},
	}})
	// mini-Reviewer approves task_complete.
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{
		Text: "APPROVED. Twin task done.",
	}})

	// Create a stub file so the twin's read_file succeeds.
	if err := os.WriteFile(filepath.Join(workDir, "stub.go"), []byte("package stub\n"), 0o644); err != nil {
		t.Fatalf("setup: write stub.go: %v", err)
	}

	// Build a routing mock that sends Twin:* to __TWIN__ and
	// Reviewer-twin:* to the shared "Reviewer" queue.
	twinMock := &twinRoutedMock{base: mc, twinKey: "__TWIN__"}

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

	tr := transcript.NewTranscript(sessionPath)
	l := loop.New(tr, coderCfg, reviewerCfg, twinMock, workDir)
	// §6.10: Orchestrator mode is the default; the middle-tier routing
	// (Twin) happens via this mode's runOrchestratorRouting.
	l.CurrentMode = loop.ModeOrchestrator

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Two messages: medium-complexity task, then "proceed" confirmation.
	taskChan := make(chan string, 2)
	taskChan <- "improve the caching layer to handle concurrent writes better"
	taskChan <- "proceed"
	close(taskChan)

	if err := l.Run(ctx, taskChan); err != nil {
		t.Fatalf("Run: %v", err)
	}

	entries := tr.Entries()

	// (a) Orchestrator emitted a stated-reasoning message mentioning
	// Twin Subagent pair (§4.2 + §6.10).
	var sawTwinProposal bool
	for _, e := range entries {
		if e.Type == transcript.TypeMessage &&
			strings.Contains(e.Content, "[Orchestrator]") &&
			strings.Contains(e.Content, "Twin Subagent pair") {
			sawTwinProposal = true
			break
		}
	}
	if !sawTwinProposal {
		t.Error("§6.11 FAIL: expected Orchestrator to propose 'Twin Subagent pair' for the medium-complexity task")
	}

	// (b) The Coder proposed spawn_twin_subagent (the medium-tier route
	// triggers ModeTriad, whose Coder uses the spawn tool).
	var sawSpawnProposal bool
	for _, e := range entries {
		if e.Type == transcript.TypeProposedAction && strings.Contains(e.Content, "spawn_twin_subagent") {
			sawSpawnProposal = true
			break
		}
	}
	if !sawSpawnProposal {
		t.Error("§6.11 FAIL: expected at least one spawn_twin_subagent proposed_action entry")
	}

	// (c) A routing_decision entry was appended with target_mode=twin.
	var sawTwinRoutingDecision bool
	for _, e := range entries {
		if e.Type == transcript.TypeRoutingDecision && strings.Contains(e.Content, `"target_mode":"twin"`) {
			sawTwinRoutingDecision = true
			break
		}
	}
	if !sawTwinRoutingDecision {
		t.Error("§6.11 FAIL: expected routing_decision entry with target_mode='twin' for the medium-complexity task")
	}

	// (d) The twin's summary action_result landed in the main transcript
	// — attributed with "Twin:<id>" prefix per §6.9. We don't assert
	// on a specific summary phrase because the runner's exact summary
	// wording depends on which sub-turns the mini-Coder chose to run
	// (read_file first vs. task_complete first) and on the mock
	// behaviour. The structural guarantees are: action_result type,
	// Speaker "System" (the loop's append of the runner's returned
	// string), and "[Twin:<id>]:" prefix. Everything else is content.
	var summary *transcript.Entry
	for i := range entries {
		e := entries[i]
		if e.Type == transcript.TypeActionResult &&
			e.Speaker == transcript.SpeakerSystem &&
			strings.HasPrefix(e.Content, "[Twin:") {
			summary = &entries[i]
			break
		}
	}
	if summary == nil {
		t.Fatalf("§6.11 FAIL: expected main transcript to contain a twin summary action_result, got entries: %+v", entries)
	}

	// (e) Extract the twin id from the summary header so we can verify
	// the per-twin transcript file.
	header := summary.Content
	openIdx := strings.Index(header, "[Twin:")
	closeIdx := strings.Index(header, "]")
	if openIdx < 0 || closeIdx < 0 || closeIdx <= openIdx {
		t.Fatalf("§6.11 FAIL: could not extract twin id from summary header: %q", header)
	}
	twinID := header[openIdx+len("[Twin:"):closeIdx]
	if twinID == "" {
		t.Fatalf("§6.11 FAIL: extracted twin id is empty from header: %q", header)
	}

	// (f) The twin's own JSONL transcript file exists at
	// <sessionDir>/twins/<id>.jsonl — separate from the main
	// transcript and from any single-subagent transcripts.
	twinPath := filepath.Join(sessionDir, "twins", twinID+".jsonl")
	if _, err := os.Stat(twinPath); err != nil {
		t.Errorf("§6.11 FAIL: twin transcript not created at %q: %v", twinPath, err)
	}

	// (g) The twin's intermediate proposed_action (read_file stub.go) is
	// recorded in the twin's own transcript — proving the private
	// propose→review→execute cycle actually ran, not just skipped.
	twinTr, err := transcript.LoadFromFile(twinPath)
	if err != nil {
		t.Fatalf("§6.11 FAIL: cannot load twin transcript %q: %v", twinPath, err)
	}
	twinEntries := twinTr.Entries()
	var sawTwinReadFile bool
	for _, e := range twinEntries {
		if e.Type == transcript.TypeProposedAction && strings.Contains(e.Content, "read_file") && strings.Contains(e.Content, "stub.go") {
			sawTwinReadFile = true
			break
		}
	}
	if !sawTwinReadFile {
		t.Errorf("§6.11 FAIL: twin's own transcript should contain the read_file stub.go proposed_action; got entries: %+v", twinEntries)
	}

	// (h) The read_file stub.go proposed_action must NOT leak into the
	// main transcript — only the summary bubbles up.
	for _, e := range entries {
		if e.Type == transcript.TypeProposedAction && strings.Contains(e.Content, "read_file") && strings.Contains(e.Content, "stub.go") {
			t.Errorf("§6.11 FAIL: main transcript leaked twin's intermediate proposed_action: %s", e.Content)
		}
	}

	// (i) Only ONE twin summary landed in the main transcript (not
	// duplicated, not missing).
	twinSummaryCount := 0
	for _, e := range entries {
		if e.Type == transcript.TypeActionResult && strings.HasPrefix(e.Content, "[Twin:") {
			twinSummaryCount++
		}
	}
	if twinSummaryCount != 1 {
		t.Errorf("§6.11 FAIL: expected exactly 1 twin summary in main transcript, got %d", twinSummaryCount)
	}
}

// ---------------------------------------------------------------------------
// §6.17 — Start-of-spawn log entry appears in main transcript immediately
// on spawn, before the twin's summary
// ---------------------------------------------------------------------------

// TestTwin_StartOfSpawnLoggedImmediately is the §6.17 acceptance test.
// It drives a twin run and verifies that the main transcript contains a
// "[System]: Twin subagent started for task: ..." entry at a line index
// BEFORE the twin summary's line index. This is the minimum-viable
// visibility fix from §6.15: even before the twin pair finishes, the
// main session can see that a twin was spawned, what its task was, and
// where its transcript lives. Without this entry, a stuck or
// rate-limit-exhausted twin pair would leave the main session in the
// dark until it eventually returned or hit the turn cap.
func TestTwin_StartOfSpawnLoggedImmediately(t *testing.T) {
	workDir := t.TempDir()
	sessionDir := t.TempDir()
	sessionPath := filepath.Join(sessionDir, "session_test.jsonl")

	mc := newMockClient()

	// Coder: propose spawn_twin_subagent.
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall(
			"spawn_twin_subagent",
			`{"task":"implement a small feature with a clear summary","context":""}`,
		)},
	}})
	// Reviewer: approve.
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{
		Text: "APPROVED. Twin subagent is the right tool.",
	}})
	// Coder: after twin summary, task_complete.
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("task_complete", "{}")},
	}})
	// Reviewer: approve task_complete.
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{
		Text: "APPROVED.",
	}})

	// Twin: mini-Coder immediately calls task_complete; mini-Reviewer approves.
	mc.addResponse("__TWIN__", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("task_complete", "{}")},
	}})
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{
		Text: "APPROVED. Twin done.",
	}})

	twinMock := &twinRoutedMock{base: mc, twinKey: "__TWIN__"}

	coderCfg := agent.AgentConfig{
		Name:     "Coder", BaseURL: "http://test", Model: "test-model", HasTools: true,
	}
	reviewerCfg := agent.AgentConfig{
		Name:     "Reviewer", BaseURL: "http://test", Model: "test-model", HasTools: false,
	}

	tr := transcript.NewTranscript(sessionPath)
	l := loop.New(tr, coderCfg, reviewerCfg, twinMock, workDir)
	// Use ModeTriad directly so we don't depend on the routing logic
	// for this test — the focus is on the start-of-spawn entry, not
	// on which tier the task is classified as.
	l.CurrentMode = loop.ModeTriad

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	taskChan := make(chan string, 1)
	taskChan <- "medium task that should be routed to twin"
	close(taskChan)

	if err := l.Run(ctx, taskChan); err != nil {
		t.Fatalf("Run: %v", err)
	}

	entries := tr.Entries()

	// (a) The start-of-spawn entry must exist.
	var spawnStartLine int = -1
	for i, e := range entries {
		if e.Type == transcript.TypeMessage &&
			strings.HasPrefix(e.Content, "[System]: Twin subagent started for task:") {
			spawnStartLine = i
			break
		}
	}
	if spawnStartLine == -1 {
		t.Fatalf("§6.17 FAIL: expected a '[System]: Twin subagent started for task:' entry in main transcript, got entries: %+v", entries)
	}

	// (b) The twin summary action_result must exist.
	var summaryLine int = -1
	for i, e := range entries {
		if e.Type == transcript.TypeActionResult && strings.HasPrefix(e.Content, "[Twin:") {
			summaryLine = i
			break
		}
	}
	if summaryLine == -1 {
		t.Fatalf("§6.17 FAIL: expected a [Twin:...] summary action_result entry, got entries: %+v", entries)
	}

	// (c) The start-of-spawn entry must appear BEFORE the summary
	// — well before, ideally immediately after the approved
	// spawn_twin_subagent proposed_action and Reviewer "APPROVED"
	// entries, since §6.15 is about visibility from the moment of
	// spawn.
	if spawnStartLine >= summaryLine {
		t.Errorf("§6.17 FAIL: start-of-spawn entry (line %d) appears at or after the twin summary (line %d); should be earlier in the transcript", spawnStartLine, summaryLine)
	}

	// (d) The start-of-spawn entry must contain the task description
	// and the transcript path — both fields are part of the §6.15
	// minimum-viable visibility contract.
	spawnEntry := entries[spawnStartLine]
	if !strings.Contains(spawnEntry.Content, "implement a small feature with a clear summary") {
		t.Errorf("§6.17 FAIL: start-of-spawn entry should contain the task description, got: %q", spawnEntry.Content)
	}
	if !strings.Contains(spawnEntry.Content, "transcript:") {
		t.Errorf("§6.17 FAIL: start-of-spawn entry should contain the transcript path, got: %q", spawnEntry.Content)
	}
	if !strings.Contains(spawnEntry.Content, "twins"+string(filepath.Separator)) &&
		!strings.Contains(spawnEntry.Content, "twins/") {
		t.Errorf("§6.17 FAIL: start-of-spawn entry should reference the twins subdirectory, got: %q", spawnEntry.Content)
	}

	// (e) The twin summary itself must NOT contain the start-of-spawn
	// text — they're different entries serving different purposes.
	if strings.Contains(entries[summaryLine].Content, "Twin subagent started") {
		t.Errorf("§6.17 FAIL: twin summary entry should not contain start-of-spawn text (they are separate entries); got: %q", entries[summaryLine].Content)
	}
}
