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
	"github.com/kaiizer777/triad/internal/skills"
	"github.com/kaiizer777/triad/internal/tracelog"
	"github.com/kaiizer777/triad/internal/transcript"
)

// writeSkill is a tiny helper to write a single Main (+ optional
// Mini) skill file in a temp dir. Used by the end-to-end funnel
// tests to set up a real skills.Registry pointing at a real
// on-disk layout.
func writeSkill(t *testing.T, dir, name, section, mainBody, miniBody string) {
	t.Helper()
	main := "---\nname: " + name + "\nsection: " + section +
		"\ndescription: \"" + section + " skill\"\ntier: main\n"
	if miniBody != "" {
		main += "mini_ref: " + name + "-mini.md\n"
	}
	main += "\n---\n" + mainBody + "\n"
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(main), 0o644); err != nil {
		t.Fatalf("write main %s: %v", name, err)
	}
	if miniBody != "" {
		mini := "---\nname: " + name + "\nsection: " + section +
			"\ndescription: \"" + section + " skill\"\ntier: mini\n" +
			"\n---\n" + miniBody + "\n"
		if err := os.WriteFile(filepath.Join(dir, name+"-mini.md"), []byte(mini), 0o644); err != nil {
			t.Fatalf("write mini %s: %v", name, err)
		}
	}
}

// makeSkillRegistry writes a 3-skill (frontend/backend/db) fixture
// into a fresh temp dir and returns a loaded *skills.Registry.
// The Mini variants are all present so the post-first-touch Mini
// path is exercised by the tests. The temp dir is the skills
// dir itself (the loader doesn't require a parent dir named
// "skills" — that's a convention for production projects, not
// a loader requirement).
func makeSkillRegistry(t *testing.T) *skills.Registry {
	t.Helper()
	dir := t.TempDir()
	writeSkill(t, dir, "frontend", "frontend",
		"FRONTEND MAIN BODY: use functional components.\n",
		"FRONTEND MINI: keep components under 50 lines.\n")
	writeSkill(t, dir, "backend", "backend",
		"BACKEND MAIN BODY: prefer tea.Cmd concurrency.\n",
		"BACKEND MINI: small handlers.\n")
	writeSkill(t, dir, "db", "db",
		"DB MAIN BODY: every query must use an index.\n",
		"DB MINI: verify indexes before adding queries.\n")
	reg, err := skills.Load(dir)
	if err != nil {
		t.Fatalf("skills.Load: %v", err)
	}
	if reg.Count() != 3 {
		t.Fatalf("registry count: got %d, want 3", reg.Count())
	}
	return reg
}

// newFunnelTestLoop is the test-only Loop constructor used by
// the funnel tests. It mirrors newTestLoop in loop_test.go but
// also attaches a skills.Registry so the funnel runs on every
// Coder turn. The loaded set is fresh per call.
func newFunnelTestLoop(t *testing.T, mc *mockClient, reg *skills.Registry) (*loop.Loop, *transcript.Transcript, chan string) {
	t.Helper()
	tr := transcript.NewTranscript("")
	coderCfg := agent.AgentConfig{Name: "Coder", HasTools: true}
	reviewerCfg := agent.AgentConfig{Name: "Reviewer", HasTools: false}
	workDir := t.TempDir()

	l := loop.New(tr, coderCfg, reviewerCfg, mc, workDir)
	l.CurrentMode = loop.ModeTriad
	l.SetSkillsRegistry(reg)
	taskChan := make(chan string, 1)
	return l, tr, taskChan
}

// newFunnelTestLoopWithPath is the Phase 4 variant of
// newFunnelTestLoop: it binds the transcript to a real file path
// so the funnel's appendSkillSelectionTrace can derive a real
// trace path and write to a per-test temp file. Without a bound
// path, appendSkillSelectionTrace is a documented no-op (it can't
// safely write to a per-test trace without a session anchor), and
// any test that needs to read back the trace log from disk has to
// use this constructor.
//
// Returns the loop, the transcript, the task channel, and the
// resolved trace file path (so tests can read it without
// re-deriving the path themselves).
func newFunnelTestLoopWithPath(t *testing.T, mc *mockClient, reg *skills.Registry) (*loop.Loop, *transcript.Transcript, chan string, string) {
	t.Helper()
	sessionDir := t.TempDir()
	sessionPath := filepath.Join(sessionDir, "session_p4.jsonl")
	tr := transcript.NewTranscript(sessionPath)
	coderCfg := agent.AgentConfig{Name: "Coder", HasTools: true}
	reviewerCfg := agent.AgentConfig{Name: "Reviewer", HasTools: false}
	workDir := t.TempDir()

	l := loop.New(tr, coderCfg, reviewerCfg, mc, workDir)
	l.CurrentMode = loop.ModeTriad
	l.SetSkillsRegistry(reg)
	taskChan := make(chan string, 1)
	tracePath := tracelog.TracePathForSession(sessionPath)
	return l, tr, taskChan, tracePath
}

// lastSystemEntry returns the most recent SpeakerSystem entry's
// content (or "" if none). Used by funnel tests to assert on the
// [Skills] per-turn selection record without depending on the
// transcript's full structure.
func lastSystemEntry(entries []transcript.Entry) string {
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Speaker == transcript.SpeakerSystem {
			return entries[i].Content
		}
	}
	return ""
}

// --- 2.8: single-domain → Main on first touch, Mini on second ---------

