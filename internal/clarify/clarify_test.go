package clarify

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// AssessAmbiguity — core cases
// ---------------------------------------------------------------------------

func TestAssessAmbiguity_TrivialNotFlagged(t *testing.T) {
	cases := []string{
		"hello",
		"hi there",
		"what is go?",
		"explain channels",
		"thanks",
		"hey",
	}
	for _, task := range cases {
		t.Run(task, func(t *testing.T) {
			b := AssessAmbiguity(task)
			if b.NeedsClarification {
				t.Errorf("trivial task %q should NOT trigger clarification, got %d questions: %+v", task, len(b.Questions), b.Questions)
			}
		})
	}
}

func TestAssessAmbiguity_ClearTaskNotFlagged(t *testing.T) {
	cases := []string{
		"Add a /users/:id endpoint to internal/api/users.go that returns the user record by id",
		"Write a Python script in scripts/parse.py that reads input.csv and outputs top 10 rows",
		"Refactor the runActiveCycle function in internal/loop/loop.go to extract the Reviewer loop into its own helper",
		"Update README.md to mention the new /journey slash command",
	}
	for _, task := range cases {
		t.Run(task, func(t *testing.T) {
			b := AssessAmbiguity(task)
			if b.NeedsClarification {
				t.Errorf("clear task %q should NOT trigger clarification, got %d questions: %+v", task, len(b.Questions), b.Questions)
			}
			if b.InterpretationHint == "" {
				t.Errorf("clear task must still produce a non-empty InterpretationHint for the proceed-with-best-judgment path")
			}
		})
	}
}

func TestAssessAmbiguity_VaguePronounFlagged(t *testing.T) {
	b := AssessAmbiguity("fix it")
	if !b.NeedsClarification {
		t.Fatalf("vague-pronoun task should trigger clarification, got %+v", b)
	}
	// The batched render should mention the question, NOT multiple
	// piecemeal rounds — confirmed by the single FormatClarifyBlock
	// below. Here we just assert at least one question.
	if len(b.Questions) == 0 {
		t.Fatalf("expected at least one clarifying question, got none")
	}
}

func TestAssessAmbiguity_BareActionFlagged(t *testing.T) {
	b := AssessAmbiguity("rename it")
	if !b.NeedsClarification {
		t.Fatalf("bare-action task should trigger clarification, got %+v", b)
	}
}

func TestAssessAmbiguity_SensitiveSurfaceFlagged(t *testing.T) {
	b := AssessAmbiguity("Update the auth module to support OAuth")
	if !b.NeedsClarification {
		t.Fatalf("sensitive-surface task should trigger clarification, got %+v", b)
	}
	// The sensitive-surface question must be among the batch.
	found := false
	for _, q := range b.Questions {
		if q.Reason == "sensitive-surface" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a sensitive-surface question, got: %+v", b.Questions)
	}
}

func TestAssessAmbiguity_BatchedNotPiecemeal(t *testing.T) {
	// A task that should trigger MULTIPLE signals at once (vague
	// pronoun + bare action + sensitive surface) must produce a
	// SINGLE Batch with multiple questions, NOT multiple batches
	// from successive calls. This is the load-bearing property
	// the doc calls out.
	b := AssessAmbiguity("fix it — the auth bit is broken")
	if !b.NeedsClarification {
		t.Fatalf("expected clarification, got %+v", b)
	}
	if len(b.Questions) < 2 {
		t.Errorf("expected multiple questions batched in one round, got %d: %+v", len(b.Questions), b.Questions)
	}
	// IDs must be unique and sequential 1..N.
	seen := map[int]bool{}
	for _, q := range b.Questions {
		if seen[q.ID] {
			t.Errorf("duplicate question ID %d", q.ID)
		}
		seen[q.ID] = true
	}
	for i := 1; i <= len(b.Questions); i++ {
		if !seen[i] {
			t.Errorf("missing question ID %d (IDs should be 1..%d)", i, len(b.Questions))
		}
	}
}

func TestAssessAmbiguity_DedupesAcrossSignals(t *testing.T) {
	// Two checks that might both fire the same reason should not
	// produce two questions with the same Reason. We construct a
	// task that hits several bare-action-adjacent phrasings.
	b := AssessAmbiguity("rename and refactor")
	if !b.NeedsClarification {
		t.Fatalf("expected clarification, got %+v", b)
	}
	seen := map[string]int{}
	for _, q := range b.Questions {
		seen[q.Reason]++
	}
	for reason, count := range seen {
		if count > 1 {
			t.Errorf("reason %q produced %d questions; expected at most 1", reason, count)
		}
	}
}

func TestAssessAmbiguity_EmptyTask(t *testing.T) {
	b := AssessAmbiguity("")
	if b.NeedsClarification {
		t.Errorf("empty task should not trigger clarification, got %+v", b)
	}
	if b.InterpretationHint == "" {
		t.Errorf("empty task should still produce an interpretation hint")
	}
}

