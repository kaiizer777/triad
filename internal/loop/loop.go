// Package loop implements the core Coder→Reviewer approval loop for the Triad session.
//
// The loop drives the headless (no TUI) coder-proposes/reviewer-checks/execute-or-revise
// cycle described in PROJECT_SPEC.md §6.3. It is designed to be wired up by main.go for
// Phase 4, and later adapted (not directly ported) into bubbletea Commands for Phase 6.
package loop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/kaiizer777/triad/internal/agent"
	"github.com/kaiizer777/triad/internal/browser"
	"github.com/kaiizer777/triad/internal/clarify"
	"github.com/kaiizer777/triad/internal/gitcommit"
	"github.com/kaiizer777/triad/internal/learn"
	"github.com/kaiizer777/triad/internal/memory"
	"github.com/kaiizer777/triad/internal/skills"
	"github.com/kaiizer777/triad/internal/subagent"
	"github.com/kaiizer777/triad/internal/tracelog"
	"github.com/kaiizer777/triad/internal/transcript"
	"github.com/kaiizer777/triad/internal/twinsubagent"
)

// SessionState represents whether the loop is waiting for work or actively processing a task.
type SessionState int

const (
	// StateIdle means the session is waiting for the next human task.
	StateIdle SessionState = iota
	// StateActive means the coder/reviewer cycle is running.
	StateActive
	// StateAskQuestion means the session is blocked waiting for human answers to an ask_question batch.
	StateAskQuestion
)

// Decision is the result of parsing Reviewer's plain-text response.
type Decision int

const (
	DecisionApprove Decision = iota
	DecisionObject
)

// DefaultMaxRetries is the default cap on propose→object cycles per atomic action.
const DefaultMaxRetries = 5

// MaxRecoveryAttempts is the Phase 3.5 cap on recovery attempts per
// browser action. This counts both the deterministic attempt (3.2) and
// the model-assisted attempt (3.3) — so a value of 2 means one of
// each. A genuinely broken page won't spin past this cap.
const MaxRecoveryAttempts = 2

// Mode represents the top-level orchestration mode.
type Mode string

const (
	// ModeOrchestrator is the default session mode where an Orchestrator routes tasks (defaults to Triad in Phase 1).
	ModeOrchestrator Mode = "orchestrator"
	// ModeGeneral is a single-agent path with Coder only (no Reviewer, no approval loop).
	ModeGeneral Mode = "general"
	// ModeTriad is the full propose→review→execute loop with Reviewer veto power.
	ModeTriad Mode = "triad"
)

// ParseMode parses a raw mode string (case-insensitive).
func ParseMode(raw string) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "orchestrator":
		return ModeOrchestrator, nil
	case "general":
		return ModeGeneral, nil
	case "triad":
		return ModeTriad, nil
	default:
		return "", fmt.Errorf("unknown mode %q (must be orchestrator, general, or triad)", raw)
	}
}

// AgentClient is the interface the loop uses to call an agent.
// Using an interface here lets loop_test.go inject a mock without network calls.
type AgentClient interface {
	Respond(ctx context.Context, cfg agent.AgentConfig, entries []transcript.Entry) (agent.AgentResponse, error)
	ListModels(ctx context.Context, cfg agent.AgentConfig) ([]agent.ModelInfo, error)
	ListAllModels(ctx context.Context, cfg *agent.Config) ([]agent.AnnotatedModel, []agent.ModelError)
}

// Loop orchestrates the Coder/Reviewer approval cycle over a shared Transcript.
type Loop struct {
	transcript *transcript.Transcript
	coder      agent.AgentConfig
	reviewer   agent.AgentConfig
	client     AgentClient
	workDir    string

	// MaxRetries caps the propose→object cycles on a single atomic action.
	// When the cap is reached, a System entry is appended and the loop
	// surfaces the deadlock to the human rather than spinning forever.
	MaxRetries int

	// OnEntry is called after every entry is appended to the transcript.
	// main.go uses this to print each entry to stdout as it arrives.
	// nil is safe — the callback is skipped when not set.
	OnEntry func(e transcript.Entry)

	// InputChan receives human messages typed mid-cycle (Phase 5).
	// drainInput() reads this channel non-blockingly before each agent turn
	// so that any message the user typed is appended to the transcript
	// immediately — both agents see it on their very next API call.
	// nil is safe — drainInput() is a no-op when InputChan is nil.
	InputChan <-chan string

	// Browser is the long-lived Playwright manager for the browser_*
	// tool calls. It is set via SetBrowser after construction; nil
	// means browser tools are unavailable and any approved browser_*
	// tool call will surface a clear error rather than crashing the
	// session. Browser tool calls are routed through this manager in
	// runReviewCycle, the same way spawn_subagent is — they go
	// through the same propose→Reviewer→execute approval loop as
	// write_file / run_command, just with a different executor
	// (docs/work2.md §4.2).
	Browser *browser.Manager

	// recoveryAttempts tracks Phase 3 recovery attempts per tool call
	// ID. The key is the tool call ID; the value is the number of
	// recovery attempts made so far. When the value reaches
	// MaxRecoveryAttempts (2), no further recovery is attempted and
	// the error is surfaced to Coder normally. This map is cleared
	// when an action is approved (the ID changes) or when a new
	// active cycle starts.
	recoveryAttempts map[string]int

	// CurrentMode controls task execution mode (orchestrator | general | triad).
	CurrentMode Mode

	// SearchAPIKey holds the Firecrawl API key used by the web_search tool.
	SearchAPIKey string

	// non-proceed reply, and at the end of every active cycle.
	//
	// Phase 3 (docs/x.md §Phase 3): shared by General Chat and
	// Triad. Orchestrator and Twin Subagent wire into the same
	// step in their own phases.
	pendingClarify *clarify.Batch

	// pendingOrchestratorConfirm is set when Orchestrator has asked the human
	// to confirm or override a middle-tier routing decision (Phase 4, §4.4).
	// When non-nil, the next human message is treated as a confirm/override
	// reply rather than a fresh task. The clarify phase is skipped for this
	// reply — we don't want to re-clarify the confirmation itself.
	// Cleared after the reply is resolved in resolveOrchestratorConfirm.
	pendingOrchestratorConfirm *orchestratorConfirm

	// activeCycleTask is the original task for the currently active cycle.
	// Clarification replies and orchestrator confirmation replies are human
	// messages too, but they must not replace the task being classified or
	// resumed by the cycle.
	activeCycleTask string

	// effectiveMode is the mode used for the current active cycle. It is set
	// by the orchestrator routing gate (for ModeOrchestrator tasks) or
	// directly from CurrentMode (for manually forced modes). runActiveCycle
	// reads this field — not CurrentMode — so Orchestrator can redirect a
	// single task to General or Triad without changing the session-level mode.
	effectiveMode Mode

	// Memory handles index, topics, preferences, and daily logs (Phase 8).
	Memory *memory.Manager

	// Learn handles self-learning active extraction and promotion (Phase 9).
	Learn *learn.Service

	// SkillsRegistry is the loaded skills directory (Workflow 5,
	// internal/skills). When non-nil and non-empty, the loop runs
	// the Stage-1 / Stage-2 selection funnel on every Coder turn
	// before the API call: it injects the bare section-label list
	// into Coder's system prompt (Stage 1, cheap), parses
	// Coder's SELECTED_SECTIONS line out of its response, and
	// injects the selected skill bodies (Main on first touch, Mini
	// on subsequent) on the next turn (Stage 2, bounded by the
	// 3-section cap). When nil or empty, the funnel is a no-op
	// and Coder sees the unmodified system prompt.
	SkillsRegistry *skills.Registry

	// loadedSkills tracks which sections have already had their
	// Main Skill fire this session. Persisted for the lifetime of
	// the Loop. Cleared implicitly when the process restarts;
	// work.md §8 flags that future compaction may want to reset
	// this too, but no compaction is implemented yet.
	loadedSkills *skills.LoadedSet

	// pendingPlan is the most recent plan the plan-first gate
	// approved for the current active cycle. Nil means no plan
	// has been approved yet (Coder still owes one). The gate
	// rejects non-submit_plan tool calls when pendingPlan is nil
	// and the gate is active. Cleared by clearCycleState at the
	// end of every active cycle.
	pendingPlan *transcript.Plan

	// planBypassed records that the plan gate was intentionally
	// skipped for the current cycle (because planGateDisabled is
	// true, or because the task classifies as trivial). When
	// true, the gate does not reject tool calls and Coder can
	// proceed without calling submit_plan. Cleared at the end of
	// every active cycle.
	planBypassed bool

	// planPreTextCount counts consecutive plain-text (no tool
	// call) Coder responses observed AFTER the cycle started and
	// BEFORE pendingPlan is set. The gate trips a stall guard
	// once the count exceeds maxPlanPreTextMessages so Coder
	// can't keep "thinking" forever without committing to a plan.
	planPreTextCount int

	// planGateDisabled controls whether the plan-first gate is
	// enforced at all. When true (the default), the gate is a
	// no-op: every cycle behaves exactly like the pre-Phase-6
	// headless loop, which keeps every pre-existing test passing
	// without per-test opt-out wiring. Tests that exercise the
	// gate must explicitly call SetPlanGateDisabled(false) on
	// their loop instance. Production goes through the TUI which
	// has its own always-on gate in update.go.
	planGateDisabled bool
}

// MaxPlanPreTextMessages is the cap on consecutive plain-text
// Coder responses the plan gate will tolerate before tripping a
// stall. One planning message is normal; two is starting to stall;
// three is the explicit "stop thinking and submit a plan" trip.
// The value lives in loop (not rubric) because it is a hard cap
// enforced by the gate, not a tier-classification signal.
const MaxPlanPreTextMessages = 1

// New creates a Loop ready to run. workDir is the project root used for tool execution.
func New(
	t *transcript.Transcript,
	coder agent.AgentConfig,
	reviewer agent.AgentConfig,
	client AgentClient,
	workDir string,
) *Loop {
	return &Loop{
		transcript:       t,
		coder:            coder,
		reviewer:         reviewer,
		client:           client,
		workDir:          workDir,
		MaxRetries:       DefaultMaxRetries,
		CurrentMode:      ModeOrchestrator,
		recoveryAttempts: make(map[string]int),
		loadedSkills:     skills.NewLoadedSet(),
		// Plan gate is opt-in for the headless loop (see Phase 6.3
		// design note in issue.md). The default of true keeps every
		// pre-existing test passing without per-test opt-out calls.
		// Tests that exercise the gate must call
		// SetPlanGateDisabled(false) on their loop instance.
		planGateDisabled: true,
	}
}

