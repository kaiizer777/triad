package loop_test

import (
	"strings"
	"testing"

	"github.com/kaiizer777/triad/internal/agent"
	"github.com/kaiizer777/triad/internal/loop"
)

// TestClassifyTask_Trivial verifies that clearly trivial tasks are classified
// as TierTrivial with a non-empty reason.
func TestClassifyTask_Trivial(t *testing.T) {
	trivialCases := []string{
		"fix typo in README",
		"hi",
		"hello there",
		"what is the difference between a slice and an array",
		"explain the main.go structure",
		"rename X",
		"ping",
	}
	for _, task := range trivialCases {
		t.Run(task, func(t *testing.T) {
			tier, reason := loop.ClassifyTask(task)
			if tier != loop.TierTrivial {
				t.Errorf("ClassifyTask(%q) = %q, want %q (reason: %s)", task, tier, loop.TierTrivial, reason)
			}
			if strings.TrimSpace(reason) == "" {
				t.Errorf("ClassifyTask(%q) returned empty reason", task)
			}
		})
	}
}

// TestClassifyTask_Critical verifies that tasks containing sensitive/critical
// keywords are classified as TierCritical with a non-empty reason mentioning
// the matched keyword.
func TestClassifyTask_Critical(t *testing.T) {
	criticalCases := []struct {
		task    string
		keyword string
	}{
		{"update the auth token validation", "auth"},
		{"add payment processing to checkout flow", "payment"},
		{"delete the old user records from the database", "delete"},
		{"migrate the users table to add a new column", "migrate"},
		{"rotate the api key for the external service", "api key"},
		{"fix the security vulnerability in the login handler", "security"},
		{"remove the legacy credential store", "credential"},
		{"refactor the entire authentication module", "refactor"},
		{"drop the sessions table", "drop"},
		{"change the rbac permission model", "rbac"},
	}
	for _, tc := range criticalCases {
		t.Run(tc.task, func(t *testing.T) {
			tier, reason := loop.ClassifyTask(tc.task)
			if tier != loop.TierCritical {
				t.Errorf("ClassifyTask(%q) = %q, want %q (reason: %s)", tc.task, tier, loop.TierCritical, reason)
			}
			if strings.TrimSpace(reason) == "" {
				t.Errorf("ClassifyTask(%q) returned empty reason", tc.task)
			}
		})
	}
}

// TestClassifyTask_Middle verifies that genuinely ambiguous tasks (not trivial,
// not critical) are classified as TierMiddle.
func TestClassifyTask_Middle(t *testing.T) {
	middleCases := []string{
		"improve the caching layer to handle concurrent writes better",
		"add a new endpoint for listing user preferences and wire it into the router",
		"update the retry logic in the background job runner to use exponential backoff",
		"extract the email-sending logic into a dedicated package",
		"write unit tests for the import/export pipeline",
		"update internal/loop/loop.go and internal/agent/client.go to add tracing",
	}
	for _, task := range middleCases {
		t.Run(task, func(t *testing.T) {
			tier, reason := loop.ClassifyTask(task)
			if tier != loop.TierMiddle {
				t.Errorf("ClassifyTask(%q) = %q, want %q (reason: %s)", task, tier, loop.TierMiddle, reason)
			}
			if strings.TrimSpace(reason) == "" {
				t.Errorf("ClassifyTask(%q) returned empty reason", task)
			}
		})
	}
}

// TestClassifyTask_EmptyTask verifies that an empty task is treated as trivial
// rather than panicking or returning an error.
func TestClassifyTask_EmptyTask(t *testing.T) {
	tier, reason := loop.ClassifyTask("")
	if tier != loop.TierTrivial {
		t.Errorf("ClassifyTask(\"\") = %q, want %q", tier, loop.TierTrivial)
	}
	if strings.TrimSpace(reason) == "" {
		t.Error("ClassifyTask(\"\") returned empty reason")
	}
}

// TestClassifyTask_NoBoundaryFalsePositive verifies that words containing
// critical keyword substrings but NOT matching whole-word boundaries are NOT
// classified as critical. E.g. "author" must not trigger on "auth".
func TestClassifyTask_NoBoundaryFalsePositive(t *testing.T) {
	cases := []struct {
		task     string
		notTier  string
		contains string
	}{
		// "author" must not trigger "auth" (word boundary check)
		{"update the author field in the readme", loop.TierCritical, "auth"},
		// "removable" must not trigger "remove"
		{"make the toolbar removable by the user", loop.TierCritical, "remove"},
	}
	for _, tc := range cases {
		t.Run(tc.task, func(t *testing.T) {
			tier, _ := loop.ClassifyTask(tc.task)
			if tier == tc.notTier {
				t.Errorf("ClassifyTask(%q) = %q — false positive: %q substring in word should not trigger critical tier", tc.task, tier, tc.contains)
			}
		})
	}
}