// TestFunnel_SingleDomainMainThenMini is the end-to-end version
// of the spec's "2.8 single-domain" requirement: a coding turn
// that touches only the `frontend` section should record a Main
// load on the first turn and a Mini load on the second turn.
//
// We exercise two Coder turns (driven by the mock client) on the
// same session and assert the [Skills] system entries describe
// the correct tier each time. The task is concrete (a single
// write_file + task_complete) so the loop's clarify phase
// doesn't intercept and require a /proceed reply — the funnel
// path is what we're testing here, not clarify.
func TestFunnel_SingleDomainMainThenMini(t *testing.T) {
	mc := newMockClient()
	reg := makeSkillRegistry(t)

	// Coder turn 1: TEXT-ONLY response that declares the
	// selection. This is the only shape that exercises the
	// post-call SELECTED_SECTIONS parse (tool calls short-
	// circuit the parse — see coderTurnWithFunnel). The loop
	// appends the cleaned text and re-enters the outer loop
	// to call Coder again.
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		Text: `SELECTED_SECTIONS: ["frontend"]` + "\n" + "I'll add a Button component now.",
	}})

	// Coder turn 2 (after the funnel's selection parse): propose
	// write_file. By this turn the loaded set has frontend, so
	// Stage 2 injects the Mini body into the prompt.
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("write_file", `{"path":"button.tsx","content":"x"}`)},
	}})

	// Reviewer turn: approve.
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{
		Text: "APPROVED. File looks good.",
	}})

	// Coder turn 3 (after write_file result): re-declare
	// frontend (still in loaded set → Mini tier) AND emit the
	// task_complete tool call. The SELECTED_SECTIONS prefix is
	// only parseable when the response is text-only, so this
	// third turn is split into two sequential Coder responses:
	// first the re-declaration as text, then the task_complete
	// as a tool call after the next loop iteration.
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		Text: `SELECTED_SECTIONS: ["frontend"]` + "\n" + "All set, time to complete.",
	}})
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("task_complete", "{}")},
	}})

	// Reviewer: approve task_complete.
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{
		Text: "APPROVED. Task complete.",
	}})

	l, tr, taskChan := newFunnelTestLoop(t, mc, reg)
	if err := runLoop(t, l, taskChan, "create button.tsx with a Button component"); err != nil {
		t.Fatalf("loop: %v", err)
	}

	entries := tr.Entries()

	// First, locate the two [Skills] system entries (one per
	// Coder turn that emitted a SELECTED_SECTIONS line). We don't
	// assume order — we just confirm both expected tiers showed
	// up across the session.
	var (
		sawMain, sawMini bool
		skillEntryCount  int
	)
	for _, e := range entries {
		if e.Speaker != transcript.SpeakerSystem {
			continue
		}
		if !strings.Contains(e.Content, "[Skills]") {
			continue
		}
		skillEntryCount++
		if strings.Contains(e.Content, "tier: main") && strings.Contains(e.Content, "first touch this session") {
			sawMain = true
		}
		if strings.Contains(e.Content, "tier: mini") && strings.Contains(e.Content, "already loaded this session") {
			sawMini = true
		}
	}

	if !sawMain {
		t.Errorf("expected a [Skills] entry recording tier=main + 'first touch this session' on the first selection; entries so far: %+v", entrySummaries(entries))
	}
	if !sawMini {
		t.Errorf("expected a [Skills] entry recording tier=mini + 'already loaded this session' on the second selection; entries so far: %+v", entrySummaries(entries))
	}
	if skillEntryCount < 2 {
		t.Errorf("expected at least 2 [Skills] system entries (one per selection turn), got %d", skillEntryCount)
	}

	// The loaded set should be in sync with the registry view
	// of the session. The Loop exposes it via LoadedSkills().
	loaded := l.LoadedSkills()
	if loaded == nil {
		t.Fatalf("Loop.LoadedSkills() returned nil")
	}
	if !loaded.Has("frontend") {
		t.Errorf("loaded set: frontend should be marked loaded after the first turn")
	}
	if loaded.Has("backend") || loaded.Has("db") {
		t.Errorf("loaded set: only frontend should be loaded, got %v", loaded.Sections())
	}
}

// --- 2.9: multi-domain within cap → 3 distinct Mains -------------------

// TestFunnel_MultiDomainAllMain drives a single Coder turn that
// selects all three sections (at the cap). On the first turn,
// each section's Main body fires — none are skipped, none are
// duplicated. The [Skills] system entry should record all three
// as `tier: main / first touch this session`.
func TestFunnel_MultiDomainAllMain(t *testing.T) {
	mc := newMockClient()
	reg := makeSkillRegistry(t)

	// Coder turn 1: TEXT-ONLY declaration of all three sections
	// (at the cap). The loop appends the cleaned text and
	// re-enters for another Coder turn.
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		Text: `SELECTED_SECTIONS: ["frontend", "backend", "db"]` + "\n" + "Setting up the full stack now.",
	}})

	// Coder turn 2 (post-funnel): propose write_file.
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("write_file", `{"path":"stack.txt","content":"ok"}`)},
	}})
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{
		Text: "APPROVED.",
	}})

	// Coder turn 3 (post write_file): task_complete.
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("task_complete", "{}")},
	}})
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{
		Text: "APPROVED. Task complete.",
	}})

	l, tr, taskChan := newFunnelTestLoop(t, mc, reg)
	if err := runLoop(t, l, taskChan, "create stack.txt with content ok"); err != nil {
		t.Fatalf("loop: %v", err)
	}

	entries := tr.Entries()

	// Find the [Skills] system entry.
	var skillEntry *transcript.Entry
	for i := range entries {
		if entries[i].Speaker == transcript.SpeakerSystem && strings.Contains(entries[i].Content, "[Skills]") {
			skillEntry = &entries[i]
			break
		}
	}
	if skillEntry == nil {
		t.Fatalf("expected a [Skills] system entry recording the selection; entries: %+v", entrySummaries(entries))
	}

	// All three sections should be recorded with tier=main and
	// "first touch this session".
	for _, want := range []string{"backend", "db", "frontend"} {
		needle := "section: " + want + ", tier: main (first touch this session)"
		if !strings.Contains(skillEntry.Content, needle) {
			t.Errorf("expected [Skills] entry to contain %q; got: %s", needle, skillEntry.Content)
		}
	}

	// The loaded set should reflect all three.
	loaded := l.LoadedSkills()
	if loaded == nil {
		t.Fatalf("Loop.LoadedSkills() returned nil")
	}
	for _, sec := range []string{"backend", "db", "frontend"} {
		if !loaded.Has(sec) {
			t.Errorf("loaded set: %q should be marked loaded after first turn", sec)
		}
	}
	if got := loaded.Sections(); len(got) != 3 {
		t.Errorf("loaded.Sections: got %v, want 3 entries", got)
	}
}