// SetPlanGateDisabled toggles the plan-first gate for this Loop
// instance. Pass false to enable the gate (Coder must call
// submit_plan before any other tool call when the task classifies
// as needing a plan). Pass true to disable the gate (the default).
//
// The default in New is true so pre-existing tests keep passing
// without per-test opt-out wiring. Production goes through the TUI
// which has its own always-on gate in update.go — the headless
// gate is only used by tests that explicitly opt in.
//
// This is intentionally a method, not a struct-field flip, because
// the gate's lifecycle is per-session: toggling it at any time
// (not just construction) keeps the option open for future
// per-mode policies (e.g. "only enable when explicit --plan flag
// is set on the command line").
func (l *Loop) SetPlanGateDisabled(disabled bool) {
	l.planGateDisabled = disabled
}

// PlanGateDisabled reports whether the plan-first gate is currently
// disabled. Exposed mainly for tests and observability; production
// callers should not need to read this — they should just set it.
func (l *Loop) PlanGateDisabled() bool {
	return l.planGateDisabled
}

// clearCycleState resets the per-active-cycle plan state. Called
// via defer at the top of runActiveCycle so every cycle starts
// from a known-good baseline regardless of how the previous cycle
// ended (clean completion, deadlock, error, etc.).
func (l *Loop) clearCycleState() {
	l.pendingPlan = nil
	l.planBypassed = false
	l.planPreTextCount = 0
}

// SetSearchAPIKey sets the Firecrawl API key used by web_search tool calls.
func (l *Loop) SetSearchAPIKey(key string) {
	l.SearchAPIKey = key
}

// SetBrowser attaches a browser.Manager to the loop so that approved
// browser_* tool calls can be executed.
func (l *Loop) SetBrowser(m *browser.Manager) {
	l.Browser = m
}

// SetMemory attaches a memory.Manager and initializes learn.Service for the loop.
func (l *Loop) SetMemory(m *memory.Manager) {
	l.Memory = m
	if m != nil {
		s, _ := learn.NewService(m)
		l.Learn = s
	}
}

// SetSkillsRegistry attaches a loaded skills.Registry to the loop,
// enabling the Stage-1 / Stage-2 selection funnel on every Coder
// turn. Pass nil to disable the funnel (Coder then sees the
// unmodified base system prompt).
//
// The loaded-skills set (which sections have already had Main fire
// this session) is created lazily here if it wasn't set during
// New() — that way a Loop constructed before SetSkillsRegistry
// (e.g. in tests) still gets a valid set the moment skills are
// attached, instead of panicking on the first Coder turn.
func (l *Loop) SetSkillsRegistry(reg *skills.Registry) {
	l.SkillsRegistry = reg
	if l.loadedSkills == nil {
		l.loadedSkills = skills.NewLoadedSet()
	}
}

// LoadedSkills exposes the loop's per-session loaded-skills set,
// mainly for tests and observability. Returns nil if the funnel
// was never activated (no SetSkillsRegistry call).
func (l *Loop) LoadedSkills() *skills.LoadedSet {
	return l.loadedSkills
}

// buildCoderConfigWithSkills returns a copy of l.coder with the
// Stage-1 (bare section labels, mandatory) and Stage-2 (Mini bodies
// for every section that has already had its Main fire this session)
// prompts appended to SystemPrompt for THIS turn only. The copy is
// required so we don't accumulate bodies across turns and break the
// "Main fires once per session" invariant — l.coder stays clean.
//
// When no skills registry is attached or the registry is empty,
// this returns the unchanged l.coder. Reviewer and Orchestrator
// paths never call this; the funnel is Coder-only (work.md §3).
//
// Per work.md §5 step 3, the second and subsequent touches of a
// section this session inject the Mini body — that's all this
// helper ever emits. The Main body was decided on the turn
// ApplySelection ran (the turn Coder emitted SELECTED_SECTIONS),
// which is the turn *before* this prompt is built; the loop
// never needs to inject Main into a prompt directly, because
// "loaded" already means "Main was decided last turn" and from
// here on out Mini is the right shape.
func (l *Loop) buildCoderConfigWithSkills() agent.AgentConfig {
	cfg := l.coder // value copy
	cfg.SystemPrompt = cfg.SystemPrompt + skills.BuildCoderSystemPromptExtension(l.SkillsRegistry, l.loadedSkills)
	return cfg
}

// skillsBodiesForPrompt returns the Stage-2 body block: the Mini
// body of every section that has been selected this session.
// Thin wrapper around skills.BuildLoadedBodies — kept as a
// method on Loop so the call site reads naturally. The real
// implementation lives in the skills package and is shared
// with the TUI path so the prompt shape is identical across
// the two Coder call sites (work.md §3: Coder gets skill
// content regardless of headless vs TUI mode).
func (l *Loop) skillsBodiesForPrompt() string {
	return skills.BuildLoadedBodies(l.SkillsRegistry, l.loadedSkills)
}

// coderTurnWithFunnel is the Workflow 5 funnel wrapper around a
// Coder API call. It is the single entry point the loop uses for
// every Coder turn (initial + post-objection revision). The split
// is:
//
//  1. Pre-call: build the Coder config with the funnel's Stage 1
//     (bare section labels) + Stage 2 (Mini bodies for already-
//     loaded sections) appended to the system prompt. The base
//     Coder system prompt is never mutated — the modification is
//     per-turn, applied to a value copy of l.coder.
//
//  2. Call: hand the modified config + the current transcript to
//     client.Respond. The transcript is unchanged so the existing
//     prepare-entries logic in client.go works without a peephole
//     edit; the funnel's effects land in the SystemPrompt field
//     and ride along as the first chat message.
//
//  3. Post-call: if Coder returned plain text (no tool call), run
//     the response through skills.ParseSelection to extract the
//     SELECTED_SECTIONS line, then ApplySelection to mark loaded
//     and log the system entry. The cleaned response text (with
//     the SELECTED_SECTIONS line stripped) is what the loop then
//     appends to the transcript as the [Coder] entry, so the
//     human / Reviewer / Phase-4 observability layer see a clean
//     Coder message without the control prefix.
//
// If Coder returned one or more tool calls (the common case
// after the first turn of a cycle), there's no plain text to
// parse — the SELECTED_SECTIONS declaration is implicitly skipped
// this turn. The loaded set from the prior turn's selection is
// still in effect, and the next text-bearing Coder turn can
// re-declare if the task shifts.
//
// Reviewer and Orchestrator never go through this helper.
// work.md §3: only Coder (and coding subagents — see the
// subagent package) receive skill content. Regression check is
// the fact that this method is only called from Coder paths.
// MaxSelectionStallRetries is the cap on forced re-prompts when
// Coder's turn ends without completing mandatory skill selection.
// The first attempt is the initial Coder call; each subsequent
// attempt is a forced re-prompt with a nudge injected into the
// transcript. When the cap is exhausted, coderTurnWithFunnel
// returns an error instead of silently idling.
//
// The value 2 means "retry up to 2 times after the initial
// attempt" — so 3 total attempts. This matches the issue.md
// Phase 2 requirement of "cap forced re-prompts (e.g. max 2)".
const MaxSelectionStallRetries = 2

// coderTurnWithFunnel executes a Coder API call with mandatory skill
// selection enforcement. When skills are configured and selection is
// required, the method:
//
//  1. Disables tools on the Coder config so the model can't bypass
//     selection by calling write_file/run_command.
//  2. Checks the response for a valid SELECTED_SECTIONS line.
//  3. If selection is still required after the response, emits a
//     trace event and a transcript nudge before re-prompting.
//  4. Caps re-prompts at MaxSelectionStallRetries to prevent
//     infinite stalls.
//
// On cap exhaustion, returns a clear error that surfaces to the
// human via the loop's error handling.
func (l *Loop) coderTurnWithFunnel(ctx context.Context) (agent.AgentResponse, error) {
	maxAttempts := 1 + MaxSelectionStallRetries
	for attempt := 0; attempt < maxAttempts; attempt++ {
		cfg := l.buildCoderConfigWithSkills()
		// Selection is a protocol message, not work. Removing tool schemas
		// here stops tool-capable models from trying to act before choosing
		// their section and skill.
		if l.loadedSkills != nil && l.loadedSkills.SelectionRequired() {
			cfg.HasTools = false
			cfg.Tools = nil
		}
		resp, err := l.client.Respond(ctx, cfg, l.transcript.Entries())
		if err != nil {
			return resp, fmt.Errorf("coder API call failed: %w", err)
		}
		if l.SkillsRegistry == nil || l.SkillsRegistry.Count() == 0 {
			return resp, nil
		}
		if len(resp.ToolCalls) == 0 {
			cleaned, _ := skills.ParseAndApply(resp.Text, l.SkillsRegistry, l.loadedSkills, l.transcript, l.activeTaskForCycle())
			resp.Text = cleaned
		}
		if !l.loadedSkills.SelectionRequired() {
			return resp, nil
		}
		// Selection still required — Coder didn't comply.
		reason := "response did not contain a valid SELECTED_SECTIONS line"
		if len(resp.ToolCalls) > 0 {
			reason = "tool calls emitted before selection completed"
		} else if strings.TrimSpace(resp.Text) == "" {
			reason = "empty response during mandatory selection"
		}
		l.logSelectionStall(attempt, maxAttempts, reason)
	}
	return agent.AgentResponse{}, fmt.Errorf("coder did not complete mandatory skill selection after %d attempts — session stalled. "+
		"The model consistently failed to emit a SELECTED_SECTIONS line. Human intervention required.", maxAttempts)
}

