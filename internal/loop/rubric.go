package loop

// routing_rubric.go
//
// Phase 5 of the Workflow 3 spec introduces a *documented, testable* rubric
// behind Orchestrator's routing decision. Previously, the criteria lived
// inline in ClassifyTask and were impossible to reason about as a unit:
// "trivial" was a long cascade of substring checks + word-count thresholds,
// "critical" was a hardcoded keyword slice, and "middle" was the default
// fall-through. That's fine for code, but it makes the rubric invisible
// to a reviewer reading work.md, impossible to keep aligned with the
// hook blocklist (Workflow 2 §3.2.3) and clarify/assess.go's safety
// vocabulary, and impossible to test for cross-run consistency.
//
// This file extracts that rubric as data — named, weighted, and doc-cited —
// and makes ClassifyTask consume it. The actual decision algorithm is
// unchanged: this is a *refactor for clarity and testability*, not a
// behaviour change. All existing tests must continue to pass.
//
// ---------------------------------------------------------------------
// Design contract — what the rubric MUST guarantee
// ---------------------------------------------------------------------
//
//  1. Determinism. ClassifyTask is a pure function of its string input.
//     Same input → same (tier, reason) tuple, every run, every process.
//     This is what makes "traceable" routing decisions possible at all
//     (Phase 0 — non-deterministic LLM routing is a real industry risk;
//     this codebase's heuristic rubric is the explicit mitigation).
//
//  2. Shared safety vocabulary. The critical keyword set in this file
//     is the same set of surfaces the hook blocklist and clarify/
//     assess.go gate on. If you add a keyword here, add it there. If
//     you change a wording, change it everywhere. A divergence means
//     a task can pass Orchestrator as "trivial" while the hook
//     auto-blocks it — a confusing UX.
//
//  3. Conservative on the trivial side. False-negative trivial
//     (calling something trivial that should have been routed through
//     Triad) is far more expensive than false-positive trivial
//     (treating a simple task as middle and asking the human once).
//     When in doubt, route to middle. This is the same bias the
//     existing ClassifyTask code applied — the rubric just makes it
//     explicit and reviewable.
//
//  4. The rubric is itself a versioned artifact. The RubricVersion
//     const below MUST be bumped whenever a rule, weight, or keyword
//     changes — and the test suite (TestRubric_VersionBump) MUST be
//     updated to assert the new version. This is how we keep the
//     rubric's history traceable, in the same spirit as append-only
//     JSONL transcripts.
//
// ---------------------------------------------------------------------
// Decision flow (the rubric's "decision tree")
// ---------------------------------------------------------------------
//
// The rubric expresses the same flow as the original ClassifyTask
// function, just as named rules instead of inline branches:
//
//   TIER CRITICAL — auto-route to Triad (no confirmation)
//   ┌─────────────────────────────────────────────────────────────┐
//   │ 1. ANY critical keyword from CriticalKeywords appears as    │
//   │    a whole word/phrase in the task.                         │
//   └─────────────────────────────────────────────────────────────┘
//
//   TIER TRIVIAL — auto-route to General Chat (no confirmation)
//   ┌─────────────────────────────────────────────────────────────┐
//   │ 2. Empty task.                                              │
//   │ 3. Conversational/informational opener (trivialPhrases).    │
//   │ 4. Short (≤ MaxTrivialWords) AND single target              │
//   │    (≤ MaxTrivialFileExts, no path separators).              │
//   └─────────────────────────────────────────────────────────────┘
//
//   TIER MIDDLE — ask the human to confirm or override
//   ┌─────────────────────────────────────────────────────────────┐
//   │ 5. Long (> MaxMiddleWords) OR multi-file scope              │
//   │    (≥ MinMiddleFileExts OR path separators present).        │
//   │ 6. Otherwise: ambiguous complexity — confirm with human.    │
//   └─────────────────────────────────────────────────────────────┘
//
// Each rule is a self-contained function returning (matched bool,
// reason string). The combined ClassifyTask walks them in order
// and returns the first match — same behaviour as before, just with
// the rules inspectable in isolation.

// RubricVersion is the version of the routing rubric. Bump it whenever
// any rule, weight, or keyword changes — TestRubric_VersionBump in
// classify_test.go asserts the current value.
const RubricVersion = "1.1.0"