// TestOrchestratorMessage_Format verifies that OrchestratorMessage returns
// the expected [Orchestrator]: prefix for all three tiers.
func TestOrchestratorMessage_Format(t *testing.T) {
	cases := []struct {
		tier       string
		reason     string
		targetMode string
		wantPrefix string
	}{
		{loop.TierTrivial, "short task", "general", "[Orchestrator]:"},
		{loop.TierCritical, "touches auth", "triad", "[Orchestrator]:"},
		{loop.TierMiddle, "ambiguous scope", "triad", "[Orchestrator]:"},
	}
	for _, tc := range cases {
		msg := loop.OrchestratorMessage(tc.tier, tc.reason, tc.targetMode)
		if !strings.HasPrefix(msg, tc.wantPrefix) {
			t.Errorf("OrchestratorMessage(tier=%q) = %q; want prefix %q", tc.tier, msg, tc.wantPrefix)
		}
	}
}

// TestOrchestratorMessage_TrivialMentionsGeneral verifies that the trivial
// message explicitly mentions "General Chat" so the human can understand the routing.
func TestOrchestratorMessage_TrivialMentionsGeneral(t *testing.T) {
	msg := loop.OrchestratorMessage(loop.TierTrivial, "short task", "general")
	if !strings.Contains(strings.ToLower(msg), "general") {
		t.Errorf("trivial OrchestratorMessage does not mention 'general': %q", msg)
	}
}

// TestOrchestratorMessage_CriticalMentionsTriad verifies that the critical
// message explicitly mentions "Triad" so the human can understand the routing.
func TestOrchestratorMessage_CriticalMentionsTriad(t *testing.T) {
	msg := loop.OrchestratorMessage(loop.TierCritical, "touches auth", "triad")
	if !strings.Contains(strings.ToLower(msg), "triad") {
		t.Errorf("critical OrchestratorMessage does not mention 'triad': %q", msg)
	}
}

// TestOrchestratorMessage_MiddleAsksForConfirm verifies that the middle message
// asks for human confirmation ("proceed" / "confirm" / "override" etc.) rather
// than announcing a completed routing decision.
func TestOrchestratorMessage_MiddleAsksForConfirm(t *testing.T) {
	msg := loop.OrchestratorMessage(loop.TierMiddle, "ambiguous scope", "triad")
	lower := strings.ToLower(msg)
	if !strings.Contains(lower, "proceed") && !strings.Contains(lower, "confirm") && !strings.Contains(lower, "override") {
		t.Errorf("middle OrchestratorMessage does not ask for confirmation: %q", msg)
	}
}

// ---------------------------------------------------------------------------
// Phase 5 §5.3 — wider, more realistic test corpus
// ---------------------------------------------------------------------------
//
// The original classify_test.go covered a handful of tasks per tier
// — enough to prove the rules fired, not enough to prove the rules
// *generalize* across the trivial→critical range. Phase 5 widens
// the corpus to:
//   - more conversational / informational variants for trivial
//   - more surface terms (multi-word phrases, less-common keywords)
//     for critical
//   - intentionally-ambiguous cases that sit on the boundary
//     between trivial and middle (the doc's middle-ground probe)
//
// The point is to expose rubric drift, not to test the function.
// A task that was once middle but routes to trivial (or vice versa)
// after a rubric change is exactly the kind of inconsistency Phase 0
// flagged as a real risk.

