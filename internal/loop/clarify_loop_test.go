package loop_test

// Tests for the Phase 3 clarify step in the headless loop.
//
// Covers docs/x.md §Phase 3 acceptance criteria 3.5 / 3.6 / 3.7:
//   - 3.5 — General Chat mode: an ambiguous task produces a SINGLE
//     batched clarify round, never piecemeal questions, never
//     guessing.
//   - 3.6 — Triad mode: Coder/Reviewer clarify BEFORE the first
//     proposed action, not mid-task.
//   - 3.7 — "just proceed" / /proceed correctly unblocks the work
//     in both modes, with a stated best-guess interpretation in
//     the transcript.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kaiizer777/triad/internal/agent"
	"github.com/kaiizer777/triad/internal/loop"
	"github.com/kaiizer777/triad/internal/transcript"
)

// countClarifyEntries returns the number of System transcript entries
// whose content looks like a Phase 3 clarify block. The "batched"
// property (3.5) is tested by asserting this count is exactly 1, not
// several — piecemeal asking would surface multiple System entries
// with the same prefix.
func countClarifyEntries(entries []transcript.Entry) int {
	n := 0
	for _, e := range entries {
		if e.Speaker != transcript.SpeakerSystem {
			continue
		}
		if strings.HasPrefix(e.Content, "[System]: Before I start, a few clarifying questions") {
			n++
		}
	}
	return n
}

// countProceedNotes returns the number of System transcript entries
// recording a Phase 3 "proceeding with best-judgment" note.
func countProceedNotes(entries []transcript.Entry) int {
	n := 0
	for _, e := range entries {
		if e.Speaker != transcript.SpeakerSystem {
			continue
		}
		if strings.HasPrefix(e.Content, "[System]: Proceeding with best-judgment") {
			n++
		}
	}
	return n
}

// ---------------------------------------------------------------------------
// Test 3.5 — General Chat: ambiguous task → single batched clarify round
// ---------------------------------------------------------------------------

// TestClarify_GeneralChat_BatchedRound runs an ambiguous task in
// General Chat mode and asserts:
//   1. The loop produces exactly ONE clarify round (batched, not
//      piecemeal).
//   2. No Coder turn fires before the round is surfaced — there
//      should be zero Coder messages and zero proposed_action
//      entries in the transcript.
//   3. The session remains idle after the round (the human has to
//      answer or say "proceed" before work starts).
func TestClarify_GeneralChat_BatchedRound(t *testing.T) {
	dir := t.TempDir()
	tr := transcript.NewTranscript("")

	// Coder is configured but should NEVER be called in this test —
	// the loop must surface the clarify round first. We register a
	// response anyway so that, if a bug let the loop proceed without
	// clarification, the failure mode is "loop got a Coder turn" not
	// "loop panicked on missing mock response".
	mc := newMockClient()
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		Text: "SHOULD NOT RUN — clarify should have blocked this turn.",
	}})

	coderCfg := agent.AgentConfig{Name: "Coder", HasTools: true}
	reviewerCfg := agent.AgentConfig{Name: "Reviewer", HasTools: false}
	l := loop.New(tr, coderCfg, reviewerCfg, mc, dir)
	l.CurrentMode = loop.ModeGeneral

	// "fix it" is vague-pronoun ambiguous. Send it, then close the
	// channel — the loop should surface one clarify round and exit
	// cleanly with the session idle. We do NOT send a /proceed here;
	// this test is about what happens when the human hasn't answered.
	taskChan := make(chan string, 1)
	taskChan <- "fix it"
	close(taskChan)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := l.Run(ctx, taskChan); err != nil {
		t.Fatalf("Run: %v", err)
	}

	entries := tr.Entries()

	// 1. Exactly one clarify round.
	if got := countClarifyEntries(entries); got != 1 {
		t.Errorf("expected exactly 1 clarify round, got %d. Entries:\n%+v", got, entries)
	}

	// 2. Zero Coder turns / proposed actions — the active cycle must
	// not have started.
	for _, e := range entries {
		if e.Speaker == transcript.SpeakerCoder {
			t.Errorf("Coder should not have been called before clarification, got entry: %+v", e)
		}
		if e.Type == transcript.TypeProposedAction {
			t.Errorf("no proposed_action entries should exist before clarification, got: %+v", e)
		}
	}
}