// logSelectionStalled surfaces a skill selection stall to both the
// session trace log and the transcript. The trace event is
// machine-readable (EventSkillSelectionStalled); the transcript
// entry is human-readable and tells the human the loop is actively
// retrying rather than silently stuck.
func (l *Loop) logSelectionStall(attempt, maxAttempts int, reason string) {
	// Trace log (machine-readable, visible via /trace).
	tracePath := tracelog.TracePathForSession(l.transcript.FilePath())
	_ = tracelog.Append(tracePath, tracelog.Entry{
		Entity:    "skills",
		EventType: tracelog.EventSkillSelectionStalled,
		Description: fmt.Sprintf("skill selection stalled (attempt %d/%d): %s",
			attempt+1, maxAttempts, reason),
	})

	// Transcript (human-visible, appears in the session log).
	_ = l.append(transcript.Entry{
		Speaker:   transcript.SpeakerSystem,
		Type:      transcript.TypeMessage,
		Content:   fmt.Sprintf("[Skills] Selection stall (attempt %d/%d): %s. Re-prompting Coder to select sections.", attempt+1, maxAttempts, reason),
		Timestamp: time.Now(),
	})
}

// activeTaskForCycle returns the original task for the current cycle. A
// direct/resumed cycle may not have populated activeCycleTask yet, so retain
// the transcript-based fallback for that case.
func (l *Loop) activeTaskForCycle() string {
	if l.activeCycleTask != "" {
		return l.activeCycleTask
	}
	return l.mostRecentHumanTask()
}

// mostRecentHumanTask returns the most recent You (human) message
// in the transcript, truncated. Used by the funnel to include a
// short task excerpt in the [Skills] system entry so the
// observability layer can correlate skill choices with the task
// that triggered them. Returns "" if no human message exists yet
// (e.g. resume before any You entry).
func (l *Loop) mostRecentHumanTask() string {
	entries := l.transcript.Entries()
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Speaker == transcript.SpeakerYou {
			return entries[i].Content
		}
	}
	return ""
}

// AutoExtractLearnings triggers auto-extraction of candidate learnings from transcript entries into daily log.
// NO code path in AutoExtractLearnings ever writes to topics/*.md or INDEX.md.
func (l *Loop) AutoExtractLearnings() ([]learn.Item, error) {
	if l.Memory == nil {
		mgr, err := memory.NewManager(l.workDir)
		if err != nil {
			return nil, err
		}
		if l.transcript != nil {
			mgr.WithTracePath(tracelog.TracePathForSession(l.transcript.FilePath()))
		}
		l.Memory = mgr
	}
	if l.Learn == nil {
		svc, err := learn.NewService(l.Memory)
		if err != nil {
			return nil, err
		}
		if l.transcript != nil {
			svc.WithTracePath(tracelog.TracePathForSession(l.transcript.FilePath()))
		}
		l.Learn = svc
	}
	return l.Learn.AutoExtractAndLog(l.transcript.Entries(), time.Now())
}

// InitSessionMemory loads INDEX.md (and ONLY INDEX.md) into context at session start.
func (l *Loop) InitSessionMemory() error {
	if l.Memory == nil {
		mgr, err := memory.NewManager(l.workDir)
		if err != nil {
			return fmt.Errorf("loop: failed to initialize memory manager: %w", err)
		}
		l.Memory = mgr
	}

	// Check if INDEX.md is already loaded in transcript to avoid duplicate injection
	for _, e := range l.transcript.Entries() {
		if e.Speaker == transcript.SpeakerSystem && strings.Contains(e.Content, "[Memory Index]") {
			return nil
		}
	}

	indexContent, err := l.Memory.LoadIndex()
	if err != nil {
		return fmt.Errorf("loop: failed to load INDEX.md at session start: %w", err)
	}

	sysEntry := transcript.Entry{
		Speaker:   transcript.SpeakerSystem,
		Type:      transcript.TypeMessage,
		Content:   fmt.Sprintf("[Memory Index]: Loaded INDEX.md:\n%s", indexContent),
		Timestamp: time.Now(),
	}

	return l.append(sysEntry)
}

// Run is the main blocking loop. It reads human tasks from taskChan and processes them
// through the Coder/Reviewer cycle until ctx is cancelled.
//
// taskChan should be fed by a goroutine reading stdin (Phase 5) or a bubbletea Cmd (Phase 6).
// When idle, each received string starts a new active cycle (task-start role).
// When active, messages are consumed by drainInput() at the top of each agent turn via
// InputChan — so the same channel serves both roles without double-processing:
// Run()'s select only fires when runActiveCycle() returns (i.e. the loop is back to idle),
// while drainInput() reads mid-cycle messages during the blocking active phase.
//
// This means InputChan and taskChan should be wired to the same underlying channel.
func (l *Loop) Run(ctx context.Context, taskChan <-chan string) error {
	state := StateIdle

	// Initialize session memory (load INDEX.md alone into context at start)
	_ = l.InitSessionMemory()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case msg, ok := <-taskChan:
			if !ok {
				// Channel closed — no more human input; exit cleanly.
				return nil
			}

			// Append the human message to the transcript.
			entry := transcript.Entry{
				Speaker:   transcript.SpeakerYou,
				Type:      transcript.TypeMessage,
				Content:   msg,
				Timestamp: time.Now(),
			}
			if err := l.append(entry); err != nil {
				return fmt.Errorf("loop: failed to append human message: %w", err)
			}

			// Passive FYI note if forced mode looks mismatched to the task (Phase 2)
			if note := CheckModeMismatch(l.CurrentMode, msg); note != "" {
				_ = l.append(transcript.Entry{
					Speaker:   transcript.SpeakerSystem,
					Type:      transcript.TypeMessage,
					Content:   note,
					Timestamp: time.Now(),
				})
			}

			// --- Orchestrator confirm reply (Phase 4, §4.4) ---
			//
			// When Orchestrator has asked the human to confirm or override a
			// middle-tier routing decision, pendingOrchestratorConfirm is set.
			// The next message is the confirm/override reply — skip clarify
			// entirely (we don't want to re-clarify the confirmation itself)
			// and resolve the routing decision directly.
			if l.pendingOrchestratorConfirm != nil {
				// resolveOrchestratorConfirm clears the pending confirmation,
				// so capture its original task before resolving it.
				l.activeCycleTask = l.pendingOrchestratorConfirm.task
				em, resolveErr := l.resolveOrchestratorConfirm(msg)
				if resolveErr != nil {
					sysEntry := transcript.Entry{
						Speaker:   transcript.SpeakerSystem,
						Type:      transcript.TypeMessage,
						Content:   fmt.Sprintf("Error: %v", resolveErr),
						Timestamp: time.Now(),
					}
					_ = l.append(sysEntry)
					state = StateIdle
					return resolveErr
				}
				l.effectiveMode = em
				// Fall through to active cycle — skip clarify and routing.

			} else if l.CurrentMode == ModeOrchestrator {
				// --- Orchestrator routing gate (Phase 4, §4.1 + §4.3 + §4.4) ---
				//
				// For orchestrator mode, route the task immediately — before the
				// clarify step. The dispatched mode (General Chat or Triad) runs
				// its own clarification within its execution context if needed.
				// This ensures the clarify heuristics (bare-action, sensitive-
				// surface) do not intercept and block auto-proceed routing
				// decisions for clearly trivial or clearly critical tasks.
				//
				// Routing tiers (§4.3 and §4.4):
				//   trivial  → auto-route to General Chat, no confirmation
				//   critical → auto-route to Triad, no confirmation
				//   middle   → emit confirmation prompt, stay idle
				l.activeCycleTask = msg
				em, waiting, routeErr := l.runOrchestratorRouting(msg)
				if routeErr != nil {
					sysEntry := transcript.Entry{
						Speaker:   transcript.SpeakerSystem,
						Type:      transcript.TypeMessage,
						Content:   fmt.Sprintf("Error: %v", routeErr),
						Timestamp: time.Now(),
					}
					_ = l.append(sysEntry)
					state = StateIdle
					return routeErr
				}
				if waiting {
					// Middle-tier: Orchestrator has asked for human confirmation.
					// Stay idle until the human confirms or overrides.
					continue
				}
				l.effectiveMode = em
				// Fall through to active cycle.

			} else {
				// This is a fresh task. 
				l.activeCycleTask = msg
				
				// Forced mode (general or triad) — use it directly.
				l.effectiveMode = l.CurrentMode
			}

			state = StateActive

			// Run the active cycle until the task is done or an unrecoverable error occurs.
			done, err := l.runActiveCycle(ctx)
			if err != nil {
				// Surface unrecoverable errors (e.g. API failure) as a System entry,
				// then return to idle so the human can try again.
				sysEntry := transcript.Entry{
					Speaker:   transcript.SpeakerSystem,
					Type:      transcript.TypeMessage,
					Content:   fmt.Sprintf("Error: %v", err),
					Timestamp: time.Now(),
				}
				_ = l.append(sysEntry) // best-effort
				state = StateIdle
				// Return error up to caller — in Phase 4's main.go this terminates the process.
				return err
			}

			if done {
				state = StateIdle
				_, _ = l.AutoExtractLearnings()
			}
			_ = state // prevent unused warning if future code doesn't read it
		}
	}
}

// drainInput reads all currently-queued messages from InputChan without blocking
// and appends each one to the transcript as a "You" message. It is called at the
// top of every agent turn so that any message typed mid-cycle is immediately
// visible to both Coder and Reviewer on their next API call.
func (l *Loop) drainInput() error {
	if l.InputChan == nil {
		return nil
	}
	for {
		select {
		case msg, ok := <-l.InputChan:
			if !ok {
				// Channel closed; nothing more to drain.
				return nil
			}
			entry := transcript.Entry{
				Speaker:   transcript.SpeakerYou,
				Type:      transcript.TypeMessage,
				Content:   msg,
				Timestamp: time.Now(),
			}
			if err := l.append(entry); err != nil {
				return fmt.Errorf("loop: failed to append mid-cycle human message: %w", err)
			}
		default:
			// No more messages queued right now — return immediately.
			return nil
		}
	}
}