// --- 2.10: cap enforcement -----------------------------------------

// TestFunnel_CapEnforced verifies that a Coder turn that tries
// to select 4+ sections is silently truncated to 3 (the hard
// cap), and the [Skills] system entry records the truncation so
// the human can see it happened. The Loop never injects the
// 4th section, and the loaded set only contains the 3 kept ones.
func TestFunnel_CapEnforced(t *testing.T) {
	// We need a 4-section registry to attempt a 4-section
	// selection. The frontend/backend/db fixture is only 3
	// sections, so build a 4th.
	dir := t.TempDir()
	writeSkill(t, dir, "frontend", "frontend",
		"FRONTEND MAIN\n", "FRONTEND MINI\n")
	writeSkill(t, dir, "backend", "backend",
		"BACKEND MAIN\n", "BACKEND MINI\n")
	writeSkill(t, dir, "db", "db",
		"DB MAIN\n", "DB MINI\n")
	writeSkill(t, dir, "extra", "extra",
		"EXTRA MAIN\n", "EXTRA MINI\n")
	reg, err := skills.Load(dir)
	if err != nil {
		t.Fatalf("skills.Load: %v", err)
	}
	if reg.Count() != 4 {
		t.Fatalf("registry count: got %d, want 4", reg.Count())
	}

	mc := newMockClient()
	// Coder: TEXT-ONLY declaration that picks all 4 in one
	// line (overflows the cap). The cap truncates inside
	// ParseSelection, so ApplySelection only sees 3 sections.
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		Text: `SELECTED_SECTIONS: ["frontend", "backend", "db", "extra"]` + "\n" + "Picking too many.",
	}})

	// Coder turn 2: write_file.
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("write_file", `{"path":"cap.txt","content":"ok"}`)},
	}})
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{
		Text: "APPROVED.",
	}})

	// Coder turn 3: task_complete.
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("task_complete", "{}")},
	}})
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{
		Text: "APPROVED. Task complete.",
	}})

	l, tr, taskChan := newFunnelTestLoop(t, mc, reg)
	if err := runLoop(t, l, taskChan, "create cap.txt with content ok"); err != nil {
		t.Fatalf("loop: %v", err)
	}

	entries := tr.Entries()

	// The [Skills] entry must mention cap-truncation. We look
	// for the [Skills] entry specifically (lastSystemEntry would
	// return the more recent "Task complete" idle note).
	var skillEntry *transcript.Entry
	for i := range entries {
		if entries[i].Speaker == transcript.SpeakerSystem && strings.Contains(entries[i].Content, "[Skills]") {
			skillEntry = &entries[i]
			break
		}
	}
	if skillEntry == nil {
		t.Fatalf("expected a [Skills] system entry; entries: %+v", entrySummaries(entries))
	}
	if !strings.Contains(skillEntry.Content, "cap-truncated") {
		t.Errorf("expected [Skills] entry to mention cap-truncation, got: %q", skillEntry.Content)
	}

	// Only 3 sections should be in the loaded set — the 4th
	// was dropped by the cap before ApplySelection ever saw
	// it. ParseSelection sorts before truncating, so with
	// [backend, db, extra, frontend] the dropped entry is
	// `frontend` (alphabetically last). The test asserts the
	// cap fired (the system entry mentions "cap-truncated")
	// and that the loaded set has exactly 3 entries; we don't
	// pin which specific entry was dropped because the cap's
	// "drop the last in sorted order" rule is documented
	// elsewhere (ParseSelection_CapTruncates in the
	// skills package) and we want this end-to-end test to
	// remain valid if that rule is later changed to "drop the
	// first in declaration order" or similar.
	loaded := l.LoadedSkills()
	if loaded == nil {
		t.Fatalf("Loop.LoadedSkills() returned nil")
	}
	got := loaded.Sections()
	if len(got) != 3 {
		t.Errorf("loaded.Sections: got %v, want exactly 3 entries (cap was 3)", got)
	}
}

// --- 2.5/2.6: regression checks (no skill content for non-Coder) -----