// RoutingRubric is the named, testable set of criteria Orchestrator
// uses to classify a task into trivial / critical / middle.
//
// It is intentionally data, not behaviour. The *algorithm* lives in
// ClassifyTask; this struct is the rule-set it consults. Tests can
// inspect this struct directly to assert the rubric's shape without
// going through the classification code.
type RoutingRubric struct {
	// Version mirrors RubricVersion — kept here as a field so the
	// rubric is self-describing when printed or logged.
	Version string

	// CriticalKeywords are the surface terms that always force TierCritical,
	// regardless of task length or other signals. These deliberately mirror
	// clarify/assess.go's `sensitive` slice and the hook blocklist from
	// Workflow 2 §3.2.3 — if you change one, change all three.
	CriticalKeywords []string

	// GreetingOpeners are single-word greetings that are always trivial,
	// but ONLY when the entire task IS the greeting. "hi" is trivial;
	// "hi please refactor the auth system" is NOT — the second word is
	// the real task and Reviewer oversight is exactly the right call.
	// Match: lower == opener.
	GreetingOpeners []string

	// InformationalOpeners are question-style openers (what is, how do
	// i, explain, tell me, etc.) that are always trivial regardless of
	// what follows. A "what is X" or "explain Y in Z" is a request for
	// information, not a code change — Reviewer overhead is pure waste.
	// Match: lower starts with opener + " " (greedy).
	InformationalOpeners []string

	// ShortFixOpeners are short code-modification patterns that are
	// trivial only when the rest of the task is short and single-file.
	// "fix typo" matches "fix typo in README" (4 words, 0 ext, no path)
	// but NOT "fix typo in cmd/triad/main.go" — that has a path
	// separator indicating multi-file scope, so the task falls through
	// to the multi-file check.
	// Match: lower == opener || lower starts with opener + " " AND
	// the suffix has no path separators. Otherwise the short-and-
	// focused rule below handles it (or it goes to middle).
	ShortFixOpeners []string

	// MaxTrivialWords is the upper bound on word count for a task to
	// still qualify as a "short one-liner". Above this, the task is no
	// longer eligible for the short-and-focused trivial rule.
	MaxTrivialWords int

	// MaxTrivialFileExts is the upper bound on distinct file-extension
	// strings in a task for it to count as single-target. Above this,
	// the task is treated as multi-file scope → TierMiddle.
	MaxTrivialFileExts int

	// MinMiddleWords is the lower bound on word count above which a task
	// is always TierMiddle, even if it would otherwise be a "focused"
	// description. Long description ⇒ ask the human.
	MinMiddleWords int

	// MinMiddleFileExts is the lower bound on distinct file-extension
	// strings in a task that triggers TierMiddle. ≥ this count means
	// multi-file scope — confirm with human.
	MinMiddleFileExts int

	// MinMiddleChars is the lower bound on raw character count above
	// which a task is always TierMiddle. Belt-and-suspenders against
	// tasks that are long but don't have many whitespace-separated
	// words (e.g. a single very long identifier / URL / log line).
	MinMiddleChars int

	// ReasonPrefixes are the leading strings used to build the
	// human-readable reason attached to a routing decision. Kept here
	// so the wording is consistent and reviewable.
	ReasonPrefixes struct {
		Critical string
		Trivial  string
		Middle   string
	}
}

// DefaultRoutingRubric returns the canonical rubric. ClassifyTask
// always uses this; exposing the function (rather than a package
// var) makes tests that need a tweaked rubric easy to write without
// mutating shared state.
func DefaultRoutingRubric() RoutingRubric {
	return RoutingRubric{
		Version: RubricVersion,
		// CriticalKeywords — see routing_rubric.go header for the
		// shared-safety-vocabulary contract. Keep this list in sync
		// with clarify.assess.go's `sensitive` slice and the hook
		// blocklist from Workflow 2 §3.2.3.
		//
		// Phase 5 §5.3 added "login", "logout", "signin", "signup"
		// to close the gap the wider test corpus exposed: a short
		// task like "add login" was routing to Trivial because the
		// keyword "auth" wasn't a substring. These are all
		// unambiguous auth surfaces, so they belong here.
		CriticalKeywords: []string{
			"auth", "authentication", "authorization",
			"login", "logout", "signin", "signout", "sign in", "sign out", "signup", "sign up",
			"password", "credential", "secret", "api key", "apikey", "token",
			"payment", "billing", "charge", "refund", "subscription",
			"delete", "drop", "truncate", "remove",
			"migration", "migrate", "schema change",
			"permission", "rbac", "security",
			"refactor", "overhaul", "redesign", "re-architect",
			"architecture",
			"breaking change",
			"database",
			"multiple files", "across all files", "all tests",
		},
		// GreetingOpeners — single-word greetings, exact-match only.
		// "hi" is trivial; "hi please refactor auth" is NOT (the
		// second word starts a real task).
		GreetingOpeners: []string{
			"hi", "hello", "hey", "yo", "ping", "thanks", "thank you",
		},
		// InformationalOpeners — question-style openers, greedy match.
		// Any task starting with one of these is an information request,
		// not a code change, so Reviewer overhead is pure waste.
		InformationalOpeners: []string{
			"what is", "what's", "how do i", "how to",
			"explain", "tell me", "show me", "who is", "what are",
		},
		// ShortFixOpeners — code-modification openers, matched with
		// a guard against path separators in the suffix. "fix typo"
		// is trivial; "fix typo in cmd/triad/main.go" is multi-file
		// scope and falls through to the middle-tier checks.
		ShortFixOpeners: []string{
			"typo", "fix typo",
		},
		// ≤ 6 words AND ≤ 1 extension AND no path separators → trivial.
		// These thresholds were chosen in Phase 4 as the conservative
		// envelope: a "rename X to Y" or "fix typo in README" always
		// fits, but anything describing a behaviour change does not.
		MaxTrivialWords:     6,
		MaxTrivialFileExts:  1,
		// > 20 words OR > 120 chars OR ≥ 2 extensions OR any path → middle.
		MinMiddleWords:     20,
		MinMiddleFileExts:  2,
		MinMiddleChars:     120,
		ReasonPrefixes: struct {
			Critical string
			Trivial  string
			Middle   string
		}{
			Critical: "task touches a sensitive/critical surface",
			Trivial:  "short, focused task (≤ 6 words, single target) — minimal overhead needed",
			Middle:   "task complexity is ambiguous — routing confirmation needed",
		},
	}
}

// (No accessor shims needed — ClassifyTask reads the rubric directly
// via the DefaultRoutingRubric() call, keeping the data flow single-
// hop. If a future caller needs the keyword slice for its own check
// — e.g. a custom command — they should call DefaultRoutingRubric()
// themselves, not through a package-level alias.)