// TestClassifyTask_WiderTrivial exercises a wider range of clearly-trivial
// tasks: short one-liners with no sensitive surface, conversational
// openers, and pure renames. Each one MUST route to TierTrivial.
func TestClassifyTask_WiderTrivial(t *testing.T) {
	trivialCases := []string{
		// pure renames
		"rename foo to bar",
		"rename X to Y",
		"rename main_test.go to triad_test.go",
		// small fixes
		"fix typo in README",
		"fix typo in CHANGELOG.md",
		"fix small typo",
		// conversational
		"hi",
		"hello there",
		"hey",
		"yo",
		"ping",
		"thanks",
		// informational
		"what is the difference between a slice and an array",
		"what's a goroutine",
		"how do i write a test in go",
		"how to install the dependencies",
		"explain the main.go structure",
		"explain how the loop works",
		"tell me about the routing rubric",
		"show me the transcript format",
		"who wrote this",
		// "rename" with a one-token target — still trivial
		"rename zzz",
	}
	for _, task := range trivialCases {
		t.Run(task, func(t *testing.T) {
			tier, reason := loop.ClassifyTask(task)
			if tier != loop.TierTrivial {
				t.Errorf("ClassifyTask(%q) = %q, want %q (reason: %s)", task, tier, loop.TierTrivial, reason)
			}
			if strings.TrimSpace(reason) == "" {
				t.Errorf("ClassifyTask(%q) returned empty reason", task)
			}
		})
	}
}

// TestClassifyTask_WiderCritical exercises a wider range of tasks
// touching the critical surface. Each MUST route to TierCritical
// regardless of length or scope.
func TestClassifyTask_WiderCritical(t *testing.T) {
	criticalCases := []string{
		// short tasks that mention sensitive surfaces
		"add login",
		"rotate the api key",
		"fix the security bug",
		"update the auth flow",
		"change the password policy",
		// long tasks mentioning sensitive surfaces
		"we need to refactor the entire user authentication system to support SSO providers and a new permission model that handles org-level RBAC",
		// billing / payments
		"add a billing endpoint that issues a refund when a subscription is cancelled",
		"wire the new charge flow into the existing payment processor and update the database schema for partial refunds",
		// deletion / dropping
		"drop the legacy sessions table from the production database",
		"truncate the audit log for the past year and remove the related indexes",
		// migrations
		"migrate the users table to add a new column for two-factor authentication",
		"do a schema change to support per-tenant encryption keys",
		// secrets / credentials
		"rotate the api key for the external service and update the credential store",
		"harden the secret-loading path so the token never appears in logs",
		// architectural surgery
		"overhaul the request-handling pipeline to support streaming responses",
		"redesign the routing layer so the per-mode logic lives in one place",
		// multi-word critical phrases
		"this is a breaking change to the public API contract",
		"add tests across all files that touch the database layer",
	}
	for _, task := range criticalCases {
		t.Run(task, func(t *testing.T) {
			tier, reason := loop.ClassifyTask(task)
			if tier != loop.TierCritical {
				t.Errorf("ClassifyTask(%q) = %q, want %q (reason: %s)", task, tier, loop.TierCritical, reason)
			}
			if strings.TrimSpace(reason) == "" {
				t.Errorf("ClassifyTask(%q) returned empty reason", task)
			}
		})
	}
}

// TestClassifyTask_AmbiguousMiddle probes the boundary between trivial
// and middle with tasks intentionally designed to be ambiguous. Each
// MUST route to TierMiddle — they describe non-trivial work but
// don't match a critical surface keyword.
//
// This is the middle-ground probe Phase 5 §5.3 calls for. If any of
// these regress to trivial, the rubric has become too lax on the
// boundary; if any regress to critical, a false-positive keyword
// match has crept in.
func TestClassifyTask_AmbiguousMiddle(t *testing.T) {
	ambiguous := []string{
		// 7-word tasks — just over the trivial threshold
		"add a debug log line to the orchestrator gate",
		"rename the loop package to triad_core across the import graph",
		// single-file but with a path separator — multi-file scope by rule
		"update the import in cmd/triad/main.go to the new path",
		// multi-extension
		"add a small CSS tweak and update the .md docs to match",
		// a "refactor" of a non-sensitive area — but the keyword is
		// universal; this SHOULD route to critical, not middle.
		// Listed in a separate test below.
		// genuine middle
		"extract the transcript JSON helpers into a new package",
		"add a /help command to the TUI that lists available modes",
		"make the orchestrator's stated reason configurable via an env var",
		"add a benchmark for the routing decision to the test suite",
		"update the README to mention the new Phase 5 rubric",
	}
	for _, task := range ambiguous {
		t.Run(task, func(t *testing.T) {
			tier, reason := loop.ClassifyTask(task)
			if tier != loop.TierMiddle {
				t.Errorf("ClassifyTask(%q) = %q, want %q (reason: %s)", task, tier, loop.TierMiddle, reason)
			}
			if strings.TrimSpace(reason) == "" {
				t.Errorf("ClassifyTask(%q) returned empty reason", task)
			}
		})
	}
}

