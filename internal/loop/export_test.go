package loop

// export_test.go
//
// Test-only accessors for the loop's internals. This file is
// compiled only when running tests (`_test.go` suffix) and lives
// in the loop package (not loop_test) so it can read unexported
// fields. The public API surface stays clean — these helpers
// are not visible to production callers.
//
// The naming convention `FooForTest` is the idiomatic Go pattern
// for this: it makes the test-only nature obvious and lets
// `go vet` / `staticcheck` flag any production code that
// accidentally imports the symbol.

import (
	"github.com/kaiizer777/triad/internal/agent"
	"github.com/kaiizer777/triad/internal/transcript"
)

// ExtractPlanItemIDForTest exposes extractPlanItemID for tests.
func ExtractPlanItemIDForTest(tc agent.ToolCall, plan *transcript.Plan) (int, bool) {
	return extractPlanItemID(tc, plan)
}

// HeuristicBindPlanItemForTest exposes heuristicBindPlanItem.
func HeuristicBindPlanItemForTest(plan *transcript.Plan) (int, bool) {
	return heuristicBindPlanItem(plan)
}

// SeedPendingPlanForTest sets l.pendingPlan directly. Used by
// the unit tests to skip the recovery path.
func SeedPendingPlanForTest(l *Loop, plan *transcript.Plan) error {
	l.pendingPlan = plan
	return nil
}

// MarkPlanItemInProgressForTest exposes markPlanItemInProgress.
func MarkPlanItemInProgressForTest(l *Loop, id int) error {
	return l.markPlanItemInProgress(id)
}

// MarkPlanItemDoneForTest exposes markPlanItemDone.
func MarkPlanItemDoneForTest(l *Loop, id int) error {
	return l.markPlanItemDone(id)
}

// PendingPlanForTest returns the loop's pendingPlan.
func PendingPlanForTest(l *Loop) *transcript.Plan {
	return l.pendingPlan
}

// RecoverPendingPlanForTest triggers the recovery path that
// runActiveCycle would invoke on entry.
func RecoverPendingPlanForTest(l *Loop) error {
	if l.pendingPlan == nil {
		l.pendingPlan = LatestApprovedPlan(l.transcript.Entries())
	}
	return nil
}

// WorkDir returns the loop's work directory.
func (l *Loop) WorkDir() string {
	return l.workDir
}