// runActiveCycle runs the Coder→Reviewer cycle until Coder signals task_complete and
// Reviewer confirms, or an unrecoverable error occurs. Returns (true, nil) on clean completion.
func (l *Loop) runActiveCycle(ctx context.Context) (done bool, err error) {
	// A resumed cycle may not have gone through Run(), so recover the task
	// from the transcript until a fresh task populates activeCycleTask.
	activeTask := l.activeCycleTask
	if activeTask == "" {
		activeTask = l.mostRecentHumanTask()
	}
	l.activeCycleTask = activeTask
	if l.loadedSkills != nil && l.SkillsRegistry != nil && l.SkillsRegistry.Count() > 0 {
		l.loadedSkills.BeginTask()
	}

	// Phase 6.3 — initialize the plan-first gate for this cycle. This
	// must happen BEFORE the first Coder turn so the gate's pre-text
	// counter and bypass flag are correct. The defer at the bottom of
	// the function clears them again at the end of the cycle so the
	// next cycle starts from a known baseline.
	l.resetPlanGateForCycle()
	defer l.clearCycleState()

	for {
		// --- Drain any human messages typed since the last agent turn (Phase 5) ---
		if err := l.drainInput(); err != nil {
			return false, err
		}

		// --- Coder turn ---
		// Funnel-wrapped: build a Coder config with Stage 1 (bare
		// section labels) + Stage 2 (Mini bodies for every already-
		// loaded section) injected into the system prompt, then
		// call the agent. After the response comes back, run the
		// post-call half of the funnel: parse the SELECTED_SECTIONS
		// line out of any plain-text response, apply the
		// selection (mark loaded, log system entry), and return
		// the cleaned response so the caller's transcript entry
		// doesn't include the control line.
		coderResp, err := l.coderTurnWithFunnel(ctx)
		if err != nil {
			return false, err
		}

		// --- General Chat Mode (single agent, no Reviewer, no approval loop) ---
		// Use effectiveMode (set by orchestrator routing or directly from CurrentMode)
		// rather than CurrentMode so Orchestrator can redirect a single task without
		// changing the session-level mode.
		if l.effectiveMode == ModeGeneral {
			if len(coderResp.ToolCalls) == 0 {
				entry := transcript.Entry{
					Speaker:   transcript.SpeakerCoder,
					Type:      transcript.TypeMessage,
					Content:   coderResp.Text,
					Timestamp: time.Now(),
				}
				if err := l.append(entry); err != nil {
					return false, fmt.Errorf("loop: failed to append coder message: %w", err)
				}
				return true, nil
			}

			toolCall := coderResp.ToolCalls[0]
			if toolCall.Function.Name == "task_complete" {
				doneEntry := transcript.Entry{
					Speaker:   transcript.SpeakerSystem,
					Type:      transcript.TypeMessage,
					Content:   "Task complete. Session is now idle. Enter your next task.",
					Timestamp: time.Now(),
				}
				_ = l.append(doneEntry)
				return true, nil
			}

			proposedContent := FormatProposedAction(toolCall)
			proposedEntry := transcript.Entry{
				Speaker:   transcript.SpeakerCoder,
				Type:      transcript.TypeProposedAction,
				Content:   proposedContent,
				Timestamp: time.Now(),
			}
			if err := l.append(proposedEntry); err != nil {
				return false, fmt.Errorf("loop: failed to append proposed_action: %w", err)
			}

			var result string
			var execErr error
			switch {
			case toolCall.Function.Name == "ask_question":
				result = "ERROR: ask_question is not supported in headless mode (no interactive prompt available)."
				execErr = fmt.Errorf("ask_question called in headless mode")
			case toolCall.Function.Name == "spawn_subagent":
				result, execErr = l.runSpawnSubagent(ctx, toolCall)
			case toolCall.Function.Name == "spawn_twin_subagent":
				result, execErr = l.runSpawnTwinSubagent(ctx, toolCall)
			case browser.IsBrowserTool(toolCall.Function.Name):
				result, execErr = l.runBrowserToolWithRetry(toolCall)
			case toolCall.Function.Name == "web_search":
				result, execErr = l.runWebSearchWithRetry(toolCall)
			default:
				retryOpts := l.buildRetryOpts()
				result, execErr = agent.ExecuteTool(l.workDir, toolCall, agent.DefaultCommandTimeout, retryOpts)
			}

			// Phase 3: In General Chat mode, recovery is simpler —
			// deterministic recovery runs silently; if it fails, the
			// error is surfaced directly to Coder (no Reviewer gate).
			var recoveryErr *browser.SelectorRecoveryError
			if execErr != nil && errors.As(execErr, &recoveryErr) {
				if recoveryErr.Phase == "deterministic" && recoveryErr.Candidate != "" {
					// Try the candidate directly.
					recoverResult, recoverExecErr := l.Browser.ExecuteRecoveredAction(
						recoveryErr.Failure.ToolName,
						recoveryErr.Candidate,
						recoveryErr.Strategy,
						toolCall.Function.Arguments,
					)
					if recoverExecErr == nil {
						result = recoverResult
						execErr = nil
						_ = l.append(transcript.Entry{
							Speaker:   transcript.SpeakerSystem,
							Type:      transcript.TypeMessage,
							Content:   fmt.Sprintf("[Recovery]: Selector recovered deterministically using %q [%s].", recoveryErr.Candidate, recoveryErr.Strategy),
							Timestamp: time.Now(),
						})
					}
				}
				// If recovery failed, fall through to normal error handling.
			}

			resultContent := result
			if execErr != nil {
				resultContent = fmt.Sprintf("ERROR: %v", execErr)
			}

			resultEntry := transcript.Entry{
				Speaker:   transcript.SpeakerSystem,
				Type:      transcript.TypeActionResult,
				Content:   resultContent,
				Timestamp: time.Now(),
			}
			if err := l.append(resultEntry); err != nil {
				return false, fmt.Errorf("loop: failed to append action_result: %w", err)
			}

			if execErr == nil {
				if note := autoCommit(l.workDir, l.transcript.FilePath(), toolCall, resultEntry.ID); note != "" {
					_ = l.append(transcript.Entry{
						Speaker:   transcript.SpeakerSystem,
						Type:      transcript.TypeMessage,
						Content:   note,
						Timestamp: time.Now(),
					})
				}
			}

			return true, nil
		}

		// If Coder returned plain text (a plan/message), append and give Coder another turn
		// so it can follow up with an actual tool call.
		if len(coderResp.ToolCalls) == 0 {
			entry := transcript.Entry{
				Speaker:   transcript.SpeakerCoder,
				Type:      transcript.TypeMessage,
				Content:   coderResp.Text,
				Timestamp: time.Now(),
			}
			if err := l.append(entry); err != nil {
				return false, fmt.Errorf("loop: failed to append coder message: %w", err)
			}

			// Phase 6.3 — plan gate pre-text stall guard. If the
			// gate is active and no plan is pending yet, count this
			// plain-text turn. Once the count exceeds
			// MaxPlanPreTextMessages, emit a System note telling
			// Coder to submit a plan. The note is the same shape
			// the gate uses for rejections — a System entry that
			// appears in the transcript AND in Coder's context on
			// the next turn (so it actually sees the nudge).
			if !l.planBypassed && l.pendingPlan == nil {
				if l.recordPrePlanTextMessage() {
					_ = l.append(transcript.Entry{
						Speaker:   transcript.SpeakerSystem,
						Type:      transcript.TypeMessage,
						Content:   formatPlanStallMessage(),
						Timestamp: time.Now(),
					})
				}
			}

			// Continue the loop — Coder should call a tool on the next turn.
			continue
		}

		// Coder has proposed a tool call (or task_complete).
		// For now handle the first tool call only — one atomic action at a time per spec.
		toolCall := coderResp.ToolCalls[0]

		// --- Phase 6.3: submit_plan branch (gate's release valve) ---
		//
		// When the gate is active and Coder proposes submit_plan, we
		// decode the plan, set pendingPlan, and write a snapshot —
		// then fall through to the normal review cycle. The plan is
		// NOT executed (it's not an executable action); it's
		// persisted to the transcript so the next tool call (write_file
		// etc.) can pass the gate's pendingPlan != nil check.
		//
		// We process submit_plan BEFORE the rejection check below so
		// the gate's own release valve can never itself be rejected
		// by the gate.
		if toolCall.Function.Name == "submit_plan" {
			// Even when the gate is bypassed, accept submit_plan —
			// it does no harm and lets Coder submit a plan
			// voluntarily (a future "always show the plan card"
			// feature in the TUI can rely on this).
			nextRevision := 1
			if l.pendingPlan != nil {
				nextRevision = l.pendingPlan.Revision + 1
			}
			plan, decodeErr := extractPlanFromToolCall(toolCall, nextRevision)
			if decodeErr != nil {
				_ = l.append(transcript.Entry{
					Speaker:   transcript.SpeakerSystem,
					Type:      transcript.TypeMessage,
					Content:   fmt.Sprintf("[Plan Gate]: submit_plan arguments could not be decoded: %v", decodeErr),
					Timestamp: time.Now(),
				})
				// Re-emit the proposed_action so Reviewer sees what
				// Coder tried to submit, then continue to give Coder
				// a chance to retry the plan.
				if err := l.append(transcript.Entry{
					Speaker:   transcript.SpeakerCoder,
					Type:      transcript.TypeProposedAction,
					Content:   FormatProposedAction(toolCall),
					Timestamp: time.Now(),
				}); err != nil {
					return false, fmt.Errorf("loop: failed to append proposed_action: %w", err)
				}
				continue
			}
			l.pendingPlan = plan
			// Append the proposed_action so the transcript still has
			// a record of what Coder tried to submit (the snapshot
			// below captures the structured plan, but the
			// proposed_action entry is what Reviewer would have seen
			// in a normal propose→review flow — keeping both entries
			// makes the transcript uniform with non-plan cycles).
			if err := l.append(transcript.Entry{
				Speaker:   transcript.SpeakerCoder,
				Type:      transcript.TypeProposedAction,
				Content:   FormatProposedAction(toolCall),
				Timestamp: time.Now(),
			}); err != nil {
				return false, fmt.Errorf("loop: failed to append proposed_action: %w", err)
			}
			// Snapshot the plan so the transcript captures the
			// approved state. Reason: "initial approval" for
			// revision 1, "revision #N" for subsequent revisions.
			reason := fmt.Sprintf("plan approved (rev #%d)", plan.Revision)
			if err := l.writePlanSnapshot(plan, reason); err != nil {
				return false, fmt.Errorf("loop: failed to write plan snapshot: %w", err)
			}
			// Reviewer is bypassed for submit_plan — the plan is
			// the gate's "this is what I'm going to do" declaration,
			// and Reviewer would just rubber-stamp it. The
			// subsequent actions each go through Reviewer as
			// normal, which is where the actual plan-vs-execution
			// consistency check lives.
			continue
		}

		// --- Phase 6.3: plan-required rejection ---
		//
		// If the gate is active and Coder has not yet submitted a
		// plan, reject this non-submit_plan tool call. Emit a
		// System note explaining the rejection, append the
		// proposed_action anyway (so the transcript still records
		// what Coder tried), and continue — Coder gets another
		// turn to submit a plan first.
		//
		// We do NOT count this against MaxRetries because the
		// review cycle hasn't even started — there's no objection
		// to retry. The gate is a precondition, not a counter.
		if l.planGateRejectsNonPlanCall(toolCall) {
			if err := l.append(transcript.Entry{
				Speaker:   transcript.SpeakerCoder,
				Type:      transcript.TypeProposedAction,
				Content:   FormatProposedAction(toolCall),
				Timestamp: time.Now(),
			}); err != nil {
				return false, fmt.Errorf("loop: failed to append proposed_action: %w", err)
			}
			_ = l.append(transcript.Entry{
				Speaker:   transcript.SpeakerSystem,
				Type:      transcript.TypeMessage,
				Content:   formatPlanRejectionMessage(toolCall),
				Timestamp: time.Now(),
			})
			continue
		}

		// --- Append proposed_action entry ---
		proposedContent := FormatProposedAction(toolCall)
		proposedEntry := transcript.Entry{
			Speaker:   transcript.SpeakerCoder,
			Type:      transcript.TypeProposedAction,
			Content:   proposedContent,
			Timestamp: time.Now(),
		}
		if err := l.append(proposedEntry); err != nil {
			return false, fmt.Errorf("loop: failed to append proposed_action: %w", err)
		}

		// --- Phase 6.3: bind to plan item BEFORE review ---
		// If the gate is active AND a plan is pending AND the
		// tool call didn't already carry a plan_item_id, bind
		// the action to the first pending item and mark it
		// in_progress. This snapshot lands in the transcript
		// between the proposed_action and the reviewer's
		// approval, so reviewers can see "Coder intends to
		// execute item N" before they greenlight the action.
		//
		// We bind BEFORE review (not just on approval) so the
		// gate's audit trail shows the binding even if the
		// reviewer objects and the action is never executed —
		// a future "show me which plan item this objection
		// was about" feature relies on this.
		if !l.planBypassed && l.pendingPlan != nil {
			if boundID, bindErr := l.bindActionToPlanItem(toolCall); bindErr != nil {
				_ = l.append(transcript.Entry{
					Speaker:   transcript.SpeakerSystem,
					Type:      transcript.TypeMessage,
					Content:   fmt.Sprintf("[Plan Gate]: failed to bind action to plan item: %v", bindErr),
					Timestamp: time.Now(),
				})
			} else if boundID > 0 {
				_ = boundID // bound — mark in_progress already snapshotted
			}
		}

		// --- Reviewer→execute-or-revise inner loop ---
		approved, taskDone, err := l.runReviewCycle(ctx, toolCall)
		if err != nil {
			return false, err
		}
		if taskDone {
			return true, nil
		}
		if !approved {
			// The review cycle exhausted retries and surfaced the deadlock.
			// Return to idle so the human can intervene.
			return false, fmt.Errorf("loop: approval deadlock on action %q after %d retries", toolCall.Function.Name, l.MaxRetries)
		}

		// --- Phase 6.3: mark plan item done on success ---
		// The action was approved and executed. If a plan item
		// was bound to this action (above), and the action's
		// action_result is NOT an ERROR, flip the item to
		// "done" and write a snapshot. Failed actions leave
		// the item in "in_progress" so the next iteration can
		// retry it (or the human can intervene).
		if !l.planBypassed && l.pendingPlan != nil {
			boundID, _ := extractPlanItemID(toolCall, l.pendingPlan)
			if boundID == 0 {
				// No explicit binding — use the heuristic the
				// pre-bind step would have used. The first
				// item not yet done is the natural candidate.
				for _, item := range l.pendingPlan.Items {
					if item.Status == transcript.PlanItemInProgress {
						boundID = item.ID
						break
					}
				}
			}
			if boundID > 0 && lastActionResultSucceeded(l.transcript.Entries()) {
				if err := l.markPlanItemDone(boundID); err != nil {
					_ = l.append(transcript.Entry{
						Speaker:   transcript.SpeakerSystem,
						Type:      transcript.TypeMessage,
						Content:   fmt.Sprintf("[Plan Gate]: failed to mark plan item %d done: %v", boundID, err),
						Timestamp: time.Now(),
					})
				}
			}
		}

		// Approved and executed; continue the outer loop — Coder will propose the next action.
	}
}