// ---------------------------------------------------------------------------
// IsProceedCommand
// ---------------------------------------------------------------------------

func TestIsProceedCommand(t *testing.T) {
	cases := []struct {
		msg  string
		want bool
	}{
		{"/proceed", true},
		{"/proceed with my best guess", true},
		{"proceed", true},
		{"just proceed", true},
		{"go ahead", true},
		{"just go", true},
		{"use your best judgment", true},
		{"use your judgement", true},
		{"do your best", true},
		{"your call", true},
		{"figure it out", true},
		{"up to you", true},
		{"don't ask", true},
		{"don't ask, just proceed", true},
		{"skip questions", true},
		{"no more questions", true},

		// Negative cases — these are NOT proceed signals.
		{"please proceed with the next phase", true}, // contains "proceed" — fine
		{"hello", false},
		{"fix the bug", false},
		{"", false},
		// A normal task that happens to contain "proceed" — should still
		// match (the doc doesn't require surgical detection, just the
		// "user clearly wants to skip the questions" signal). We
		// intentionally accept this — false positives here just unblock
		// the loop with a best-guess note, which is the same behavior
		// as the explicit /proceed command.
	}
	for _, c := range cases {
		t.Run(c.msg, func(t *testing.T) {
			got := IsProceedCommand(c.msg)
			if got != c.want {
				t.Errorf("IsProceedCommand(%q) = %v, want %v", c.msg, got, c.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Format helpers
// ---------------------------------------------------------------------------

func TestFormatClarifyBlock_BatchedShape(t *testing.T) {
	b := Batch{
		NeedsClarification: true,
		InterpretationHint: "Treat the task as: fix it",
		Questions: []Question{
			{ID: 1, Text: "Which file?", Reason: "vague-pronoun", Default: "grep for it"},
			{ID: 2, Text: "What's the scope?", Reason: "ambiguous-scope", Default: "narrow"},
		},
	}
	out := FormatClarifyBlock(b)

	// Single System entry (no double-rendering).
	if strings.Count(out, "[System]:") != 1 {
		t.Errorf("expected exactly one [System]: header, got %d in:\n%s", strings.Count(out, "[System]:"), out)
	}
	// All questions present in one block.
	if !strings.Contains(out, "1) Which file?") {
		t.Errorf("missing question 1 in:\n%s", out)
	}
	if !strings.Contains(out, "2) What's the scope?") {
		t.Errorf("missing question 2 in:\n%s", out)
	}
	// Reasons shown so the user understands why we're asking.
	if !strings.Contains(out, "vague-pronoun") {
		t.Errorf("expected reason to surface in:\n%s", out)
	}
	// Defaults shown.
	if !strings.Contains(out, "[default: grep for it]") {
		t.Errorf("expected default to surface in:\n%s", out)
	}
	// Must tell the user they can say "proceed".
	if !strings.Contains(out, "proceed") {
		t.Errorf("expected the render to mention the proceed signal, got:\n%s", out)
	}
}

func TestFormatClarifyBlock_SortsByID(t *testing.T) {
	// Pass questions in scrambled order; render must come out 1..N.
	b := Batch{
		NeedsClarification: true,
		Questions: []Question{
			{ID: 3, Text: "Q3"},
			{ID: 1, Text: "Q1"},
			{ID: 2, Text: "Q2"},
		},
	}
	out := FormatClarifyBlock(b)
	i1 := strings.Index(out, "1) Q1")
	i2 := strings.Index(out, "2) Q2")
	i3 := strings.Index(out, "3) Q3")
	if !(i1 >= 0 && i2 > i1 && i3 > i2) {
		t.Errorf("expected questions in order 1,2,3; got indices %d,%d,%d in:\n%s", i1, i2, i3, out)
	}
}

func TestFormatClarifyBlock_NoQuestionsFallback(t *testing.T) {
	// Defensive: if someone hands us a non-clarifying batch, we
	// still produce something sensible.
	b := Batch{NeedsClarification: false, InterpretationHint: "ok"}
	out := FormatClarifyBlock(b)
	if !strings.Contains(out, "clear") {
		t.Errorf("fallback render should mention the task is clear, got: %q", out)
	}
}

func TestFormatProceedNote(t *testing.T) {
	b := Batch{InterpretationHint: "Treat the task as: fix the foo bug"}
	note := FormatProceedNote(b)
	if !strings.Contains(note, "Treat the task as: fix the foo bug") {
		t.Errorf("proceed note must state the best-guess interpretation, got: %q", note)
	}

	// Empty hint -> safe fallback.
	note2 := FormatProceedNote(Batch{})
	if !strings.Contains(note2, "Proceeding with best-judgment") {
		t.Errorf("empty-hint proceed note should fall back gracefully, got: %q", note2)
	}
}