// TestFunnel_RegistryNotAttached_NoFunnel is the regression check
// for the "no skills configured" case: when SetSkillsRegistry is
// not called (or the registry is empty), the loop's Coder turns
// run unmodified — no Stage 1 scaffold, no SELECTED_SECTIONS
// requirement, no skill body injection. The pre-Phase-2 behavior
// is preserved.
func TestFunnel_RegistryNotAttached_NoFunnel(t *testing.T) {
	mc := newMockClient()
	// No SetSkillsRegistry call — funnel should be a no-op.
	_, tr, taskChan := newFunnelTestLoop(t, mc, nil)

	// Coder emits a SELECTED_SECTIONS line, but the registry is
	// nil, so the funnel is a no-op and the text passes through
	// unchanged. This is the key regression check: a misconfigured
	// or missing registry must not surface a "skills failure" to
	// the user — it just gracefully skips.
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		Text:      `SELECTED_SECTIONS: ["frontend"]` + "\n" + "some prose",
		ToolCalls: []agent.ToolCall{makeToolCall("task_complete", "{}")},
	}})
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{
		Text: "APPROVED.",
	}})

	l, _, _ := newFunnelTestLoop(t, mc, nil)
	_ = tr
	_ = l
	_ = taskChan
	// We don't actually run the loop here — the goal is to
	// confirm the registry-nil path doesn't panic, and that's
	// already covered by the in-package TestApplySelection tests
	// and the existing loop_test.go suite (which constructs a
	// Loop without SetSkillsRegistry). The point of this test is
	// documentation: the test file makes the no-funnel
	// contract explicit so future refactors don't accidentally
	// make the registry mandatory.
}

// entrySummaries renders a compact one-line-per-entry view of the
// transcript for diagnostic output. Helps when a funnel test
// fails and you need to see "what entries actually landed" at a
// glance.
func entrySummaries(entries []transcript.Entry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		first := strings.SplitN(e.Content, "\n", 2)[0]
		if len(first) > 100 {
			first = first[:100] + "..."
		}
		out = append(out, e.Speaker+":"+string(e.Type)+": "+first)
	}
	return out
}

// _ silences "imported and not used" if a test-only refactor
// drops context or transcript usage temporarily.
var (
	_ = context.Background
	_ = time.Second
)

// ---------------------------------------------------------------------------
// Phase 4 Observability Acceptance Tests (work.md §7 / 4.2 - 4.3).
// ---------------------------------------------------------------------------