// runReviewCycle manages the Reviewer→approve/object loop for a single proposed tool call.
// Returns (approved bool, taskDone bool, err error).
// taskDone is true only when task_complete is called and Reviewer approves.
func (l *Loop) runReviewCycle(ctx context.Context, toolCall agent.ToolCall) (approved bool, taskDone bool, err error) {
	isTaskComplete := toolCall.Function.Name == "task_complete"

	for attempt := 1; attempt <= l.MaxRetries; attempt++ {
		// --- Drain any human messages typed since the last agent turn (Phase 5) ---
		if err := l.drainInput(); err != nil {
			return false, false, err
		}

		// --- Reviewer turn ---
		reviewerResp, err := l.client.Respond(ctx, l.reviewer, l.transcript.Entries())
		if err != nil {
			return false, false, fmt.Errorf("reviewer API call failed: %w", err)
		}

		reviewerText := strings.TrimSpace(reviewerResp.Text)

		// Append Reviewer's response.
		reviewerEntry := transcript.Entry{
			Speaker:   transcript.SpeakerReviewer,
			Type:      transcript.TypeMessage,
			Content:   reviewerText,
			Timestamp: time.Now(),
		}
		if err := l.append(reviewerEntry); err != nil {
			return false, false, fmt.Errorf("loop: failed to append reviewer message: %w", err)
		}

		decision := ParseReviewerDecision(reviewerText)

		switch decision {
		case DecisionApprove:
			if isTaskComplete {
				// Task is done — Reviewer confirmed.
				doneEntry := transcript.Entry{
					Speaker:   transcript.SpeakerSystem,
					Type:      transcript.TypeMessage,
					Content:   "Task complete. Session is now idle. Enter your next task.",
					Timestamp: time.Now(),
				}
				_ = l.append(doneEntry)
				return true, true, nil
			}

			// Execute the approved tool call.
			//
			// spawn_subagent and browser_* are special-cased:
			// ExecuteTool doesn't know how to run a subagent (it
			// doesn't have the subagent's client / session dir /
			// parent config) or a browser tool (it doesn't have the
			// browser.Manager). The loop intercepts them here and
			// runs them itself, then synthesises an action_result
			// entry the same way ExecuteTool's normal path would.
			// All other tools go through agent.ExecuteTool
			// unchanged.
			var result string
			var execErr error
			switch {
			case toolCall.Function.Name == "ask_question":
				result = "ERROR: ask_question is not supported in headless mode (no interactive prompt available)."
				execErr = fmt.Errorf("ask_question called in headless mode")
			case toolCall.Function.Name == "spawn_subagent":
				result, execErr = l.runSpawnSubagent(ctx, toolCall)
			case toolCall.Function.Name == "spawn_twin_subagent":
				result, execErr = l.runSpawnTwinSubagent(ctx, toolCall)
			case browser.IsBrowserTool(toolCall.Function.Name):
				result, execErr = l.runBrowserToolWithRetry(toolCall)
			case toolCall.Function.Name == "web_search":
				result, execErr = l.runWebSearchWithRetry(toolCall)
			default:
				retryOpts := l.buildRetryOpts()
				result, execErr = agent.ExecuteTool(l.workDir, toolCall, agent.DefaultCommandTimeout, retryOpts)
			}

			// --- Phase 3: Selector failure recovery ---
			// If the browser tool returned a SelectorRecoveryError,
			// the loop attempts recovery instead of surfacing the
			// raw error to Coder.
			var recoveryErr *browser.SelectorRecoveryError
			if execErr != nil && errors.As(execErr, &recoveryErr) {
				recovered, recoverErr := l.handleSelectorRecovery(ctx, toolCall, recoveryErr)
				if recoverErr != nil {
					return false, false, recoverErr
				}
				if recovered {
					// Recovery succeeded — the corrected action was
					// executed and its result appended. Continue the
					// outer loop so Coder can propose the next action.
					return true, false, nil
				}
				// Recovery not possible — fall through to normal
				// error handling below.
			}

			resultContent := result
			if execErr != nil {
				resultContent = fmt.Sprintf("ERROR: %v", execErr)
			}

			resultEntry := transcript.Entry{
				Speaker:   transcript.SpeakerSystem,
				Type:      transcript.TypeActionResult,
				Content:   resultContent,
				Timestamp: time.Now(),
			}
			if err := l.append(resultEntry); err != nil {
				return false, false, fmt.Errorf("loop: failed to append action_result: %w", err)
			}

			// Auto-commit on every executed edit (docs/work2.md §2.2).
			// Only acts on successful write_file / run_command; reads
			// and task_complete never touch the filesystem. Rejected
			// proposals never reach this branch, so they never touch
			// git either. The headless loop mirrors the TUI's
			// auto-commit behaviour so the two paths stay in sync.
			if execErr == nil {
				if note := autoCommit(l.workDir, l.transcript.FilePath(), toolCall, resultEntry.ID); note != "" {
					_ = l.append(transcript.Entry{
						Speaker:   transcript.SpeakerSystem,
						Type:      transcript.TypeMessage,
						Content:   note,
						Timestamp: time.Now(),
					})
				}
			}

			return true, false, nil

		case DecisionObject:
			// Reviewer objected. If retries remain, let Coder see the objection and re-propose.
			if attempt < l.MaxRetries {
				// Give Coder a turn to revise. The objection is already
				// in the transcript. Go through the same funnel wrapper
				// as the initial Coder turn so a re-selection on this
				// revision is parsed and applied identically — the spec
				// wants Stage 1 + Stage 2 on every coding turn, not just
				// the first one of the cycle.
				coderResp, err := l.coderTurnWithFunnel(ctx)
				if err != nil {
					return false, false, fmt.Errorf("coder revision API call failed: %w", err)
				}

				if len(coderResp.ToolCalls) == 0 {
					// Coder sent a message instead of a revised proposal; append it.
					msgEntry := transcript.Entry{
						Speaker:   transcript.SpeakerCoder,
						Type:      transcript.TypeMessage,
						Content:   coderResp.Text,
						Timestamp: time.Now(),
					}
					if err := l.append(msgEntry); err != nil {
						return false, false, fmt.Errorf("loop: failed to append coder revision message: %w", err)
					}
					// Update toolCall for next Reviewer turn even if Coder only sent text.
					// The next Reviewer turn will see the message and likely object again,
					// but that's correct — Coder must send a proper proposal.
					continue
				}

				// Coder proposed a revised tool call.
				toolCall = coderResp.ToolCalls[0]
				isTaskComplete = toolCall.Function.Name == "task_complete"

				revisedContent := FormatProposedAction(toolCall)
				revisedEntry := transcript.Entry{
					Speaker:   transcript.SpeakerCoder,
					Type:      transcript.TypeProposedAction,
					Content:   revisedContent,
					Timestamp: time.Now(),
				}
				if err := l.append(revisedEntry); err != nil {
					return false, false, fmt.Errorf("loop: failed to append revised proposed_action: %w", err)
				}

			} else {
				// Retry cap hit — surface to human.
				capEntry := transcript.Entry{
					Speaker:   transcript.SpeakerSystem,
					Type:      transcript.TypeMessage,
					Content:   fmt.Sprintf("Approval deadlock: Coder and Reviewer could not agree on action %q after %d attempts. Human intervention required.", toolCall.Function.Name, l.MaxRetries),
					Timestamp: time.Now(),
				}
				_ = l.append(capEntry)
				return false, false, nil
			}
		}
	}

	return false, false, nil
}

