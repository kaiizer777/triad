package clarify

import (
	"strings"
	"unicode"
)

// assessContext is a small bag of pre-computed values shared across the
// heuristic checks. Computing these once keeps AssessAmbiguity readable
// and avoids re-tokenising the task on every check.
type assessContext struct {
	raw   string
	lower string
	words []string
}

// AssessAmbiguity inspects a task description and returns a Batch with
// zero or more clarifying questions. The same Batch shape is used
// regardless of which mode called it (General Chat, Triad, Orchestrator,
// Twin Subagent) — the doc's whole point is that the "ask before starting
// work" step is one shared, reusable primitive.
//
// The heuristic is intentionally conservative. False positives (asking
// when the task was actually clear) are mildly annoying; false negatives
// (proceeding when clarification was needed) are expensive in tokens
// and human time. We bias toward asking, but only when at least one
// concrete signal fires — so a "hello" or "what time is it?" does NOT
// produce a clarification round.
func AssessAmbiguity(task string) Batch {
	ctx := assessContext{
		raw:   task,
		lower: strings.ToLower(strings.TrimSpace(task)),
		words: strings.Fields(strings.ToLower(strings.TrimSpace(task))),
	}
	if ctx.lower == "" {
		// An empty task isn't ambiguous — it's a no-op. The caller
		// should normally not even get here, but treat it as
		// unambiguous so the loop never deadlocks on a silent
		// empty input.
		return Batch{NeedsClarification: false, InterpretationHint: "Empty task — nothing to do."}
	}

	// Trivial conversational / informational tasks are NEVER
	// clarification-worthy. These are exactly the cases Phase 2's
	// mismatch check uses to spot a "you probably don't need Triad
	// here" task — and clarifying them would be obnoxious.
	if isTrivial(ctx) {
		return Batch{
			NeedsClarification: false,
			InterpretationHint: "Conversational / informational request — no clarification needed.",
		}
	}

	var questions []Question
	seen := map[string]bool{} // dedup by reason so we don't ask the same thing twice

	// Each check below appends a question with a unique Reason key.
	// Order doesn't matter — we sort by ID at render time.
	addQ := func(text, reason, def string) {
		if seen[reason] {
			return
		}
		seen[reason] = true
		questions = append(questions, Question{
			ID:      len(questions) + 1,
			Text:    text,
			Reason:  reason,
			Default: def,
		})
	}

	// 1. Pronoun / demonstrative without a clear antecedent.
	// "fix it", "update that", "rename this" — we don't know what
	// "it" refers to. Cheap and high-signal.
	if hasVaguePronoun(ctx) {
		addQ(
			"Which file or component does your task refer to? (e.g. a specific file path, package, or symbol)",
			"vague-pronoun",
			"I'll grep for the most likely file based on context.",
		)
	}

	// 2. Action verb is present but the target is missing.
	// "rename", "refactor", "delete", "add", "update" etc. without a
	// concrete object — we don't know what to operate on.
	if hasBareAction(ctx) {
		addQ(
			"What should the action apply to? (file path, function name, package, or specific behaviour)",
			"bare-action",
			"I'll pick the most likely target based on the repo layout.",
		)
	}

	// 3. Multi-file or "across X" / "all Y" patterns — confirm scope.
	if hasAmbiguousScope(ctx) {
		addQ(
			"What's the exact scope? (which files / dirs / modules should I touch — or \"all of them\" if global)",
			"ambiguous-scope",
			"I'll scope to the most specific natural unit (one file or one package).",
		)
	}

	// 4. Sensitive-surface keywords (auth, payment, delete, secret,
	// migration, credential) — even with clear intent, these deserve
	// an explicit "yes I really mean this" confirmation.
	if hasSensitiveSurface(ctx) {
		addQ(
			"This touches a sensitive area (auth/payment/deletion/etc). Do you want me to proceed, and is there extra review/backup I should do first?",
			"sensitive-surface",
			"I'll proceed with default caution: no irreversible actions, add a clear comment, and surface the change for Reviewer scrutiny.",
		)
	}

	// 5. Output-format ambiguity — "write tests" doesn't say which
	// framework, "make it work" doesn't say what "work" means, etc.
	if hasUnspecifiedOutput(ctx) {
		addQ(
			"What's the expected output / success criteria? (e.g. specific file written, test passing, behaviour matching X)",
			"unspecified-output",
			"I'll aim for the most standard interpretation (e.g. Go test file with table-driven cases for \"write tests\").",
		)
	}

	if len(questions) == 0 {
		return Batch{
			NeedsClarification: false,
			InterpretationHint: buildInterpretationHint(ctx),
		}
	}

	return Batch{
		NeedsClarification: true,
		Questions:          questions,
		InterpretationHint: buildInterpretationHint(ctx),
	}
}