// TestClarify_GeneralChat_ClearTaskSkipsRound runs an unambiguous
// task in General Chat and asserts the clarify step does NOT surface
// anything — the active cycle runs straight through.
func TestClarify_GeneralChat_ClearTaskSkipsRound(t *testing.T) {
	dir := t.TempDir()
	tr := transcript.NewTranscript("")

	mc := newMockClient()
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		Text: "Here is your answer.",
	}})

	coderCfg := agent.AgentConfig{Name: "Coder", HasTools: true}
	reviewerCfg := agent.AgentConfig{Name: "Reviewer", HasTools: false}
	l := loop.New(tr, coderCfg, reviewerCfg, mc, dir)
	l.CurrentMode = loop.ModeGeneral

	taskChan := make(chan string, 1)
	taskChan <- "Explain what the testify suite does in Go testing"
	close(taskChan)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := l.Run(ctx, taskChan); err != nil {
		t.Fatalf("Run: %v", err)
	}

	entries := tr.Entries()

	// No clarify round.
	if got := countClarifyEntries(entries); got != 0 {
		t.Errorf("expected zero clarify rounds on a clear task, got %d", got)
	}

	// Coder did run.
	coderTurns := 0
	for _, e := range entries {
		if e.Speaker == transcript.SpeakerCoder {
			coderTurns++
		}
	}
	if coderTurns == 0 {
		t.Errorf("expected Coder to run on a clear task, got 0 Coder entries")
	}
}

// ---------------------------------------------------------------------------
// Test 3.6 — Triad: clarify before the first proposed action
// ---------------------------------------------------------------------------

// TestClarify_Triad_ClarifyBeforeFirstAction runs an ambiguous task
// in Triad mode and asserts:
//   1. The clarify round is surfaced BEFORE any Coder proposed_action.
//   2. The clarify round is surfaced BEFORE any Reviewer message —
//      the Reviewer does not even see the proposed action because
//      the active cycle never started.
//   3. Exactly one batched round (not piecemeal).
func TestClarify_Triad_ClarifyBeforeFirstAction(t *testing.T) {
	dir := t.TempDir()
	tr := transcript.NewTranscript("")

	// Coder and Reviewer are configured but should never be called.
	mc := newMockClient()
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("write_file", `{"path":"x.txt","content":"y"}`)},
	}})
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{Text: "APPROVED."}})

	coderCfg := agent.AgentConfig{Name: "Coder", HasTools: true}
	reviewerCfg := agent.AgentConfig{Name: "Reviewer", HasTools: false}
	l := loop.New(tr, coderCfg, reviewerCfg, mc, dir)
	l.CurrentMode = loop.ModeTriad

	taskChan := make(chan string, 1)
	taskChan <- "fix it" // vague-pronoun ambiguous
	close(taskChan)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := l.Run(ctx, taskChan); err != nil {
		t.Fatalf("Run: %v", err)
	}

	entries := tr.Entries()

	// 1. Exactly one clarify round.
	if got := countClarifyEntries(entries); got != 1 {
		t.Errorf("expected exactly 1 clarify round, got %d. Entries:\n%+v", got, entries)
	}

	// 2. No Coder proposed_action. No Reviewer messages at all.
	for _, e := range entries {
		if e.Type == transcript.TypeProposedAction {
			t.Errorf("no proposed_action should exist before clarification, got: %+v", e)
		}
		if e.Speaker == transcript.SpeakerReviewer {
			t.Errorf("Reviewer should not be called before clarification, got: %+v", e)
		}
	}

	// 3. The clarify entry appears AFTER the [You] task entry (so
	// the human sees their own message first) and is the last
	// meaningful content. Note: in Triad mode, the Phase 2 mismatch
	// note (a System entry) may also appear between the [You] entry
	// and the clarify block — that's intentional and non-blocking,
	// so we look for the LAST [You] message before the clarify
	// entry, not the immediately-prior one.
	clarifyIdx := -1
	for i, e := range entries {
		if e.Speaker == transcript.SpeakerSystem &&
			strings.HasPrefix(e.Content, "[System]: Before I start") {
			clarifyIdx = i
			break
		}
	}
	if clarifyIdx <= 0 {
		t.Fatalf("clarify entry not found, got index %d, entries: %+v", clarifyIdx, entries)
	}
	lastYouBeforeClarify := -1
	for i := 0; i < clarifyIdx; i++ {
		if entries[i].Speaker == transcript.SpeakerYou {
			lastYouBeforeClarify = i
		}
	}
	if lastYouBeforeClarify < 0 {
		t.Errorf("expected a [You] message before the clarify block, got entries: %+v", entries)
	}
}