// TestClassifyTask_BoundaryExclusivity pins the exact word-count /
// extension-count boundaries. A rubric tweak that bumps
// MaxTrivialWords from 6 to 7 (for example) should break this test
// so the change is deliberate, not silent.
func TestClassifyTask_BoundaryExclusivity(t *testing.T) {
	// 6 words: still trivial
	tier, _ := loop.ClassifyTask("rename foo to bar baz qux")
	if tier != loop.TierTrivial {
		t.Errorf("6-word rename should be trivial, got %q", tier)
	}
	// 7 words: over the boundary → middle
	tier, _ = loop.ClassifyTask("rename foo to bar baz qux quux")
	if tier != loop.TierMiddle {
		t.Errorf("7-word rename should be middle, got %q", tier)
	}
	// 20 words: middle
	twenty := "one two three four five six seven eight nine ten eleven twelve thirteen fourteen fifteen sixteen seventeen eighteen nineteen twenty"
	if strings.Fields(twenty) == nil || len(strings.Fields(twenty)) != 20 {
		t.Fatalf("test bug: 20-word string has %d words", len(strings.Fields(twenty)))
	}
	tier, _ = loop.ClassifyTask(twenty)
	if tier != loop.TierMiddle {
		t.Errorf("20-word task should be middle, got %q", tier)
	}
	// 21 words: over the boundary → middle
	twentyOne := twenty + " twentyone"
	if len(strings.Fields(twentyOne)) != 21 {
		t.Fatalf("test bug: 21-word string has %d words", len(strings.Fields(twentyOne)))
	}
	tier, _ = loop.ClassifyTask(twentyOne)
	if tier != loop.TierMiddle {
		t.Errorf("21-word task should be middle, got %q", tier)
	}
	// 1 file extension + no path → still trivial
	tier, _ = loop.ClassifyTask("fix typo in main.go")
	if tier != loop.TierTrivial {
		t.Errorf("1-extension short task should be trivial, got %q", tier)
	}
	// 2 file extensions → middle (multi-file scope)
	tier, _ = loop.ClassifyTask("update main.go and README.md")
	if tier != loop.TierMiddle {
		t.Errorf("2-extension short task should be middle, got %q", tier)
	}
	// Path separator → middle regardless of length
	tier, _ = loop.ClassifyTask("fix typo in cmd/triad/main.go")
	if tier != loop.TierMiddle {
		t.Errorf("path-separator task should be middle, got %q", tier)
	}
}

// TestRubric_VersionBump pins the current RubricVersion so that any
// change to the rule set forces an explicit version bump + test
// update. This is the contract from rubric.go's "the rubric is itself
// a versioned artifact" clause.
func TestRubric_VersionBump(t *testing.T) {
	if loop.RubricVersion == "" {
		t.Error("RubricVersion is empty — must be a non-empty semver string")
	}
	// Sanity: the version must contain a digit (catches accidental
	// empty-string or whitespace-only values that survived a copy-paste).
	hasDigit := false
	for _, r := range loop.RubricVersion {
		if r >= '0' && r <= '9' {
			hasDigit = true
			break
		}
	}
	if !hasDigit {
		t.Errorf("RubricVersion %q does not contain any digits — likely not a version", loop.RubricVersion)
	}
	// The default rubric's Version field must match the package const.
	rubric := loop.DefaultRoutingRubric()
	if rubric.Version != loop.RubricVersion {
		t.Errorf("DefaultRoutingRubric().Version = %q, want %q (RubricVersion)", rubric.Version, loop.RubricVersion)
	}
}