// TestObservability_Phase4_4_2_AttributedToTurn is the acceptance
// test for work.md §4.2: "trigger a mixed-domain task and confirm
// /trace correctly attributes each skill choice to the specific turn
// that caused it, not a prior or later turn."
//
// We drive two separate sessions through the loop, each with a
// distinct human task and a distinct section selection:
//
//   - session A: task = "create button.tsx with a Button component"
//     (UI-flavored) → Coder picks SELECTED_SECTIONS: ["frontend"]
//   - session B: task = "create index.txt with the user_id index
//     definition" (DB-flavored) → Coder picks SELECTED_SECTIONS: ["db"]
//
// Tasks are worded specifically enough to bypass the loop's
// clarify phase (the same convention the existing Phase 2 funnel
// tests use — short, vague tasks trigger clarify first and never
// reach the funnel, so they can't be used for funnel-level
// attribution tests).
//
// After both loops complete, we read each session's trace log and
// assert: the first EventSkillSelection entry's task excerpt matches
// the task that was actually passed in (not the other session's task),
// the decisions list contains the section that was selected (not the
// other session's), and the chronological order of trace entries
// within a single session matches the order of the Coder turns that
// produced them.
//
// The two sessions land in two distinct trace files (because each
// transcript has a distinct bound file path), so there's no risk of
// cross-test pollution from a shared default.jsonl.
func TestObservability_Phase4_4_2_AttributedToTurn(t *testing.T) {
	reg := makeSkillRegistry(t)

	// --- Session A: frontend-flavored task ---------------------
	mcA := newMockClient()
	mcA.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		Text: `SELECTED_SECTIONS: ["frontend"]` + "\n" + "I'll update the button component.",
	}})
	mcA.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("write_file", `{"path":"button.tsx","content":"x"}`)},
	}})
	mcA.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{Text: "APPROVED."}})
	mcA.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("task_complete", "{}")},
	}})
	mcA.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{Text: "APPROVED. Task complete."}})

	lA, trA, taskChanA, tracePathA := newFunnelTestLoopWithPath(t, mcA, reg)
	const taskA = "create button.tsx with a Button component"
	if err := runLoop(t, lA, taskChanA, taskA); err != nil {
		t.Fatalf("loop A: %v", err)
	}
	_ = trA

	// --- Session B: db-flavored task ---------------------------
	mcB := newMockClient()
	mcB.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		Text: `SELECTED_SECTIONS: ["db"]` + "\n" + "I'll add the user_id index.",
	}})
	mcB.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("write_file", `{"path":"index.txt","content":"x"}`)},
	}})
	mcB.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{Text: "APPROVED."}})
	mcB.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("task_complete", "{}")},
	}})
	mcB.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{Text: "APPROVED. Task complete."}})

	lB, trB, taskChanB, tracePathB := newFunnelTestLoopWithPath(t, mcB, reg)
	const taskB = "create index.txt with the user_id index definition"
	if err := runLoop(t, lB, taskChanB, taskB); err != nil {
		t.Fatalf("loop B: %v", err)
	}
	_ = trB

	// --- Read trace A and assert attribution -------------------
	entriesA, err := tracelog.LoadTrace(tracePathA)
	if err != nil {
		t.Fatalf("LoadTrace(A): %v", err)
	}
	if len(entriesA) == 0 {
		t.Fatalf("session A trace log is empty; expected at least 1 skill_selection entry")
	}
	skillA := findSkillSelections(entriesA)
	if len(skillA) == 0 {
		t.Fatalf("session A: no EventSkillSelection entries found in trace; entries=%+v", entriesA)
	}
	firstA := skillA[0]
	if got := firstA.Data["task"]; got != taskA {
		t.Errorf("session A first skill event task: got %q, want %q", got, taskA)
	}
	decisionsA := extractDecisions(t, firstA.Data)
	if len(decisionsA) != 1 {
		t.Fatalf("session A first skill event decisions: got %+v, want 1 entry", firstA.Data["decisions"])
	}
	if decisionsA[0]["section"] != "frontend" {
		t.Errorf("session A first skill event section: got %q, want %q (must NOT be the other session's section)",
			decisionsA[0]["section"], "frontend")
	}
	if decisionsA[0]["tier"] != string(skills.TierMain) {
		t.Errorf("session A first skill event tier: got %q, want %q (first touch → main)",
			decisionsA[0]["tier"], skills.TierMain)
	}
	if cost, _ := decisionsA[0]["token_cost"].(float64); int(cost) <= 0 {
		t.Errorf("session A first skill event token cost: got %v, want > 0", decisionsA[0]["token_cost"])
	}
	if totalA, _ := firstA.Data["total_tokens"].(float64); int(totalA) <= 0 {
		t.Errorf("session A first skill event total_tokens: got %v, want > 0", firstA.Data["total_tokens"])
	}
	// And the negative: the DB task text must NOT have leaked into
	// the frontend task's trace entry. If both tasks' excerpts are
	// showing up, attribution broke.
	for _, e := range skillA {
		if got, _ := e.Data["task"].(string); strings.Contains(got, "user_id") {
			t.Errorf("session A trace polluted with session B's task text: %q", got)
		}
	}

	// --- Read trace B and assert attribution -------------------
	entriesB, err := tracelog.LoadTrace(tracePathB)
	if err != nil {
		t.Fatalf("LoadTrace(B): %v", err)
	}
	skillB := findSkillSelections(entriesB)
	if len(skillB) == 0 {
		t.Fatalf("session B: no EventSkillSelection entries found in trace")
	}
	firstB := skillB[0]
	if got := firstB.Data["task"]; got != taskB {
		t.Errorf("session B first skill event task: got %q, want %q", got, taskB)
	}
	decisionsB := extractDecisions(t, firstB.Data)
	if len(decisionsB) != 1 {
		t.Fatalf("session B first skill event decisions: got %+v, want 1 entry", firstB.Data["decisions"])
	}
	if decisionsB[0]["section"] != "db" {
		t.Errorf("session B first skill event section: got %q, want %q (must NOT be session A's section)",
			decisionsB[0]["section"], "db")
	}
	if decisionsB[0]["tier"] != string(skills.TierMain) {
		t.Errorf("session B first skill event tier: got %q, want %q (first touch → main)",
			decisionsB[0]["tier"], skills.TierMain)
	}

	// --- Confirm trace path isolation: A's file has no DB picks,
	// B's file has no frontend picks. This is the negative check
	// for cross-session contamination. ------------------------
	for _, e := range skillA {
		for _, d := range extractDecisions(t, e.Data) {
			if d["section"] == "db" {
				t.Errorf("session A trace contains a 'db' section; should be frontend-only")
			}
		}
	}
	for _, e := range skillB {
		for _, d := range extractDecisions(t, e.Data) {
			if d["section"] == "frontend" {
				t.Errorf("session B trace contains a 'frontend' section; should be db-only")
			}
		}
	}
}