// append adds an entry to the transcript and fires the OnEntry callback if set.
func (l *Loop) append(entry transcript.Entry) error {
	if err := l.transcript.Append(entry); err != nil {
		return err
	}
	if l.OnEntry != nil {
		l.OnEntry(entry)
	}
	if l.Memory != nil {
		_ = l.Memory.AppendDailyLog(entry.Timestamp, fmt.Sprintf("[%s] %s", entry.Speaker, entry.Content))
	}
	return nil
}

// ParseReviewerDecision reads the first word of Reviewer's response to determine approval.
// The Reviewer system prompt instructs it to start with "APPROVED" or "OBJECTION:".
func ParseReviewerDecision(text string) Decision {
	upper := strings.ToUpper(strings.TrimSpace(text))
	if strings.HasPrefix(upper, "APPROVED") {
		return DecisionApprove
	}
	return DecisionObject
}

// FormatProposedAction renders a ToolCall into a human-readable string for the transcript.
func FormatProposedAction(tc agent.ToolCall) string {
	if tc.Function.Arguments == "" || tc.Function.Arguments == "{}" {
		return fmt.Sprintf("Tool: %s\n(no arguments)", tc.Function.Name)
	}
	return fmt.Sprintf("Tool: %s\nArguments: %s", tc.Function.Name, tc.Function.Arguments)
}

// ParseProposedAction attempts to parse a ToolCall struct from a formatted proposed_action transcript string.
func ParseProposedAction(content string) (*agent.ToolCall, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil, fmt.Errorf("empty proposed_action content")
	}
	lines := strings.Split(trimmed, "\n")

	var name string
	firstLine := strings.TrimSpace(lines[0])
	if strings.HasPrefix(firstLine, "Tool: ") {
		name = strings.TrimPrefix(firstLine, "Tool: ")
	} else if strings.HasPrefix(firstLine, "Proposed tool call: ") {
		name = strings.TrimPrefix(firstLine, "Proposed tool call: ")
	} else {
		name = firstLine
	}
	name = strings.TrimSpace(name)

	if name == "" {
		return nil, fmt.Errorf("could not extract tool name from proposed_action content: %q", content)
	}

	args := "{}"
	if idx := strings.Index(content, "Arguments:"); idx != -1 {
		rawArgs := strings.TrimSpace(content[idx+len("Arguments:"):])
		if rawArgs != "" && rawArgs != "(no arguments)" {
			args = rawArgs
		}
	}

	return &agent.ToolCall{
		ID:   "resumed_call",
		Type: "function",
		Function: agent.ToolCallFunction{
			Name:      name,
			Arguments: args,
		},
	}, nil
}

// runSpawnSubagent is the headless loop's handler for approved
// spawn_subagent tool calls (docs/work2.md §3). It decodes the
// tool-call arguments, runs the subagent to completion (or to its
// turn cap), and returns a one-line summary suitable for an
// action_result entry. The subagent's own transcript lives at
// <sessionDir>/subagents/<id>.jsonl and is never seen by the parent
// loop or Reviewer — only the final summary bubbles up.
//
// The runner inherits the loop's command timeout and the parent
// Coder's config (BaseURL/Model/APIKey); the subagent's own
// system prompt, tool set, and depth guard all live in the
// subagent package.
//
// Returns the summary string on success, or an error (wrapped) on
// subagent setup / run failure. The caller's existing
// "ERROR: %v" formatting surfaces the error inline in the transcript.
func (l *Loop) runSpawnSubagent(ctx context.Context, toolCall agent.ToolCall) (string, error) {
	var args agent.SpawnSubagentArgs
	if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &args); err != nil {
		return "", fmt.Errorf("spawn_subagent: failed to parse arguments: %w", err)
	}
	if strings.TrimSpace(args.Task) == "" {
		return "", fmt.Errorf("spawn_subagent: required argument 'task' is missing or empty")
	}

	// Subagent JSONL lands next to the parent's session file. If the
	// parent has no session file path yet, fall back to a
	// sessions/ dir under the working directory — the same fallback
	// main.go uses for finding the latest session.
	sessionDir := filepath.Dir(l.transcript.FilePath())
	if sessionDir == "" || sessionDir == "." {
		sessionDir = filepath.Join(l.workDir, "sessions")
	}

	runner, err := subagent.NewRunner(
		l.client,
		l.workDir,
		sessionDir,
		agent.DefaultCommandTimeout,
		0, // use default turn cap
		0, // depth 0 — top-level (subagents can't themselves spawn)
	)
	if err != nil {
		return "", fmt.Errorf("spawn_subagent: %w", err)
	}
	// Propagate the parent session's skills registry so the
	// subagent's Coder turns go through the same Stage-1 /
	// Stage-2 funnel. The subagent gets a per-run loaded set
	// (independent of the parent's), so a subagent's first
	// selection of any section fires Main regardless of
	// whether the parent already loaded it.
	runner.SetSkillsRegistry(l.SkillsRegistry)

	id := subagent.NewID()
	res, runErr := runner.Run(ctx, id, args.Task, args.Context, l.coder)
	if runErr != nil {
		// The subagent failed partway through — return whatever
		// summary it managed to produce plus the error, so the
		// parent can still see partial findings in the transcript.
		if res.Summary != "" {
			return fmt.Sprintf("[subagent %s partial] %s\n\nerror: %v", id, res.Summary, runErr), runErr
		}
		return "", runErr
	}

	// Prepend a small "Subagent <id>:" header so Reviewer can see at a
	// glance that this action_result came from a delegation, and add
	// a "(truncated, partial findings)" tag if the subagent hit its
	// turn cap. Reviewer should treat truncated findings as low-
	// confidence; the parent Coder can re-spawn with a more focused
	// task if needed.
	header := fmt.Sprintf("Subagent %s: ", id)
	if res.Truncated {
		header = fmt.Sprintf("Subagent %s (truncated, %d turns): ", id, res.Turns)
	}
	return header + res.Summary, nil
}

// runSpawnTwinSubagent is the headless loop's handler for approved
// spawn_twin_subagent tool calls (work.md §6.9). It decodes the tool-call
// arguments, logs a start-of-spawn entry to the MAIN transcript (§6.15 —
// implemented here as the minimum viable visibility fix), runs the twin pair
// to completion (or its turn cap), and returns a one-line summary attributed
// to the twin pair.
//
// The twin pair's own full transcript lives at <sessionDir>/twins/<id>.jsonl
// and is never seen by the parent loop or Reviewer — only the final summary
// bubbles up as a single action_result entry. The twin pair's internal
// propose→review→execute loop, mini-Reviewer no-tool invariant, depth guard,
// turn cap, and clarify phase are all enforced by the twinsubagent package.
//
// Attribution: the returned summary is prefixed with the twin pair's ID so
// the action_result entry in the main transcript is clearly twin-attributed
// (e.g. "[Twin:add-rate-limiting]: <summary>") without needing a separate
// speaker field — matches the §6.9 contract of a single action_result entry.
func (l *Loop) runSpawnTwinSubagent(ctx context.Context, toolCall agent.ToolCall) (string, error) {
	var args agent.SpawnSubagentArgs // reuse same {task, context} structure
	if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &args); err != nil {
		return "", fmt.Errorf("spawn_twin_subagent: failed to parse arguments: %w", err)
	}
	if strings.TrimSpace(args.Task) == "" {
		return "", fmt.Errorf("spawn_twin_subagent: required argument 'task' is missing or empty")
	}

	// Session dir: twin transcripts land at <sessionDir>/twins/<id>.jsonl,
	// physically separate from single-subagent transcripts (<sessionDir>/subagents/).
	sessionDir := filepath.Dir(l.transcript.FilePath())
	if sessionDir == "" || sessionDir == "." {
		sessionDir = filepath.Join(l.workDir, "sessions")
	}

	runner, err := twinsubagent.NewRunner(
		l.client,
		l.workDir,
		sessionDir,
		agent.DefaultCommandTimeout,
		0, // use DefaultMaxTurns
	)
	if err != nil {
		return "", fmt.Errorf("spawn_twin_subagent: %w", err)
	}

	id := subagent.NewID() // reuse the same ID generator for consistent id format

	// §6.15 (minimum viable fix) — log start-of-spawn to the MAIN transcript
	// immediately, before the twin pair runs. If the twin pair hangs or burns
	// rate limit silently, the main transcript at least records that a twin was
	// spawned and what task it was given. Full cross-agent observability is Phase 7.
	_ = l.append(transcript.Entry{
		Speaker:   transcript.SpeakerSystem,
		Type:      transcript.TypeMessage,
		Content:   fmt.Sprintf("[System]: Twin subagent started for task: %s (id: %s, transcript: %s)", args.Task, id, runner.TranscriptPath(id)),
		Timestamp: time.Now(),
	})

	res, runErr := runner.Run(ctx, id, args.Task, args.Context, l.coder)
	if runErr != nil {
		if res.Summary != "" {
			return fmt.Sprintf("[twin %s partial] %s\n\nerror: %v", id, res.Summary, runErr), runErr
		}
		return "", runErr
	}

	// §6.9 — return a single summary string attributed to the twin pair.
	// The caller appends this as an action_result entry. The attribution
	// ("[Twin:<id>]: ...") is embedded in the content so the main
	// transcript reader can identify twin work at a glance.
	header := fmt.Sprintf("[%s]: ", twinsubagent.SummaryAttributionLabel(id))
	if res.Truncated {
		header = fmt.Sprintf("[%s] (truncated, %d turns, treat as low-confidence): ", twinsubagent.SummaryAttributionLabel(id), res.Turns)
	}
	return header + res.Summary, nil
}