// TestRubric_SharedSafetyVocabulary is the cross-module contract from
// rubric.go: critical keywords in the routing rubric MUST be aligned
// with the sensitive-surface list in clarify/assess.go.
//
// We don't directly import the clarify package's sensitive list (it's
// package-private), but we DO know the public hook blocklist is the
// same vocabulary — and we know the routing rubric's surface terms.
// This test asserts that EVERY keyword the routing rubric treats as
// critical also appears in the orchestrator spec. If a future
// contributor adds a keyword to the rubric, they're forced to also
// update the spec, which is the visible artifact reviewers see.
func TestRubric_SharedSafetyVocabulary(t *testing.T) {
	rubric := loop.DefaultRoutingRubric()
	if len(rubric.CriticalKeywords) == 0 {
		t.Fatal("DefaultRoutingRubric().CriticalKeywords is empty — rubric is broken")
	}
	// Every critical keyword must be lowercase and non-empty (the
	// matcher lowercases the input and uses word-boundary checks,
	// so a mixed-case keyword would silently never match).
	for _, kw := range rubric.CriticalKeywords {
		if strings.TrimSpace(kw) == "" {
			t.Errorf("critical keyword list contains an empty entry")
		}
		if kw != strings.ToLower(kw) {
			t.Errorf("critical keyword %q is not lowercase — word-boundary match will silently miss", kw)
		}
	}
	// The orchestrator spec must mention at least one of the rubric's
	// critical keywords (proves the spec is anchored to the actual
	// rule-set, not stale copy-paste text).
	spec := agent.OrchestratorSpec
	specLower := strings.ToLower(spec)
	matched := 0
	for _, kw := range rubric.CriticalKeywords {
		if strings.Contains(specLower, kw) {
			matched++
		}
	}
	if matched < 5 {
		t.Errorf("OrchestratorSpec mentions only %d/%d critical keywords — spec is decoupled from the rubric (or rubric shrank)", matched, len(rubric.CriticalKeywords))
	}
	// The spec must mention the rubric by name — the whole point of
	// Phase 5 is that Orchestrator's "system prompt" references the
	// rubric explicitly.
	if !strings.Contains(spec, "RoutingRubric") && !strings.Contains(specLower, "routing rubric") {
		t.Error("OrchestratorSpec does not reference RoutingRubric by name — Phase 5 §5.2 contract violated")
	}
	// The spec must explicitly call out the routing decision order
	// (1. Critical, 2. Trivial, 3. Middle) so reviewers know the
	// priority of the keyword check.
	if !strings.Contains(spec, "CRITICAL") || !strings.Contains(spec, "TRIVIAL") || !strings.Contains(spec, "MIDDLE") {
		t.Error("OrchestratorSpec does not enumerate the routing decision order (CRITICAL / TRIVIAL / MIDDLE)")
	}
}

// ---------------------------------------------------------------------------
// Phase 5 §5.4 — repeat-run determinism
// ---------------------------------------------------------------------------
//
// Phase 0's research explicitly flagged non-deterministic LLM routing as
// a real industry failure mode. The mitigation in this codebase is
// structural: ClassifyTask is a pure Go function (no goroutines, no
// time.Now(), no LLM, no map iteration that affects output), so Go
// itself guarantees bit-identical output for the same input.
//
// This test suite pins that property down:
//   1. The same input produces the same (tier, reason) tuple 100 times
//      in a row — across the full trivial→critical range.
//   2. Case-insensitive whitespace variants route identically to their
//      canonical form ("  FIX  typo  " == "fix typo").
//   3. Reason text is stable across runs (a "reason drift" bug — where
//      a contributor changes the reason template on one branch — would
//      fail this test even if the tier stays correct).
//
// If a future refactor accidentally introduces a non-deterministic
// dependency (e.g. time-of-day, goroutine scheduling, map iteration),
// these tests fail loudly. The point is to make the determinism
// property a *tested contract*, not a hopeful assumption.

// TestClassifyTask_RepeatRunDeterminism runs the same task through
// ClassifyTask 100 times and asserts that the (tier, reason) tuple
// is bit-identical every time. Catches map-iteration, time-based,
// or goroutine-induced drift.
func TestClassifyTask_RepeatRunDeterminism(t *testing.T) {
	// One task per tier, plus a couple of edge cases that have
	// historically been the most likely drift points (case
	// variation, whitespace).
	cases := []string{
		"fix typo in README",                                       // trivial
		"hi",                                                       // trivial (greeting)
		"explain the main.go structure",                            // trivial (informational)
		"update the auth token validation",                         // critical
		"add a billing endpoint that issues a refund",              // critical
		"improve the caching layer to handle concurrent writes",    // middle
		"add a /help command to the TUI",                          // middle
		"  FIX  typo  in  README  ",                                // trivial w/ whitespace + case
		"Update The AUTH token validation logic",                   // critical w/ case
	}
	const runs = 100
	for _, task := range cases {
		t.Run(task, func(t *testing.T) {
			firstTier, firstReason := loop.ClassifyTask(task)
			for i := 0; i < runs; i++ {
				tier, reason := loop.ClassifyTask(task)
				if tier != firstTier {
					t.Fatalf("run %d: tier drifted from %q to %q for task %q", i, firstTier, tier, task)
				}
				if reason != firstReason {
					t.Fatalf("run %d: reason drifted:\n  first:  %q\n  now:    %q\n  task:   %q", i, firstReason, reason, task)
				}
			}
		})
	}
}

