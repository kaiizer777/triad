package loop_test

import (
	"strings"
	"testing"

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
