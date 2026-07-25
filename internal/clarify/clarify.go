// Package clarify implements the shared "batched upfront clarifying questions"
// step that all modes (General Chat, Triad, and — later — Orchestrator and
// Twin Subagent) call before starting real work on a task.
//
// Design (docs/x.md §Phase 3):
//
//   - "Batched" is the load-bearing word. All questions for a given task are
//     surfaced in ONE round, not one-at-a-time, and not scattered mid-task.
//   - "Upfront" means this runs BEFORE the first agent turn / tool call, not
//     during the loop. If a human sends "just proceed" (or the explicit
//     /proceed command) the agent(s) continue with a stated best-guess
//     interpretation, recorded as a System note in the transcript.
//   - This package is intentionally heuristic-based for now (free, fast,
//     deterministic, no token cost on every task) — the same flavour as
//     internal/loop/mismatch.go. If heuristics turn out too noisy in real
//     use, the package boundary is small enough to swap in an LLM-driven
//     assessor without touching callers.
//
// The package is dependency-free (no agent/transcript imports) so it can
// be reused by both the headless loop and the TUI without cycles.
package clarify

import (
	"fmt"
	"sort"
	"strings"
)

// Question is one numbered clarifying question produced by AssessAmbiguity.
// ID is stable across renders of the same question (so a human reply like
// "/answer 2=foo" can reference it cleanly).
type Question struct {
	ID      int    // 1-based, stable within a single batch
	Text    string // the question itself, e.g. "Which file should I edit?"
	Reason  string // short reason this is ambiguous; surfaces on render
	Default string // reasonable default if human says "just proceed"
}

// Batch is the result of one AssessAmbiguity call. NeedsClarification is
// the single, batched round — never partial, never piecemeal. Questions are
// sorted by ID ascending so renders are deterministic.
type Batch struct {
	NeedsClarification bool
	Questions          []Question
	// InterpretationHint is a one-line summary of the most likely
	// interpretation, used both inline with the questions and as the
	// stated best-guess recorded in the transcript when the human
	// says "proceed".
	InterpretationHint string
}

// IsProceedCommand reports whether a human message is the explicit-or-
// equivalent "proceed with your best judgment" signal. The doc (§3.3)
// requires that this unblock the loop, not stall forever. We accept a
// few common phrasings — the explicit slash command, "proceed", and
// the more conversational "just go" / "use your best judgment".
func IsProceedCommand(msg string) bool {
	trimmed := strings.TrimSpace(strings.ToLower(msg))
	if trimmed == "" {
		return false
	}
	// Exact /proceed (or /proceed with extra args) is the canonical
	// signal. We also accept bare "proceed" so the user doesn't have
	// to type the slash.
	if strings.HasPrefix(trimmed, "/proceed") {
		return true
	}
	proceedPhrases := []string{
		"proceed",
		"just proceed",
		"go ahead",
		"just go",
		"use your best judgment",
		"use your judgement",
		"do your best",
		"your call",
		"figure it out",
		"don't ask",
		"don't ask, just",
		"don't bother asking",
		"no questions",
		"no more questions",
		"skip questions",
		"i don't care",
		"up to you",
	}
	for _, p := range proceedPhrases {
		if strings.Contains(trimmed, p) {
			return true
		}
	}
	return false
}

// FormatClarifyBlock renders a Batch as a single, parseable System
// transcript entry. The format is intentionally stable — Phase 3 tests
// assert on it — and human-readable. The same renderer is used for the
// General Chat and Triad modes so the human always sees the same shape.
func FormatClarifyBlock(b Batch) string {
	if !b.NeedsClarification || len(b.Questions) == 0 {
		// Nothing to ask. Caller should normally not even invoke us
		// in this case, but we keep a clean fallback so the loop
		// can never accidentally render garbage.
		return "[System]: Task is clear enough to proceed."
	}

	var sb strings.Builder
	sb.WriteString("[System]: Before I start, a few clarifying questions (please answer or say \"proceed\"):\n")
	if b.InterpretationHint != "" {
		sb.WriteString("  Most likely interpretation: ")
		sb.WriteString(b.InterpretationHint)
		sb.WriteString("\n")
	}
	// Sort by ID for deterministic output.
	qs := append([]Question(nil), b.Questions...)
	sort.Slice(qs, func(i, j int) bool { return qs[i].ID < qs[j].ID })
	for _, q := range qs {
		sb.WriteString(fmt.Sprintf("  %d) %s", q.ID, q.Text))
		if q.Reason != "" {
			sb.WriteString("  (")
			sb.WriteString(q.Reason)
			sb.WriteString(")")
		}
		if q.Default != "" {
			sb.WriteString("  [default: ")
			sb.WriteString(q.Default)
			sb.WriteString("]")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("Reply with answers, or say \"proceed\" / /proceed to use defaults.")
	return strings.TrimRight(sb.String(), "\n")
}

// FormatProceedNote renders the System note recorded in the transcript
// when a human says "proceed" after a clarify round. It states the
// best-guess interpretation so future review of the transcript can see
// what assumption was made (rather than just silent proceeding).
func FormatProceedNote(b Batch) string {
	if b.InterpretationHint == "" {
		return "[System]: Proceeding with best-judgment interpretation (no clarification round was held)."
	}
	return "[System]: Proceeding with best-judgment interpretation: " + b.InterpretationHint
}