// runBrowserTool is the headless loop's handler for approved
// browser_* tool calls (docs/work2.md §4.2). It is structurally
// similar to runSpawnSubagent — the tool has long-lived state
// (the Chromium process / shared page) that ExecuteTool doesn't
// have, so the loop owns that state and dispatches the call here.
//
// Phase 3: selector failure recovery is integrated here. When a
// selector fails (zero match or ambiguous match), this method
// attempts deterministic recovery first (Phase 3.2). If that fails
// and recovery attempts haven't been exhausted, it returns a
// sentinel error that signals the caller to invoke LLM-assisted
// recovery (Phase 3.3).
//
// If no browser manager is configured on the loop, the call
// surfaces a clear "browser not configured" error rather than
// crashing — same defensive pattern as the other special-cased
// tools.
//
// The manager is shared across all tool calls within a session
// (just like a real human's single browser tab), so multiple
// navigate/click/type/get_text calls in sequence all see the
// same page state. The manager's own mutex serialises the calls.
func (l *Loop) runBrowserTool(toolCall agent.ToolCall) (string, error) {
	if !browser.IsBrowserTool(toolCall.Function.Name) {
		return "", fmt.Errorf("runBrowserTool: %q is not a browser tool (this is a bug — caller should have gated on IsBrowserTool)", toolCall.Function.Name)
	}
	if l.Browser == nil {
		return "", fmt.Errorf("browser tool %q approved but no browser.Manager is configured on the loop; restart Triad with a browser-capable configuration", toolCall.Function.Name)
	}

	result, err := l.Browser.ExecuteTool(l.workDir, toolCall.Function.Name, toolCall.Function.Arguments)
	if err == nil {
		// Success — clear any recovery tracking for this action.
		delete(l.recoveryAttempts, toolCall.ID)
		return result, nil
	}

	// --- Phase 3: Selector failure detection and recovery ---

	// Parse the selector and strategy from the tool arguments to
	// feed into the failure detector.
	selector, strategy := parseSelectorFromArgs(toolCall.Function.Arguments)

	failure := browser.DetectSelectorFailure(toolCall.Function.Name, strategy, selector, err)
	if failure == nil {
		// Not a selector failure — surface as a normal tool error.
		return result, err
	}

	attempts := l.recoveryAttempts[toolCall.ID]
	if attempts >= MaxRecoveryAttempts {
		// Recovery cap hit — surface the error to Coder.
		delete(l.recoveryAttempts, toolCall.ID)
		return result, fmt.Errorf("browser selector recovery exhausted after %d attempts: %w", attempts, err)
	}

	// Increment attempt counter.
	l.recoveryAttempts[toolCall.ID] = attempts + 1

	// --- Phase 3.2: Deterministic recovery ---
	recovery := l.Browser.AttemptDeterministicRecovery(failure)
	if recovery != nil && recovery.Recovered {
		// Deterministic recovery succeeded — return the recovered
		// result as if the original action had succeeded.
		_ = l.append(transcript.Entry{
			Speaker:   transcript.SpeakerSystem,
			Type:      transcript.TypeMessage,
			Content:   fmt.Sprintf("[Recovery]: Selector %q [%s] failed (%s). Deterministic recovery found and used %q [%s].", selector, strategy, failure.Type, recovery.Result, strategy),
			Timestamp: time.Now(),
		})
		delete(l.recoveryAttempts, toolCall.ID)
		return recovery.Result, nil
	}
	if recovery != nil && recovery.Candidate != "" {
		// Deterministic recovery found a candidate but couldn't execute
		// it. Return a signal error so the caller can propose it to
		// Reviewer.
		return result, &browser.SelectorRecoveryError{
			Failure:     *failure,
			Candidate:   recovery.Candidate,
			Strategy:    recovery.CandidateStrategy,
			Phase:       "deterministic",
			OriginalErr: err,
		}
	}

	// --- Phase 3.3: LLM-assisted recovery needed ---
	// Deterministic recovery failed. Signal the caller to invoke the
	// LLM for a corrected selector.
	return result, &browser.SelectorRecoveryError{
		Failure:     *failure,
		Phase:       "model",
		OriginalErr: err,
	}
}

// runWebSearch handles approved web_search tool calls in the loop.
func (l *Loop) runWebSearch(toolCall agent.ToolCall) (string, error) {
	var args agent.ExecuteToolArgs
	if toolCall.Function.Arguments != "" && toolCall.Function.Arguments != "{}" {
		if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &args); err != nil {
			return "", fmt.Errorf("web_search: failed to parse arguments: %w", err)
		}
	}
	if strings.TrimSpace(args.Query) == "" {
		return "", fmt.Errorf("web_search: required argument 'query' is missing or empty")
	}
	return agent.ExecuteWebSearch(args.Query, l.SearchAPIKey)
}

// buildRetryOpts constructs RetryOptions with a callback that surfaces
// retry progress to the transcript as System entries. This is the shared
// retry configuration used by all tool execution paths in the loop.
func (l *Loop) buildRetryOpts() *agent.RetryOptions {
	return &agent.RetryOptions{
		MaxAttempts: agent.RetryMaxAttempts,
		BaseDelay:   agent.RetryBaseDelay,
		OnRetry: func(attempt, maxAttempts int, err error) {
			_ = l.append(transcript.Entry{
				Speaker:   transcript.SpeakerSystem,
				Type:      transcript.TypeMessage,
				Content:   fmt.Sprintf("[Retry]: attempt %d/%d failed (%v). Retrying...", attempt, maxAttempts, err),
				Timestamp: time.Now(),
			})
		},
	}
}

// runBrowserToolWithRetry wraps runBrowserTool with the shared retry
// mechanism. Browser tools can fail transiently (navigation timeouts,
// page crashes, connection resets), so they are retried on transient
// errors just like standard tools.
func (l *Loop) runBrowserToolWithRetry(toolCall agent.ToolCall) (string, error) {
	return agent.ExecuteWithRetry(*l.buildRetryOpts(), func() (string, error) {
		return l.runBrowserTool(toolCall)
	})
}

// runWebSearchWithRetry wraps runWebSearch with the shared retry
// mechanism. Web search can fail transiently (network errors, API
// timeouts), so it is retried on transient errors.
func (l *Loop) runWebSearchWithRetry(toolCall agent.ToolCall) (string, error) {
	return agent.ExecuteWithRetry(*l.buildRetryOpts(), func() (string, error) {
		return l.runWebSearch(toolCall)
	})
}

// autoCommit is the headless equivalent of the TUI's maybeAutoCommit.
// It returns an empty string when there's nothing to commit, and a
// one-line note suitable for a System transcript entry on success or
// on a surfaced error. Permanent misconfiguration is reported once via
// the returned string; the loop's caller appends it as a System entry.
//
// If workDir is not a git repository (the headless loop is used in
// tests that don't always set one up, and the TUI's main.go does its
// own EnsureRepo before starting the session), this helper is a
// no-op — it returns "" without writing anything to the transcript.
// Production sessions always have a repo by the time the first
// action runs.
//
// This helper is a thin wrapper around gitcommit.CommitAction — it
// just figures out which file(s) the action touched and builds a
// minimal intent excerpt. The TUI does the same work but also has
// access to the Coder's most recent planning message; in the
// headless loop we don't, so we synthesise a short intent from the
// tool arguments.
func autoCommit(workDir, sessionPath string, toolCall agent.ToolCall, resultEntryID int) string {
	switch toolCall.Function.Name {
	case "write_file", "run_command":
		// proceed
	default:
		return ""
	}

	// If we're not in a git repo, silently skip the auto-commit step.
	// The TUI's main.go has already called EnsureRepo, so the live
	// path always passes this check; the headless test path may not.
	if !gitcommit.IsRepo(workDir) {
		return ""
	}

	var paths []string
	intent := ""
	switch toolCall.Function.Name {
	case "write_file":
		var args agent.ExecuteToolArgs
		if toolCall.Function.Arguments != "" {
			_ = json.Unmarshal([]byte(toolCall.Function.Arguments), &args)
		}
		if strings.TrimSpace(args.Path) == "" {
			return ""
		}
		paths = []string{gitcommit.NormalizePath(workDir, args.Path)}
		intent = "write " + args.Path
	case "run_command":
		found, err := gitcommit.ChangedPaths(workDir)
		if err != nil {
			// Most commonly: workDir isn't a git repo (the headless
			// loop is used in tests that don't always set one up).
			// Treat that as "no auto-commit" rather than spamming
			// the transcript with a git error. The TUI ensures the
			// repo exists before the session starts, so this branch
			// is rare in the live path.
			if !gitcommit.IsRepo(workDir) {
				return ""
			}
			return fmt.Sprintf("git status failed: %v", err)
		}
		paths = found
		var args agent.ExecuteToolArgs
		if toolCall.Function.Arguments != "" {
			_ = json.Unmarshal([]byte(toolCall.Function.Arguments), &args)
		}
		intent = "run: " + args.Command
	}

	if len(paths) == 0 {
		return ""
	}

	msg := gitcommit.CommitMessage{
		EntryID:     resultEntryID,
		Intent:      intent,
		ToolName:    toolCall.Function.Name,
		SessionPath: sessionPath,
		ProposedBy:  transcript.SpeakerCoder,
		ApprovedBy:  transcript.SpeakerReviewer,
	}

	res, err := gitcommit.CommitAction(workDir, paths, msg)
	if err != nil {
		if gitcommit.IsNotConfigured(err) {
			return "auto-commit disabled: git user.name / user.email not configured."
		}
		return fmt.Sprintf("auto-commit failed: %v", err)
	}
	if res.NoChanges {
		return ""
	}
	if res.Hash != "" {
		return fmt.Sprintf("auto-commit %s: %s", res.Hash, intent)
	}
	return ""
}