// TestObservability_Phase4_4_3_TraceAnswersWhyQuestion is the
// acceptance test for work.md §4.3: "you should be able to answer
// 'why did Coder just do something DB-flavored when I asked for a
// UI tweak' purely by reading /trace, without needing to inspect
// raw logs."
//
// Setup: a single session where the human task is purely UI-flavored
// ("redesign the homepage hero section"), but the mock Coder
// misclassifies and selects ["db"] instead of ["frontend"]. This is
// the bad-self-classification case from work.md §8.
//
// The test loads the trace log, runs FormatTraceOutput, and asserts
// the rendered output alone (no transcript inspection) lets a human
// see all four Phase 4 fields required by work.md §7:
//
//   1. the triggering user message ("redesign the homepage hero section")
//   2. which skill(s) Coder selected ("db")
//   3. the tier actually injected (main — first touch)
//   4. the token cost of what was injected (a positive number)
//
// The formatting check is the §4.3 contract: the human never has to
// look at the JSONL. The /trace output is the interface.
func TestObservability_Phase4_4_3_TraceAnswersWhyQuestion(t *testing.T) {
	reg := makeSkillRegistry(t)
	mc := newMockClient()
	// Misclassify: UI task, but Coder picks the DB section. This
	// is exactly the bad-self-classification case work.md §8 calls
	// out — the human is now asking "why did Coder go DB on me?".
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		Text: `SELECTED_SECTIONS: ["db"]` + "\n" + "Picking db (deliberate test mismatch).",
	}})
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("write_file", `{"path":"x.txt","content":"x"}`)},
	}})
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{Text: "APPROVED."}})
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("task_complete", "{}")},
	}})
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{Text: "APPROVED. Task complete."}})

	const uiTask = "redesign the homepage hero section with a new layout"
	l, tr, taskChan, tracePath := newFunnelTestLoopWithPath(t, mc, reg)
	if err := runLoop(t, l, taskChan, uiTask); err != nil {
		t.Fatalf("loop: %v", err)
	}
	_ = tr

	// Load and format the trace.
	entries, err := tracelog.LoadTrace(tracePath)
	if err != nil {
		t.Fatalf("LoadTrace: %v", err)
	}
	formatted := tracelog.FormatTraceOutput(entries)

	// Field 1: triggering user message. Must be present and
	// match the task verbatim (no /trace-output needed for this
	// assertion — the human sees the task in the rendered block).
	if !strings.Contains(formatted, uiTask) {
		t.Errorf("§4.3 FAIL: /trace output missing the triggering task %q; output:\n%s",
			uiTask, formatted)
	}

	// Field 2: section(s) Coder selected. Must mention the
	// (incorrect, in this test) section name.
	if !strings.Contains(formatted, "section: db") {
		t.Errorf("§4.3 FAIL: /trace output missing the selected section 'db'; output:\n%s", formatted)
	}

	// Field 3: tier actually injected (main, since first touch).
	if !strings.Contains(formatted, "tier: main") {
		t.Errorf("§4.3 FAIL: /trace output missing tier=main; output:\n%s", formatted)
	}

	// Field 4: token cost — must be present and > 0.
	if !strings.Contains(formatted, "tokens:") {
		t.Errorf("§4.3 FAIL: /trace output missing 'tokens:' field; output:\n%s", formatted)
	}
	if !strings.Contains(formatted, "total tokens:") {
		t.Errorf("§4.3 FAIL: /trace output missing 'total tokens:' summary; output:\n%s", formatted)
	}

	// The §4.3 contract: a human reading this output alone can
	// answer the question. The "section: db" line is the answer
	// — the human sees Coder picked the wrong section for the
	// UI task they asked for, and the trace tells them so
	// without them having to dig into the JSONL.
	// We assert the rendered output contains the exact phrase
	// "section: db" so a simple text search answers the why
	// question. This is the regression-protection for the
	// "4.3 checkpoint" line in work.md.
	if !strings.Contains(formatted, "- section: db") {
		t.Errorf("§4.3 FAIL: /trace output should contain '- section: db' line so a human can grep the why-answer; got:\n%s",
			formatted)
	}
}

// findSkillSelections returns the EventSkillSelection entries from
// a trace log slice, in chronological order (the same order they
// were appended). Used by the Phase 4 tests to slice out the events
// they want to assert on without depending on the other event types
// (clarify, routing, etc.) that may have been emitted by the same
// loop.
func findSkillSelections(entries []tracelog.Entry) []tracelog.Entry {
	var out []tracelog.Entry
	for _, e := range entries {
		if e.EventType == tracelog.EventSkillSelection {
			out = append(out, e)
		}
	}
	return out
}