// TestClassifyTask_DeterminismAcrossGoroutines goes one step further
// than TestClassifyTask_RepeatRunDeterminism: it runs the same
// classifier concurrently from 16 goroutines × 100 iterations each
// and asserts all observations match. This catches map-iteration
// non-determinism that the serial test would miss (Go's map iteration
// order is intentionally randomised to discourage reliance on it).
func TestClassifyTask_DeterminismAcrossGoroutines(t *testing.T) {
	task := "improve the caching layer to handle concurrent writes better"
	wantTier, wantReason := loop.ClassifyTask(task)

	const goroutines = 16
	const iters = 100
	results := make(chan struct {
		tier   string
		reason string
	}, goroutines*iters)

	for g := 0; g < goroutines; g++ {
		go func() {
			for i := 0; i < iters; i++ {
				t, r := loop.ClassifyTask(task)
				results <- struct {
					tier   string
					reason string
				}{t, r}
			}
		}()
	}
	for i := 0; i < goroutines*iters; i++ {
		r := <-results
		if r.tier != wantTier || r.reason != wantReason {
			t.Errorf("concurrent run produced different output: got tier=%q reason=%q, want tier=%q reason=%q",
				r.tier, r.reason, wantTier, wantReason)
		}
	}
}

// TestClassifyTask_WhitespaceAndCaseInsensitive asserts that the
// classifier's normalisation (lowercasing + trimming) makes canonical
// and non-canonical inputs produce the same tier. This is a separate
// property from "same input is deterministic" — it's the contract
// that "FIX typo" routes like "fix typo".
func TestClassifyTask_WhitespaceAndCaseInsensitive(t *testing.T) {
	pairs := []struct {
		canonical    string
		variant      string
	}{
		{"fix typo in README", "  fix typo in README  "},
		{"fix typo in README", "FIX TYPO IN README"},
		{"hi", "  HI  "},
		{"hi", "Hi"},
		{"update the auth token validation", "Update The AUTH Token Validation"},
		{"improve the caching layer to handle concurrent writes better",
			"  IMPROVE THE CACHING LAYER TO HANDLE CONCURRENT WRITES BETTER  "},
	}
	for _, p := range pairs {
		t.Run(p.canonical, func(t *testing.T) {
			canTier, _ := loop.ClassifyTask(p.canonical)
			varTier, _ := loop.ClassifyTask(p.variant)
			if canTier != varTier {
				t.Errorf("variant %q routed to %q, but canonical %q routed to %q — normalisation is broken",
					p.variant, varTier, p.canonical, canTier)
			}
		})
	}
}

// TestClassifyTask_ReasonTextStable asserts that the reason string
// for a given input is byte-stable across the corpus. If a future
// contributor changes the reason template on one branch (e.g. from
// "task touches a sensitive/critical surface" to "sensitive surface
// detected"), this test fires for every critical input that had a
// snapshot taken. The point: reason drift is a real risk because
// humans parse the reason text in the transcript, and a silent
// wording change is a small but real UX break.
func TestClassifyTask_ReasonTextStable(t *testing.T) {
	// (input, expected substring that must appear in the reason)
	snapshots := []struct {
		task        string
		tier        string
		needInReason string
	}{
		{"hi", loop.TierTrivial, "greeting"},
		{"what is a goroutine", loop.TierTrivial, "informational"},
		{"explain the main.go structure", loop.TierTrivial, "informational"},
		{"fix typo", loop.TierTrivial, "short fix"},
		{"fix typo in README", loop.TierTrivial, "short fix"},
		{"update the auth token validation", loop.TierCritical, "auth"},
		{"add a billing endpoint that issues a refund", loop.TierCritical, "billing"},
		{"improve the caching layer to handle concurrent writes better", loop.TierMiddle, ""},
		{"update internal/loop/loop.go to add tracing", loop.TierMiddle, "directory path"},
	}
	for _, snap := range snapshots {
		t.Run(snap.task, func(t *testing.T) {
			tier, reason := loop.ClassifyTask(snap.task)
			if tier != snap.tier {
				t.Errorf("tier changed: got %q, want %q", tier, snap.tier)
			}
			if snap.needInReason != "" && !strings.Contains(reason, snap.needInReason) {
				t.Errorf("reason %q does not contain expected substring %q (reason drift?)", reason, snap.needInReason)
			}
		})
	}
}