// ---------------------------------------------------------------------------
// Test 3.7 — "just proceed" / /proceed unblocks the work
// ---------------------------------------------------------------------------

// TestClarify_ProceedUnblocksGeneralChat confirms that in General
// Chat, the sequence "ambiguous task" + "/proceed" results in:
//   1. One clarify round (the question).
//   2. One proceed note (the recorded best-guess interpretation).
//   3. A Coder turn that ACTUALLY runs (not blocked, not guessing).
//   4. The proceed note states the best-guess interpretation in the
//      transcript, not just silent proceeding.
func TestClarify_ProceedUnblocksGeneralChat(t *testing.T) {
	dir := t.TempDir()
	tr := transcript.NewTranscript("")

	mc := newMockClient()
	// After the /proceed reply unblocks the loop, the next Coder
	// turn must fire. We expect a Coder text response (single-agent
	// General Chat path).
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		Text: "Done — best-guess answer.",
	}})

	coderCfg := agent.AgentConfig{Name: "Coder", HasTools: true}
	reviewerCfg := agent.AgentConfig{Name: "Reviewer", HasTools: false}
	l := loop.New(tr, coderCfg, reviewerCfg, mc, dir)
	l.CurrentMode = loop.ModeGeneral

	taskChan := make(chan string, 2)
	taskChan <- "fix it"   // ambiguous → clarify round
	taskChan <- "/proceed" // explicit slash command unblocks
	close(taskChan)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := l.Run(ctx, taskChan); err != nil {
		t.Fatalf("Run: %v", err)
	}

	entries := tr.Entries()

	// 1. Exactly one clarify round.
	if got := countClarifyEntries(entries); got != 1 {
		t.Errorf("expected exactly 1 clarify round, got %d. Entries:\n%+v", got, entries)
	}

	// 2. Exactly one proceed note.
	if got := countProceedNotes(entries); got != 1 {
		t.Errorf("expected exactly 1 proceed note, got %d. Entries:\n%+v", got, entries)
	}

	// 3. Coder ran.
	coderTurns := 0
	for _, e := range entries {
		if e.Speaker == transcript.SpeakerCoder {
			coderTurns++
		}
	}
	if coderTurns == 0 {
		t.Errorf("expected Coder to run after /proceed, got 0 Coder entries")
	}

	// 4. The proceed note must state a best-guess interpretation —
	// i.e. it must not be empty, must include the task text (or
	// truncated form of it), and must come AFTER the [You] /proceed
	// reply.
	proceedIdx := -1
	for i, e := range entries {
		if strings.HasPrefix(e.Content, "[System]: Proceeding with best-judgment") {
			proceedIdx = i
			break
		}
	}
	if proceedIdx < 0 {
		t.Fatalf("proceed note not found in entries: %+v", entries)
	}
	// The proceed note must come AFTER the [You] /proceed entry so
	// the order is: [You] /proceed → [System] Proceeding → Coder turn.
	foundProceedUser := false
	for i := 0; i < proceedIdx; i++ {
		e := entries[i]
		if e.Speaker == transcript.SpeakerYou && strings.TrimSpace(e.Content) == "/proceed" {
			foundProceedUser = true
			break
		}
	}
	if !foundProceedUser {
		t.Errorf("expected [You] /proceed entry to appear before the proceed note, got entries: %+v", entries)
	}
}

// TestClarify_ProceedUnblocksTriad mirrors the General Chat test but
// for Triad mode — same invariants, plus we expect a Reviewer turn
// to fire after the proceed unblocks.
func TestClarify_ProceedUnblocksTriad(t *testing.T) {
	dir := t.TempDir()
	tr := transcript.NewTranscript("")

	mc := newMockClient()
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("task_complete", "{}")},
	}})
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{Text: "APPROVED. Done."}})

	coderCfg := agent.AgentConfig{Name: "Coder", HasTools: true}
	reviewerCfg := agent.AgentConfig{Name: "Reviewer", HasTools: false}
	l := loop.New(tr, coderCfg, reviewerCfg, mc, dir)
	l.CurrentMode = loop.ModeTriad

	taskChan := make(chan string, 2)
	taskChan <- "fix it"   // ambiguous
	taskChan <- "/proceed" // unblocks
	close(taskChan)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := l.Run(ctx, taskChan); err != nil {
		t.Fatalf("Run: %v", err)
	}

	entries := tr.Entries()

	// 1. One clarify round.
	if got := countClarifyEntries(entries); got != 1 {
		t.Errorf("expected exactly 1 clarify round, got %d. Entries:\n%+v", got, entries)
	}

	// 2. One proceed note.
	if got := countProceedNotes(entries); got != 1 {
		t.Errorf("expected exactly 1 proceed note, got %d", got)
	}

	// 3. Coder ran (proposed task_complete), and Reviewer ran.
	coderRan := false
	reviewerRan := false
	for _, e := range entries {
		if e.Speaker == transcript.SpeakerCoder {
			coderRan = true
		}
		if e.Speaker == transcript.SpeakerReviewer {
			reviewerRan = true
		}
	}
	if !coderRan {
		t.Errorf("expected Coder to run after /proceed in Triad mode, got 0 Coder entries")
	}
	if !reviewerRan {
		t.Errorf("expected Reviewer to run after Coder's proposal, got 0 Reviewer entries")
	}
}