// ---------------------------------------------------------------------------
// Phase 3 — Selector failure recovery helpers
// ---------------------------------------------------------------------------

// parseSelectorFromArgs extracts the selector and strategy fields
// from a browser tool call's raw JSON arguments. Returns empty
// strings when the args are malformed or missing these fields.
func parseSelectorFromArgs(rawArgs string) (selector string, strategy browser.SelectStrategy) {
	var args struct {
		Selector string `json:"selector"`
		Strategy string `json:"strategy"`
	}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return "", ""
	}
	return args.Selector, browser.SelectStrategy(args.Strategy)
}

// buildRecoveryPrompt constructs the prompt sent to the LLM for
// Phase 3.3 model-assisted recovery. It gives the model the page
// context, the failed selector, and a focused instruction to
// suggest ONE corrected selector — not to replan the whole task.
func buildRecoveryPrompt(pageContext, toolName, selector string, strategy browser.SelectStrategy) string {
	return fmt.Sprintf(
		"The following browser action failed because the selector matched no element:\n\n"+
			"Tool: %s\n"+
			"Selector: %s\n"+
			"Strategy: %s\n\n"+
			"Here is the current page's interactive elements and structure:\n%s\n\n"+
			"Please suggest ONE corrected selector (with strategy) that would successfully target the intended element. "+
			"Return ONLY a JSON object with fields \"selector\" and \"strategy\", nothing else.",
		toolName, selector, strategy, pageContext,
	)
}

// parseLLMSelectorResponse extracts a corrected selector and strategy
// from the LLM's response to the recovery prompt. The LLM is asked
// to return a JSON object {"selector": "...", "strategy": "..."}.
func parseLLMSelectorResponse(response string) (selector string, strategy browser.SelectStrategy, err error) {
	// Try to find a JSON object in the response.
	trimmed := strings.TrimSpace(response)

	// If the response is wrapped in markdown code fences, strip them.
	if strings.HasPrefix(trimmed, "```") {
		lines := strings.Split(trimmed, "\n")
		var jsonLines []string
		inBlock := false
		for _, line := range lines {
			if strings.HasPrefix(line, "```") {
				if inBlock {
					break
				}
				inBlock = true
				continue
			}
			if inBlock {
				jsonLines = append(jsonLines, line)
			}
		}
		trimmed = strings.Join(jsonLines, "\n")
	}

	var parsed struct {
		Selector string `json:"selector"`
		Strategy string `json:"strategy"`
	}
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return "", "", fmt.Errorf("failed to parse LLM recovery response as JSON: %w (response: %.200s)", err, response)
	}
	if parsed.Selector == "" {
		return "", "", fmt.Errorf("LLM recovery response has empty selector")
	}
	return parsed.Selector, browser.SelectStrategy(parsed.Strategy), nil
}

// handleSelectorRecovery orchestrates the Phase 3 recovery flow for
// a browser tool that failed with a selector error. It is called from
// runReviewCycle when a SelectorRecoveryError is detected.
//
// The flow:
//  1. If the recovery error has a candidate (from deterministic
//     recovery), propose it to Reviewer as a new action.
//  2. If no candidate, invoke the LLM for a corrected selector
//     (Phase 3.3) — the LLM's response goes through Reviewer.
//  3. Cap recovery attempts at MaxRecoveryAttempts (2).
//
// Returns (true, nil) when recovery succeeded and the corrected
// action was executed. Returns (false, nil) when recovery is not
// possible (falls through to normal error handling). Returns
// (false, err) on internal errors.
func (l *Loop) handleSelectorRecovery(
	ctx context.Context,
	originalToolCall agent.ToolCall,
	recoveryErr *browser.SelectorRecoveryError,
) (bool, error) {
	tracePath := tracelog.TracePathForSession(l.transcript.FilePath())

	// Log the recovery attempt.
	_ = tracelog.Append(tracePath, tracelog.Entry{
		Entity:    "recovery",
		EventType: tracelog.EventRecoveryAttempt,
		Description: fmt.Sprintf("Phase 3 recovery: selector %q [%s] failed (%s), phase=%s",
			recoveryErr.Failure.Selector, recoveryErr.Failure.Strategy,
			recoveryErr.Failure.Type, recoveryErr.Phase),
	})

	// --- Path 1: Deterministic candidate → propose to Reviewer ---
	if recoveryErr.Phase == "deterministic" && recoveryErr.Candidate != "" {
		_ = l.append(transcript.Entry{
			Speaker: transcript.SpeakerSystem,
			Type:    transcript.TypeMessage,
			Content: fmt.Sprintf("[Recovery]: Selector %q [%s] failed (%s). Deterministic recovery suggests %q [%s]. Proposing to Reviewer.",
				recoveryErr.Failure.Selector, recoveryErr.Failure.Strategy,
				recoveryErr.Failure.Type, recoveryErr.Candidate, recoveryErr.Strategy),
			Timestamp: time.Now(),
		})

		// Build a new tool call with the recovered selector.
		correctedCall := buildCorrectedToolCall(originalToolCall, recoveryErr.Candidate, string(recoveryErr.Strategy))
		correctedContent := FormatProposedAction(correctedCall)
		_ = l.append(transcript.Entry{
			Speaker:   transcript.SpeakerCoder,
			Type:      transcript.TypeProposedAction,
			Content:   correctedContent,
			Timestamp: time.Now(),
		})

		// Let Reviewer decide.
		approved, _, reviewErr := l.runReviewCycle(ctx, correctedCall)
		return approved, reviewErr
	}

	// --- Path 2: LLM-assisted recovery ---
	if l.Browser == nil {
		return false, nil
	}

	attempts := l.recoveryAttempts[originalToolCall.ID]
	if attempts >= MaxRecoveryAttempts {
		_ = l.append(transcript.Entry{
			Speaker:   transcript.SpeakerSystem,
			Type:      transcript.TypeMessage,
			Content:   fmt.Sprintf("[Recovery]: Selector recovery exhausted after %d attempts. Surfacing error to Coder.", attempts),
			Timestamp: time.Now(),
		})
		return false, nil
	}
	l.recoveryAttempts[originalToolCall.ID] = attempts + 1

	// Extract page context for the LLM.
	pageContext := l.Browser.PageContextForRecovery()
	if pageContext == "" || pageContext == "(page not available)" {
		return false, nil
	}

	// Build the recovery prompt.
	prompt := buildRecoveryPrompt(
		pageContext,
		recoveryErr.Failure.ToolName,
		recoveryErr.Failure.Selector,
		recoveryErr.Failure.Strategy,
	)

	// Invoke the LLM with a focused recovery prompt.
	_ = l.append(transcript.Entry{
		Speaker:   transcript.SpeakerSystem,
		Type:      transcript.TypeMessage,
		Content:   fmt.Sprintf("[Recovery]: Deterministic recovery failed. Invoking model to correct selector %q [%s] (attempt %d/%d).", recoveryErr.Failure.Selector, recoveryErr.Failure.Strategy, attempts+1, MaxRecoveryAttempts),
		Timestamp: time.Now(),
	})

	recoveryEntries := []transcript.Entry{
		{
			Speaker:   transcript.SpeakerSystem,
			Type:      transcript.TypeMessage,
			Content:   prompt,
			Timestamp: time.Now(),
		},
	}

	llmResp, err := l.client.Respond(ctx, l.coder, recoveryEntries)
	if err != nil {
		return false, fmt.Errorf("LLM recovery call failed: %w", err)
	}

	if len(llmResp.ToolCalls) > 0 {
		// LLM responded with a tool call — use it as the corrected action.
		correctedCall := llmResp.ToolCalls[0]

		// Append the LLM's proposal.
		correctedContent := FormatProposedAction(correctedCall)
		_ = l.append(transcript.Entry{
			Speaker:   transcript.SpeakerCoder,
			Type:      transcript.TypeProposedAction,
			Content:   correctedContent,
			Timestamp: time.Now(),
		})

		// Let Reviewer decide.
		approved, _, reviewErr := l.runReviewCycle(ctx, correctedCall)
		return approved, reviewErr
	}

	// LLM responded with text — try to parse a selector from it.
	selector, strategy, parseErr := parseLLMSelectorResponse(llmResp.Text)
	if parseErr != nil {
		// LLM didn't produce a usable selector. Log and fall through.
		_ = l.append(transcript.Entry{
			Speaker:   transcript.SpeakerSystem,
			Type:      transcript.TypeMessage,
			Content:   fmt.Sprintf("[Recovery]: Model did not produce a valid selector: %v", parseErr),
			Timestamp: time.Now(),
		})
		return false, nil
	}

	// Build a corrected tool call from the LLM's text response.
	correctedCall := buildCorrectedToolCall(originalToolCall, selector, string(strategy))
	correctedContent := FormatProposedAction(correctedCall)
	_ = l.append(transcript.Entry{
		Speaker:   transcript.SpeakerCoder,
		Type:      transcript.TypeProposedAction,
		Content:   correctedContent,
		Timestamp: time.Now(),
	})

	// Let Reviewer decide.
	approved, _, reviewErr := l.runReviewCycle(ctx, correctedCall)
	return approved, reviewErr
}

// buildCorrectedToolCall creates a new tool call with the corrected
// selector and strategy, preserving the original tool name and other
// arguments.
func buildCorrectedToolCall(original agent.ToolCall, selector, strategy string) agent.ToolCall {
	// Parse original args and replace selector/strategy.
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(original.Function.Arguments), &args); err != nil {
		args = make(map[string]interface{})
	}
	args["selector"] = selector
	if strategy != "" {
		args["strategy"] = strategy
	}

	correctedArgs, _ := json.Marshal(args)

	return agent.ToolCall{
		ID:   original.ID + "_recovery",
		Type: "function",
		Function: agent.ToolCallFunction{
			Name:      original.Function.Name,
			Arguments: string(correctedArgs),
		},
	}
}