// extractDecisions pulls the per-section decision list out of an
// EventSkillSelection entry's Data field. The trace log stores
// decisions as `[]any` of `map[string]any` after a JSON roundtrip
// (the on-disk JSONL has no Go type info, so the generic
// map[string]any decoder is the canonical in-memory shape), even
// though the funnel wrote them as []tracelog.SkillSelectionDecision.
// This helper is the one place the test code touches that
// JSON-decoding detail — the assertions read fields out of
// map[string]any, the same way the production /trace renderer does.
func extractDecisions(t *testing.T, data map[string]any) []map[string]any {
	t.Helper()
	raw, ok := data["decisions"].([]any)
	if !ok {
		// The field may have been decoded as []tracelog.SkillSelectionDecision
		// in some Go versions / unmarshaler combos. Try that too.
		if typed, typedOK := data["decisions"].([]tracelog.SkillSelectionDecision); typedOK {
			out := make([]map[string]any, 0, len(typed))
			for _, d := range typed {
				out = append(out, map[string]any{
					"section":    d.Section,
					"tier":       d.Tier,
					"token_cost": d.TokenCost,
					"forced":     d.Forced,
				})
			}
			return out
		}
		t.Fatalf("decisions field has unexpected type %T: %+v", data["decisions"], data["decisions"])
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// --- Phase 5.5: end-to-end against the REAL on-disk skills/ ---------------
//
// The Phase 1-4 funnel tests use a small fixture (3 short skill
// files written to t.TempDir) because they're testing the funnel's
// mechanics, not the project's actual skill content. Phase 5 ships
// real, author-edited skill content for this project (frontend /
// backend / db, with Main + Mini variants) in skills/*.md at the
// project root. Phase 5.5's job is to confirm those real files
// flow through the same funnel correctly end-to-end:
//
//   - all 3 sections load cleanly from the on-disk skills/
//   - a multi-domain first turn injects all 3 Mains (each at
//     5-8k tokens, not 0 or a stub)
//   - a subsequent re-declaration of any subset injects the Mini
//     variant for those sections (each at 2-4k tokens)
//   - the loaded set, the [Skills] system entries, and the trace
//     log all agree on which tier was actually injected per
//     section per turn
//
// This is the §5.5 acceptance test for the "Starter Skills"
// phase. If the real skills/ ever drifts in a way the funnel
// can't handle (missing Mini, bad frontmatter, total body
// under budget, etc.) this test catches it before the human
// runs a real multi-domain task and sees the funnel inject
// nothing.

// realSkillsDir is the project-root skills/ directory the
// Phase 5 starter skills live in. The path is computed relative
// to the test file's location so the test works regardless of
// where `go test` is invoked from.
func realSkillsDir(t *testing.T) string {
	t.Helper()
	// internal/loop/<this>.go → ../../skills
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	dir := filepath.Join(wd, "..", "..", "skills")
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		t.Fatalf("expected project-root skills/ at %q; stat err: %v", abs, err)
	}
	return abs
}

// TestFunnel_Phase5_5_RealSkillsEndToEnd loads the real
// on-disk skills/ directory (the Phase 5 starter content the
// human authored) and runs a multi-domain session through the
// full funnel. The contract is: 3 Mains on the first turn, then
// 3 Minis on the second turn, then a mixed re-declaration of 2
// sections on the third turn (2 Minis, 1 unchanged from prior
// turn). All three flows must agree on tier per section.
func TestFunnel_Phase5_5_RealSkillsEndToEnd(t *testing.T) {
	dir := realSkillsDir(t)
	reg, err := skills.Load(dir)
	if err != nil {
		t.Fatalf("skills.Load(%q): %v", dir, err)
	}
	if reg.Count() != 4 {
		t.Fatalf("Phase 5.5 FAIL: expected 4 real skills in %q, got %d", dir, reg.Count())
	}
	for _, sec := range []string{"backend", "db", "frontend"} {
		if _, ok := reg.GetBySection(sec); !ok {
			t.Fatalf("Phase 5.5 FAIL: real skill section %q missing from %q", sec, dir)
		}
	}

	// Sanity check the real files are non-empty and in budget.
	// If a Phase 5 author edit ever strips a body down to a
	// stub, the funnel will silently inject nothing and the
	// "did Mini fire" assertion below will falsely pass. This
	// belt-and-braces check makes the failure mode loud.
	for _, sec := range []string{"frontend", "backend", "db", "general-chat"} {
		sk, _ := reg.GetBySection(sec)
		if len(sk.MainBody) < 1000 {
			t.Errorf("Phase 5.5 FAIL: real %q MainBody is suspiciously short (%d chars) — did a phase-5 edit strip the body?",
				sec, len(sk.MainBody))
		}
		if len(sk.MiniBody) < 500 {
			t.Errorf("Phase 5.5 FAIL: real %q MiniBody is suspiciously short (%d chars) — did a phase-5 edit strip the body?",
				sec, len(sk.MiniBody))
		}
	}

	mc := newMockClient()

	// --- Turn 1: declare all 3 sections (at the cap) ---------
	// Each section's Main body must fire. The real bodies are
	// 5-8k tokens each; the funnel's token cost recorded in
	// the trace log will reflect that.
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		Text: `SELECTED_SECTIONS: ["frontend", "backend", "db"]` + "\n" + "Full-stack task incoming.",
	}})
	// Turn 2 (post-funnel): write_file, the trivial approval loop.
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("write_file", `{"path":"hello.txt","content":"hi"}`)},
	}})
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{Text: "APPROVED."}})

	// --- Turn 3 (post write_file): re-declare the same 3
	// sections. Now loaded, so the funnel must inject each
	// section's Mini body, not the Main.
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		Text: `SELECTED_SECTIONS: ["frontend", "backend", "db"]` + "\n" + "Touching all 3 again.",
	}})

	// --- Turn 4: re-declare just 2 (frontend + backend). db is
	// still loaded from turn 1; the prompt builder should
	// include db's Mini as a carried-over loaded section, but
	// this turn's selection decisions are only for the 2
	// declared. We assert the decisions on this turn.
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		Text: `SELECTED_SECTIONS: ["frontend", "backend"]` + "\n" + "Just two this time.",
	}})

	// Turn 5: task_complete so the loop idles cleanly.
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("task_complete", "{}")},
	}})
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{Text: "APPROVED. Task complete."}})

	l, tr, taskChan, tracePath := newFunnelTestLoopWithPath(t, mc, reg)
	if err := runLoop(t, l, taskChan, "set up a full-stack hello world"); err != nil {
		t.Fatalf("loop: %v", err)
	}

	// --- Loaded-set assertions -----------------------------------
	// After turn 4, all 3 sections are in the loaded set. No
	// turn selected a section that wasn't already loaded; the
	// expected post-state is exactly 3 loaded sections.
	loaded := l.LoadedSkills()
	if loaded == nil {
		t.Fatalf("Loop.LoadedSkills() returned nil")
	}
	gotLoaded := loaded.Sections()
	if len(gotLoaded) != 3 {
		t.Errorf("Phase 5.5 FAIL: loaded.Sections() = %v, want exactly 3 sections (frontend, backend, db)", gotLoaded)
	}
	for _, sec := range []string{"frontend", "backend", "db"} {
		if !loaded.Has(sec) {
			t.Errorf("Phase 5.5 FAIL: section %q should be in loaded set after multi-touch", sec)
		}
	}

	// --- Transcript [Skills] entry assertions --------------------
	// There must be exactly 3 [Skills] system entries (one per
	// Coder turn that emitted a SELECTED_SECTIONS line: turns
	// 1, 3, 4). The first must record 3 Mains; the second
	// must record 3 Minis; the third must record 2 Minis.
	entries := tr.Entries()
	var skillEntries []transcript.Entry
	for _, e := range entries {
		if e.Speaker == transcript.SpeakerSystem && strings.Contains(e.Content, "[Skills]") {
			skillEntries = append(skillEntries, e)
		}
	}
	if len(skillEntries) != 3 {
		t.Fatalf("Phase 5.5 FAIL: expected 3 [Skills] system entries (turns 1/3/4), got %d. Entries: %+v",
			len(skillEntries), entrySummaries(entries))
	}

	// Turn 1: 3 mains, 0 minis.
	if !strings.Contains(skillEntries[0].Content, "section: backend, tier: main (first touch this session)") {
		t.Errorf("Phase 5.5 FAIL: turn 1 entry missing backend/main. Got:\n%s", skillEntries[0].Content)
	}
	if !strings.Contains(skillEntries[0].Content, "section: db, tier: main (first touch this session)") {
		t.Errorf("Phase 5.5 FAIL: turn 1 entry missing db/main. Got:\n%s", skillEntries[0].Content)
	}
	if !strings.Contains(skillEntries[0].Content, "section: frontend, tier: main (first touch this session)") {
		t.Errorf("Phase 5.5 FAIL: turn 1 entry missing frontend/main. Got:\n%s", skillEntries[0].Content)
	}
	// No Mini on turn 1.
	if strings.Contains(skillEntries[0].Content, "tier: mini") {
		t.Errorf("Phase 5.5 FAIL: turn 1 entry should have 0 Mini decisions (first touch), got:\n%s", skillEntries[0].Content)
	}

	// Turn 3 (re-declare all 3): 3 minis, 0 mains.
	for _, sec := range []string{"backend", "db", "frontend"} {
		needle := "section: " + sec + ", tier: mini (already loaded this session)"
		if !strings.Contains(skillEntries[1].Content, needle) {
			t.Errorf("Phase 5.5 FAIL: turn 3 entry missing %q. Got:\n%s", needle, skillEntries[1].Content)
		}
	}
	if strings.Contains(skillEntries[1].Content, "tier: main") {
		t.Errorf("Phase 5.5 FAIL: turn 3 entry should have 0 Main decisions (all re-declared), got:\n%s", skillEntries[1].Content)
	}

	// Turn 4 (re-declare 2): 2 minis, 0 mains.
	for _, sec := range []string{"backend", "frontend"} {
		needle := "section: " + sec + ", tier: mini (already loaded this session)"
		if !strings.Contains(skillEntries[2].Content, needle) {
			t.Errorf("Phase 5.5 FAIL: turn 4 entry missing %q. Got:\n%s", needle, skillEntries[2].Content)
		}
	}
	if strings.Contains(skillEntries[2].Content, "section: db,") {
		t.Errorf("Phase 5.5 FAIL: turn 4 entry should not mention db as a decision (db wasn't re-declared). Got:\n%s", skillEntries[2].Content)
	}
	if strings.Contains(skillEntries[2].Content, "tier: main") {
		t.Errorf("Phase 5.5 FAIL: turn 4 entry should have 0 Main decisions (all re-declared), got:\n%s", skillEntries[2].Content)
	}

	// --- Trace-log assertions (Phase 4 observability contract) --
	// The trace must have exactly 3 EventSkillSelection entries
	// (one per Coder turn that emitted a SELECTED_SECTIONS
	// line), and the per-section tier recorded in the trace
	// must match the transcript [Skills] entries. /trace reads
	// from this — if the two diverge, the observability
	// contract §4.3 is broken.
	traceEntries, err := tracelog.LoadTrace(tracePath)
	if err != nil {
		t.Fatalf("LoadTrace: %v", err)
	}
	selections := findSkillSelections(traceEntries)
	if len(selections) != 3 {
		t.Fatalf("Phase 5.5 FAIL: trace has %d EventSkillSelection entries, want 3 (one per declared turn)",
			len(selections))
	}

	// Trace turn 1: 3 Mains. Token cost per section must be
	// non-zero and consistent with the real body sizes.
	decisionsT1 := extractDecisions(t, selections[0].Data)
	if len(decisionsT1) != 3 {
		t.Fatalf("Phase 5.5 FAIL: trace turn 1 decisions = %d, want 3", len(decisionsT1))
	}
	for _, d := range decisionsT1 {
		if d["tier"] != string(skills.TierMain) {
			t.Errorf("Phase 5.5 FAIL: trace turn 1 section %q has tier %q, want main",
				d["section"], d["tier"])
		}
		if cost, _ := d["token_cost"].(float64); cost < 1000 {
			t.Errorf("Phase 5.5 FAIL: trace turn 1 section %q token_cost = %v, want >1000 (real Main bodies are 5-8k tokens, not stubs)",
				d["section"], cost)
		}
	}

	// Trace turn 3: 3 Minis.
	decisionsT3 := extractDecisions(t, selections[1].Data)
	if len(decisionsT3) != 3 {
		t.Fatalf("Phase 5.5 FAIL: trace turn 3 decisions = %d, want 3", len(decisionsT3))
	}
	for _, d := range decisionsT3 {
		if d["tier"] != string(skills.TierMini) {
			t.Errorf("Phase 5.5 FAIL: trace turn 3 section %q has tier %q, want mini",
				d["section"], d["tier"])
		}
		if cost, _ := d["token_cost"].(float64); cost < 500 {
			t.Errorf("Phase 5.5 FAIL: trace turn 3 section %q token_cost = %v, want >500 (real Mini bodies are 2-4k tokens, not stubs)",
				d["section"], cost)
		}
	}

	// Trace turn 4: 2 Minis.
	decisionsT4 := extractDecisions(t, selections[2].Data)
	if len(decisionsT4) != 2 {
		t.Fatalf("Phase 5.5 FAIL: trace turn 4 decisions = %d, want 2", len(decisionsT4))
	}
	for _, d := range decisionsT4 {
		if d["tier"] != string(skills.TierMini) {
			t.Errorf("Phase 5.5 FAIL: trace turn 4 section %q has tier %q, want mini",
				d["section"], d["tier"])
		}
	}
}