// buildInterpretationHint produces a one-line "most likely interpretation"
// string used both as part of the clarify block and as the stated
// best-guess recorded when the human says "proceed". It's a small
// stable summary, not a re-narration of the whole task.
func buildInterpretationHint(ctx assessContext) string {
	trimmed := strings.TrimSpace(ctx.raw)
	// Cap at ~140 chars so the System entry doesn't dominate the
	// transcript on very long tasks.
	if len(trimmed) > 140 {
		trimmed = strings.TrimSpace(trimmed[:137]) + "..."
	}
	return "Treat the task as: " + trimmed
}

// ---------------------------------------------------------------------------
// Heuristic checks. Each returns true if the task has the corresponding
// ambiguity signal. They are deliberately independent (no early-return)
// so we can BATCH all signals in one round, not one at a time.
// ---------------------------------------------------------------------------

func isTrivial(ctx assessContext) bool {
	if len(ctx.words) == 0 {
		return true
	}
	// Very short queries with conversational openers are almost
	// never clarification-worthy. Imperative-but-underspecified
	// tasks like "fix it" / "rename it" are NOT in this set — those
	// are the textbook case for clarify, and the doc explicitly
	// requires us to ask before starting work.
	trivialOpeners := []string{
		"hi", "hello", "hey", "yo", "thanks", "thank you",
		"what is", "what's", "how do i", "how to", "explain",
		"who is", "what are", "tell me",
	}
	first := ctx.words[0]
	for _, opener := range trivialOpeners {
		if first == opener || strings.HasPrefix(ctx.lower, opener+" ") || strings.HasPrefix(ctx.lower, opener+",") {
			return true
		}
	}
	// A bare single token (e.g. "hi", "hey") is conversational.
	// We deliberately do NOT extend this to 2-word imperatives
	// like "fix it" — those are exactly the cases clarify exists
	// to surface. The 1-word fast-path is just a guard against
	// accidental single-token input.
	if len(ctx.words) == 1 {
		return true
	}
	return false
}

func hasVaguePronoun(ctx assessContext) bool {
	// A small set of demonstratives / pronouns that almost always
	// need an antecedent. "it" alone (no other content words) is
	// skipped by the trivial check above.
	vague := []string{
		" fix it", " break it", " update it", " rename it",
		" refactor it", " delete it", " remove it", " change it",
		" fix that", " update that", " rename that", " change that",
		" fix this", " update this", " rename this", " change this",
		" this file", " that file", " the file",
		" this function", " that function", " the function",
		" this bug", " that bug", " the bug",
	}
	for _, v := range vague {
		if strings.Contains(" "+ctx.lower, v) {
			return true
		}
	}
	return false
}

