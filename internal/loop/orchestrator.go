package loop

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/kaiizer777/triad/internal/transcript"
)

// Complexity tiers used by Orchestrator routing.
const (
	TierTrivial  = "trivial"
	TierCritical = "critical"
	TierMiddle   = "middle"
)

// orchestratorConfirm holds state for a pending middle-tier confirmation round.
// When set, the next human message is treated as a confirm/override reply rather
// than a fresh task.
type orchestratorConfirm struct {
	task     string // the original task text
	tier     string // always TierMiddle
	proposed string // the mode we proposed ("triad" for now; Phase 6 will use "twin")
}

// criticalKeywords are exact surface terms that always warrant full-Triad
// oversight regardless of task length. These deliberately mirror the sensitive
// surface list in clarify/assess.go and the hook blocklist described in
// Workflow 2 §3.2.3, so the two systems share the same safety vocabulary.
var criticalKeywords = []string{
	"auth", "authentication", "authorization",
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
}

// trivialPhrases are patterns that strongly suggest a one-liner or informational
// task that never needs Reviewer overhead.
var trivialPhrases = []string{
	"typo", "fix typo",
	"hello", "hi", "hey", "ping",
	"what is", "what's", "how do i", "how to", "explain", "tell me", "show me",
}

// ClassifyTask inspects a task description and returns one of the three routing
// tiers ("trivial", "critical", "middle") plus a short human-readable reason.
//
// Design constraints (from Phase 0 research):
//   - Trivial: genuinely single-file/one-liner tasks where Reviewer overhead is
//     pure waste. Conservative — false negatives (calling something trivial when
//     it isn't) are more expensive than false positives.
//   - Critical: any task touching auth, payments, deletion, sensitive credentials,
//     or architectural surgery — matches the hook blocklist from Workflow 2 §3.2.3.
//     More oversight is never the wrong call, so critical auto-proceeds to Triad.
//   - Middle: the genuine "I'm not sure" ground; Orchestrator must ask the human
//     to confirm or override rather than silently picking.
func ClassifyTask(task string) (tier, reason string) {
	lower := strings.ToLower(strings.TrimSpace(task))
	words := strings.Fields(lower)

	if lower == "" {
		return TierTrivial, "empty task — treat as trivial no-op"
	}

	// --- Critical check (highest priority) ---
	// If ANY critical keyword appears as a whole word/phrase, route to Triad.
	for _, kw := range criticalKeywords {
		if containsWordOr(lower, kw) {
			return TierCritical, fmt.Sprintf("task touches a sensitive/critical surface (%q)", kw)
		}
	}

	// --- Trivial check ---
	// Informational / conversational openers → always trivial.
	for _, p := range trivialPhrases {
		if lower == p || strings.HasPrefix(lower, p+" ") || strings.HasPrefix(lower, p+",") {
			return TierTrivial, fmt.Sprintf("conversational/informational request (%q opener)", p)
		}
	}

	// Short word count is the strongest trivial signal: genuine one-liners rarely
	// exceed 6 words ("fix typo in README", "rename X to Y").
	if len(words) <= 6 {
		extCount := countFileExtensions(lower)
		hasPaths := strings.Contains(lower, "/") || strings.Contains(lower, `\`)
		if extCount <= 1 && !hasPaths {
			return TierTrivial, "short, focused task (≤6 words, single target) — minimal overhead needed"
		}
	}

	// --- Middle: everything else ---
	// Long description or multi-file scope → human must confirm.
	if len(words) > 20 || len(lower) > 120 {
		return TierMiddle, "task is lengthy / potentially multi-file — scope needs confirmation"
	}

	extCount := countFileExtensions(lower)
	if extCount >= 2 {
		return TierMiddle, "task touches multiple files — routing confirmation needed"
	}

	hasPaths := strings.Contains(lower, "/") || strings.Contains(lower, `\`)
	if hasPaths {
		return TierMiddle, "task references directory paths — may touch multiple files"
	}

	return TierMiddle, "task complexity is ambiguous — routing confirmation needed"
}

// OrchestratorMessage formats the mandatory stated-reasoning message that
// Orchestrator emits BEFORE acting on any routing decision (requirement 4.2).
// This message is always appended to the transcript — even on "obvious"
// auto-proceed cases. It is never optional.
func OrchestratorMessage(tier, reason, targetMode string) string {
	switch tier {
	case TierTrivial:
		return fmt.Sprintf("[Orchestrator]: This looks like a trivial task (%s) — routing to General Chat.", reason)
	case TierCritical:
		return fmt.Sprintf("[Orchestrator]: This looks like a critical task (%s) — routing to Triad for full oversight.", reason)
	default:
		// Middle tier — Orchestrator proposes and asks for confirmation.
		return fmt.Sprintf(
			"[Orchestrator]: I'd route this to full Triad (medium complexity — %s). "+
				"Proceed, or would you prefer to override the mode? (reply to confirm, or use /mode to override)",
			reason,
		)
	}
}

// AppendRoutingDecision appends a TypeRoutingDecision entry to the loop's
// transcript with the full structured payload (requirement 4.5). This is the
// machine-readable counterpart to OrchestratorMessage's human-readable text.
func (l *Loop) AppendRoutingDecision(task, tier, targetMode, reason string, autoProceed bool) error {
	rd := transcript.RoutingDecision{
		Task:            task,
		ComplexityJudge: tier,
		TargetMode:      targetMode,
		AutoProceeded:   autoProceed,
		Reason:          reason,
	}
	data, err := json.Marshal(rd)
	if err != nil {
		return fmt.Errorf("orchestrator: failed to marshal routing decision: %w", err)
	}
	return l.append(transcript.Entry{
		Speaker:   transcript.SpeakerSystem,
		Type:      transcript.TypeRoutingDecision,
		Content:   string(data),
		Timestamp: time.Now(),
	})
}

// runOrchestratorRouting is the orchestrator gate called inside Run() after the
// clarify phase resolves (requirement 4.6 — clarify first, then route).
//
// Returns:
//   - effectiveMode: the mode to use for the active cycle (only valid when !waitingForConfirm)
//   - waitingForConfirm: true when a middle-tier confirmation round was started;
//     caller should continue to the top of Run() and wait for the human's reply
//   - err: non-nil on append failure
func (l *Loop) runOrchestratorRouting(task string) (effectiveMode Mode, waitingForConfirm bool, err error) {
	tier, reason := ClassifyTask(task)

	switch tier {
	case TierTrivial:
		// Auto-proceed to General Chat. State the reasoning, log the decision.
		msg := OrchestratorMessage(tier, reason, string(ModeGeneral))
		if appendErr := l.append(transcript.Entry{
			Speaker:   transcript.SpeakerSystem,
			Type:      transcript.TypeMessage,
			Content:   msg,
			Timestamp: time.Now(),
		}); appendErr != nil {
			return "", false, appendErr
		}
		if appendErr := l.AppendRoutingDecision(task, tier, string(ModeGeneral), reason, true); appendErr != nil {
			return "", false, appendErr
		}
		return ModeGeneral, false, nil

	case TierCritical:
		// Auto-proceed to Triad. State the reasoning, log the decision.
		msg := OrchestratorMessage(tier, reason, string(ModeTriad))
		if appendErr := l.append(transcript.Entry{
			Speaker:   transcript.SpeakerSystem,
			Type:      transcript.TypeMessage,
			Content:   msg,
			Timestamp: time.Now(),
		}); appendErr != nil {
			return "", false, appendErr
		}
		if appendErr := l.AppendRoutingDecision(task, tier, string(ModeTriad), reason, true); appendErr != nil {
			return "", false, appendErr
		}
		return ModeTriad, false, nil

	default: // TierMiddle
		// Ask the human to confirm before proceeding. State the reasoning first,
		// then set pendingOrchestratorConfirm and return waitingForConfirm=true.
		// The routing_decision entry will be appended when the human confirms
		// (with auto_proceeded=false to record it was human-confirmed).
		msg := OrchestratorMessage(tier, reason, string(ModeTriad))
		if appendErr := l.append(transcript.Entry{
			Speaker:   transcript.SpeakerSystem,
			Type:      transcript.TypeMessage,
			Content:   msg,
			Timestamp: time.Now(),
		}); appendErr != nil {
			return "", false, appendErr
		}
		l.pendingOrchestratorConfirm = &orchestratorConfirm{
			task:     task,
			tier:     TierMiddle,
			proposed: string(ModeTriad),
		}
		return "", true, nil
	}
}

// resolveOrchestratorConfirm handles a human reply to a middle-tier confirmation
// round. It appends the routing_decision entry (with auto_proceeded=false) and
// returns the effective mode to use. Any non-empty reply is treated as "proceed".
// If the reply is a /mode override, the active mode is set accordingly.
func (l *Loop) resolveOrchestratorConfirm(reply string) (effectiveMode Mode, err error) {
	confirm := l.pendingOrchestratorConfirm
	l.pendingOrchestratorConfirm = nil

	// Check if the human overrode the mode via /mode command.
	targetMode := Mode(confirm.proposed) // default: Triad
	lowerReply := strings.ToLower(strings.TrimSpace(reply))
	if strings.HasPrefix(lowerReply, "/mode ") {
		modeStr := strings.TrimPrefix(lowerReply, "/mode ")
		if parsed, parseErr := ParseMode(modeStr); parseErr == nil {
			// Honour the override, but don't let them switch to orchestrator
			// inside an orchestrator routing cycle — that would be a no-op loop.
			if parsed != ModeOrchestrator {
				targetMode = parsed
			}
		}
	}

	// Record the human-confirmed routing decision.
	if appendErr := l.AppendRoutingDecision(confirm.task, confirm.tier, string(targetMode), "human confirmed routing decision", false); appendErr != nil {
		return "", appendErr
	}

	// Ack so the transcript shows the decision was received.
	ack := fmt.Sprintf("[Orchestrator]: Confirmed — routing to %s.", targetMode)
	if appendErr := l.append(transcript.Entry{
		Speaker:   transcript.SpeakerSystem,
		Type:      transcript.TypeMessage,
		Content:   ack,
		Timestamp: time.Now(),
	}); appendErr != nil {
		return "", appendErr
	}

	return targetMode, nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// containsWordOr checks whether needle appears in s as a whole word (single-word
// needles, using letter-boundary logic), or as a phrase (multi-word needles,
// simple substring match).
func containsWordOr(s, needle string) bool {
	if needle == "" {
		return false
	}
	// Multi-word needles (e.g. "api key"): space-separated substring match.
	if strings.Contains(needle, " ") {
		return strings.Contains(s, needle)
	}
	// Single-word: require word boundaries.
	idx := 0
	for {
		i := strings.Index(s[idx:], needle)
		if i < 0 {
			return false
		}
		i += idx
		leftOK := i == 0 || !isAlpha(s[i-1])
		rightIdx := i + len(needle)
		rightOK := rightIdx >= len(s) || !isAlpha(s[rightIdx])
		if leftOK && rightOK {
			return true
		}
		idx = rightIdx
	}
}

func isAlpha(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// countFileExtensions returns how many distinct file extension strings appear in s.
func countFileExtensions(s string) int {
	exts := []string{
		".go", ".ts", ".tsx", ".js", ".jsx", ".py", ".md",
		".txt", ".json", ".yaml", ".yml", ".html", ".css",
		".rs", ".java", ".kt", ".swift", ".c", ".cpp", ".h",
		".hpp", ".sh", ".sql",
	}
	count := 0
	for _, ext := range exts {
		if strings.Contains(s, ext) {
			count++
		}
	}
	return count
}