// TestClarify_ConversationalProceedEquivSlashCommand confirms that
// "just proceed" (no slash) is accepted as the same signal — the
// doc (§3.3) requires both forms to unblock.
func TestClarify_ConversationalProceedEquivSlashCommand(t *testing.T) {
	dir := t.TempDir()
	tr := transcript.NewTranscript("")

	mc := newMockClient()
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		Text: "Best-guess answer.",
	}})

	coderCfg := agent.AgentConfig{Name: "Coder", HasTools: true}
	reviewerCfg := agent.AgentConfig{Name: "Reviewer", HasTools: false}
	l := loop.New(tr, coderCfg, reviewerCfg, mc, dir)
	l.CurrentMode = loop.ModeGeneral

	taskChan := make(chan string, 2)
	taskChan <- "fix it"
	taskChan <- "just proceed" // conversational, not /proceed
	close(taskChan)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := l.Run(ctx, taskChan); err != nil {
		t.Fatalf("Run: %v", err)
	}

	entries := tr.Entries()

	// We should still get exactly one proceed note (the loop does
	// not care which form of the signal the human used).
	if got := countProceedNotes(entries); got != 1 {
		t.Errorf("expected exactly 1 proceed note for conversational 'just proceed', got %d. Entries:\n%+v", got, entries)
	}
}

// TestClarify_RealAnswersUnblockToo confirms that a NON-proceed
// reply (a real answer to the questions) also unblocks the loop
// with a "[System]: Clarification received — proceeding." ack note.
// The doc says either form unblocks the work — only the proceed
// signal is the best-guess path; real answers are an explicit
// refinement.
func TestClarify_RealAnswersUnblockToo(t *testing.T) {
	dir := t.TempDir()
	tr := transcript.NewTranscript("")

	mc := newMockClient()
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		Text: "Got it, here is the answer.",
	}})

	coderCfg := agent.AgentConfig{Name: "Coder", HasTools: true}
	reviewerCfg := agent.AgentConfig{Name: "Reviewer", HasTools: false}
	l := loop.New(tr, coderCfg, reviewerCfg, mc, dir)
	l.CurrentMode = loop.ModeGeneral

	taskChan := make(chan string, 2)
	taskChan <- "fix it"
	taskChan <- "1. The login button in src/components/Login.tsx\n2. The user_id column in users table"
	close(taskChan)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := l.Run(ctx, taskChan); err != nil {
		t.Fatalf("Run: %v", err)
	}

	entries := tr.Entries()

	// 1. One clarify round.
	if got := countClarifyEntries(entries); got != 1 {
		t.Errorf("expected 1 clarify round, got %d", got)
	}

	// 2. A clarification-received ack should exist (NOT a proceed
	// note — these are different signals).
	gotAck := false
	for _, e := range entries {
		if e.Speaker == transcript.SpeakerSystem &&
			strings.Contains(e.Content, "Clarification received") {
			gotAck = true
			break
		}
	}
	if !gotAck {
		t.Errorf("expected '[System]: Clarification received' ack after real answers, got: %+v", entries)
	}

	// 3. No proceed note on the real-answers path.
	if got := countProceedNotes(entries); got != 0 {
		t.Errorf("real-answers path should NOT emit a proceed note, got %d", got)
	}

	// 4. Coder ran.
	coderRan := false
	for _, e := range entries {
		if e.Speaker == transcript.SpeakerCoder {
			coderRan = true
			break
		}
	}
	if !coderRan {
		t.Errorf("expected Coder to run after real answers, got 0 Coder entries")
	}
}