func hasBareAction(ctx assessContext) bool {
	bareActions := []string{
		"rename", "refactor", "delete", "remove", "rewrite",
		"optimize", "improve", "clean up", "fix", "update",
		"add", "create", "implement", "build", "write",
		"change", "modify", "move",
	}
	if len(ctx.words) == 0 {
		return false
	}
	first := strings.TrimRight(ctx.words[0], ",:;")
	isBareActionStart := false
	for _, a := range bareActions {
		if first == a {
			isBareActionStart = true
			break
		}
	}
	if !isBareActionStart {
		return false
	}
	// Now check: is there ANY concrete target anywhere in the
	// task? File path (with / or \), file extension, a quoted
	// string, or an explicit noun phrase (>= 2 words after the
	// verb). If so, the task is clear enough.
	hasPath := strings.Contains(ctx.raw, "/") || strings.Contains(ctx.raw, `\`)
	hasExt := false
	for _, ext := range []string{".go", ".ts", ".tsx", ".js", ".jsx", ".py", ".md", ".txt", ".json", ".yaml", ".yml", ".html", ".css", ".rs", ".java", ".kt", ".swift", ".c", ".cpp", ".h", ".hpp", ".sh", ".sql"} {
		if strings.Contains(ctx.lower, ext) {
			hasExt = true
			break
		}
	}
	hasQuoted := strings.Contains(ctx.raw, `"`) || strings.Contains(ctx.raw, `'`)
	if hasPath || hasExt || hasQuoted {
		return false
	}
	// "rename that" / "delete it" are handled by hasVaguePronoun
	// separately, but if we got here it's a bare-action task with
	// no concrete target at all. "rename" alone, "fix" alone,
	// "refactor" with nothing else — those are bare.
	return true
}

func hasAmbiguousScope(ctx assessContext) bool {
	ambiguousScopeMarkers := []string{
		"everywhere", "all over", "all files", "all the files",
		"across all", "across the", "throughout", "whole project",
		"whole repo", "entire project", "entire repo",
		"globally", "all tests", "all of them",
	}
	for _, m := range ambiguousScopeMarkers {
		if strings.Contains(ctx.lower, m) {
			return true
		}
	}
	// "in <single dir>" is fine. "in <dir> and <dir>" is not.
	// Count " and " between path-ish words.
	return false
}

func hasSensitiveSurface(ctx assessContext) bool {
	sensitive := []string{
		"auth", "authentication", "authorization", "password",
		"payment", "billing", "charge", "refund", "subscription",
		"delete", "remove", "drop", "truncate",
		"migration", "migrate", "schema change",
		"secret", "credential", "api key", "apikey", "token",
		"permission", "rbac", "security",
	}
	for _, s := range sensitive {
		// Word-ish match so "author" doesn't trigger on "authorization".
		if containsWord(ctx.lower, s) {
			return true
		}
	}
	return false
}

func hasUnspecifiedOutput(ctx assessContext) bool {
	// "write tests" / "add tests" without naming a framework or
	// existing file to extend.
	if (strings.Contains(ctx.lower, "write test") || strings.Contains(ctx.lower, "add test")) &&
		!containsAny(ctx.lower, []string{"go test", "pytest", "jest", "mocha", "vitest", "xunit", "rspec", "junit"}) {
		return true
	}
	// "make it work" / "make it pass" — outcome unspecified.
	vagueOutcomes := []string{
		"make it work", "make it pass", "make it fast",
		"make it better", "improve performance",
		"fix the issue", "address the issue",
	}
	for _, v := range vagueOutcomes {
		if strings.Contains(ctx.lower, v) {
			return true
		}
	}
	return false
}

// containsWord reports whether needle appears in s as a whole word (i.e.
// surrounded by non-letter characters or string boundaries). Cheap
// approximation: scan with simple boundaries.
func containsWord(s, needle string) bool {
	if needle == "" {
		return false
	}
	idx := 0
	for {
		i := strings.Index(s[idx:], needle)
		if i < 0 {
			return false
		}
		i += idx
		leftOK := i == 0 || !isLetter(rune(s[i-1]))
		rightIdx := i + len(needle)
		rightOK := rightIdx >= len(s) || !isLetter(rune(s[rightIdx]))
		if leftOK && rightOK {
			return true
		}
		idx = rightIdx
	}
}

func containsAny(s string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}

func isLetter(r rune) bool {
	return unicode.IsLetter(r)
}
