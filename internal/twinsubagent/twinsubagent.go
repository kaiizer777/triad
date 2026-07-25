// Package twinsubagent implements the Twin Subagent construct (work.md §Phase 6) —
// an isolated mini-Triad consisting of a mini-Coder and a mini-Reviewer that run
// their own private propose→review→execute loop, completely separate from the
// main session loop.
//
// Design summary (full rationale in work.md §Phase 6):
//
//   - The twin pair gets its own isolated JSONL transcript at
//     <sessionDir>/twins/<id>.jsonl, physically distinct from both the
//     main session transcript and single-subagent transcripts
//     (<sessionDir>/subagents/<id>.jsonl).
//   - Orchestrator hands off exactly ONE message to the twin pair — the
//     task description plus optional bounded context. The parent session
//     transcript is never passed in.
//   - Once spawned, mini-Coder and mini-Reviewer run their private
//     propose→review→execute loop autonomously, reusing the same logical
//     flow as internal/loop's review cycle but pointed at the twin's own
//     isolated transcript. (The loop package cannot be imported here
//     without a cycle — loop imports subagent; this package follows the
//     same self-contained approach.)
//   - mini-Reviewer invariant (§6.5): HasTools:false at the AgentConfig
//     level, enforced structurally. The model literally cannot call any
//     tool because the API schema sent to it contains no tools.
//   - mini-Coder gets read_file, write_file, run_command, and task_complete.
//     spawn_subagent is absent (depth guard, §6.8, added in that task).
//   - When the twin pair agrees the task is complete, Runner returns a
//     Result with the final summary, which the caller (Orchestrator /
//     main loop) appends to the main transcript as a single action_result
//     entry attributed to the twin pair (§6.9, wired in that task).
//   - A hard turn cap (DefaultMaxTurns) bounds the private loop against
//     the 15x token-cost finding from Phase 0 research.
package twinsubagent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/kaiizer777/triad/internal/agent"
	"github.com/kaiizer777/triad/internal/gitcommit"
	"github.com/kaiizer777/triad/internal/logger"
	"github.com/kaiizer777/triad/internal/tracelog"
	"github.com/kaiizer777/triad/internal/transcript"
)

// ---------------------------------------------------------------------------
// Speaker helpers
// ---------------------------------------------------------------------------

// CoderSpeakerLabel returns the speaker string used in the twin pair's own
// transcript for mini-Coder entries. Format: "Twin:<id>".
// This is distinct from "Subagent:<id>" so the two constructs remain
// visually separable in post-mortem transcript reads.
func CoderSpeakerLabel(id string) string {
	return transcript.SpeakerTwin + ":" + id
}

// SummaryAttributionLabel returns the Speaker value used for the single
// action_result entry appended to the MAIN transcript when the twin pair
// finishes. Format: "Twin:<id>" — the same as the mini-Coder's within-twin
// speaker so both the twin's internal transcript and the main transcript use
// a consistent label.
func SummaryAttributionLabel(id string) string {
	return transcript.SpeakerTwin + ":" + id
}

// ---------------------------------------------------------------------------
// Configuration constants
// ---------------------------------------------------------------------------

// DefaultMaxTurns caps how many total agent turns (mini-Coder + mini-Reviewer
// combined) the twin pair can consume. This is the primary mitigation against
// the 15x token-cost failure mode identified in Phase 0 research. The cap is
// on total turns rather than per-agent turns so a disagreement spiral (Coder
// proposes / Reviewer objects / Coder revises / ...) cannot burn the budget
// silently.
//
// Empirically 16 total turns (8 Coder + 8 Reviewer at most) covers the
// medium-complexity tasks the twin pair is designed for — read a few files,
// make a focused change, run the tests, confirm done.
const DefaultMaxTurns = 16

// DefaultMaxRetries is the cap on propose→object cycles for a single atomic
// action within the twin pair's private loop. Mirrors loop.DefaultMaxRetries.
const DefaultMaxRetries = 5

// ---------------------------------------------------------------------------
// Result
// ---------------------------------------------------------------------------

// Result is what a finished twin pair returns to the parent loop / Orchestrator.
// Only Summary and Truncated are surfaced to the human via the main transcript;
// TranscriptPath is kept for debugging; Turns is for observability.
type Result struct {
	// Summary is the twin pair's final summary. The caller appends this
	// to the main session transcript as a single action_result entry
	// attributed to the twin pair (§6.9). On a clean completion it is
	// derived from mini-Coder's last plain-text message before
	// task_complete; on truncation it is synthesized from trailing entries.
	Summary string

	// TranscriptPath is the path to the twin pair's isolated JSONL file.
	// Useful for debugging / post-mortem. Not surfaced in the main transcript.
	TranscriptPath string

	// Turns is how many total agent turns (mini-Coder + mini-Reviewer)
	// were consumed. Capped by Runner.maxTurns.
	Turns int

	// Truncated is true if the twin pair hit the turn cap before mini-Coder
	// called task_complete AND mini-Reviewer approved. The Summary field in
	// that case is synthesized from trailing transcript entries, not a real
	// completion summary.
	Truncated bool
}

// ---------------------------------------------------------------------------
// Runner
// ---------------------------------------------------------------------------

// Client is the subset of *agent.Client the Runner uses. Identical to the
// interface in the subagent package — defined separately to keep the
// packages independent.
type Client interface {
	Respond(ctx context.Context, cfg agent.AgentConfig, entries []transcript.Entry) (agent.AgentResponse, error)
}

// Runner drives one twin subagent pair to completion. Construct one per
// invocation via NewRunner; the struct is not safe for concurrent use.
type Runner struct {
	client         Client
	workDir        string
	sessionDir     string
	commandTimeout time.Duration
	maxTurns       int
	maxRetries     int
}

// NewRunner constructs a twin subagent runner.
//
// sessionDir is the directory under which the twin pair's per-run transcript
// lives — the twin's JSONL is written to <sessionDir>/twins/<id>.jsonl,
// which is physically separate from single-subagent transcripts
// (<sessionDir>/subagents/<id>.jsonl) and from the main session file.
//
// commandTimeout caps the mini-Coder's run_command executions; pass 0 to use
// the agent package's default. maxTurns caps the total number of agent turns
// the twin pair can consume; pass 0 to use DefaultMaxTurns.
func NewRunner(client Client, workDir, sessionDir string, commandTimeout time.Duration, maxTurns int) (*Runner, error) {
	if client == nil {
		return nil, fmt.Errorf("twinsubagent: client must not be nil")
	}
	if workDir == "" {
		return nil, fmt.Errorf("twinsubagent: workDir must not be empty")
	}
	if sessionDir == "" {
		return nil, fmt.Errorf("twinsubagent: sessionDir must not be empty")
	}
	if maxTurns <= 0 {
		maxTurns = DefaultMaxTurns
	}
	if commandTimeout <= 0 {
		commandTimeout = agent.DefaultCommandTimeout
	}
	return &Runner{
		client:         client,
		workDir:        workDir,
		sessionDir:     sessionDir,
		commandTimeout: commandTimeout,
		maxTurns:       maxTurns,
		maxRetries:     DefaultMaxRetries,
	}, nil
}

// TranscriptPath returns the path where this runner's twin transcript will
// be (or was) written, for a given id. Useful for callers that need the path
// before calling Run (e.g. to log a start-of-spawn entry, §6.15).
func (r *Runner) TranscriptPath(id string) string {
	return filepath.Join(r.sessionDir, "twins", id+".jsonl")
}

// Run executes the twin pair's private propose→review→execute loop until
// mini-Coder calls task_complete and mini-Reviewer approves, or the turn cap
// is hit, or ctx is cancelled.
//
// task is the focused description of what the twin pair should do.
// extraContext is optional bounded context (code excerpts, file paths) the
// parent hands in — same pattern as subagent.Runner.Run.
// parentCoderCfg is the parent Coder's agent config; mini-Coder and
// mini-Reviewer inherit BaseURL / APIKey / Model from it, but get their own
// system prompts, tool lists, and agent names.
//
// The twin pair's transcript is written to <sessionDir>/twins/<id>.jsonl as
// a side effect. The path is returned in Result.TranscriptPath.
func (r *Runner) Run(ctx context.Context, id, task, extraContext string, parentCoderCfg agent.AgentConfig) (Result, error) {
	if id == "" {
		return Result{}, fmt.Errorf("twinsubagent: id must not be empty")
	}
	if strings.TrimSpace(task) == "" {
		return Result{}, fmt.Errorf("twinsubagent: task must not be empty")
	}
	if parentCoderCfg.BaseURL == "" || parentCoderCfg.Model == "" {
		return Result{}, fmt.Errorf("twinsubagent: parentCoderCfg must have BaseURL and Model set")
	}

	transcriptPath := r.TranscriptPath(id)
	tr := transcript.NewTranscript(transcriptPath)

	// §6.3 — Seed the twin transcript with EXACTLY ONE message: task + context.
	// The parent session transcript is never passed in — the twin pair sees
	// only this seed and the results of their own tool calls.
	seed := task
	if extraContext != "" {
		seed = task + "\n\nContext:\n" + extraContext
	}
	if err := tr.Append(transcript.Entry{
		Speaker:   transcript.SpeakerYou,
		Type:      transcript.TypeMessage,
		Content:   seed,
		Timestamp: time.Now(),
	}); err != nil {
		return Result{}, fmt.Errorf("twinsubagent: failed to seed transcript: %w", err)
	}

	// §6.6 — Clarify phase: assess the task for ambiguity before the private
	// propose→review→execute loop starts. The twin pair is headless (no
	// interactive stdin), so if the task is ambiguous we append the clarify
	// block and immediately append a "proceeding with best-guess" proceed note
	// — the pair cannot pause and wait for a human reply. Clear tasks produce
	// no transcript entries; the loop starts immediately without overhead.
	RunClarifyPhase(task, tr, nil)

	// §6.5 — mini-Coder / mini-Reviewer configs. The constructors in
	// twinsubagent_config.go are the single source of truth for these
	// configs — exported so tests can assert on the structural invariants
	// (mini-Coder has a narrow tool set, mini-Reviewer has HasTools:false).
	miniCoder := MiniCoderConfig(parentCoderCfg, id)
	miniReviewer := MiniReviewerConfig(parentCoderCfg, id)

	logger.L().Info("twinsubagent starting",
		"id", id,
		"transcript", transcriptPath,
		"max_turns", r.maxTurns,
	)

	tracePath := tracelog.TracePathForSession(transcriptPath)
	_ = tracelog.Append(tracePath, tracelog.Entry{
		Entity:      fmt.Sprintf("twin:%s", id),
		EventType:   tracelog.EventTwinSpawn,
		Description: fmt.Sprintf("Spawned twin subagent %s for task: %s", id, task),
	})

	res := Result{TranscriptPath: transcriptPath}

	// §6.4 — Private propose→review→execute loop.
	// Mirrors internal/loop's runActiveCycle + runReviewCycle logic but:
	//   (a) uses the twin's own isolated transcript, not the main transcript
	//   (b) is self-contained to avoid an import cycle (loop imports subagent;
	//       this package follows the same self-contained approach)
	//   (c) is capped by maxTurns across the entire mini-Coder+mini-Reviewer
	//       exchange, not just the mini-Coder's calls
	totalTurns := 0

	for {
		// --- mini-Coder turn ---
		if err := ctx.Err(); err != nil {
			return res, fmt.Errorf("twinsubagent: context cancelled before coder turn %d: %w", totalTurns+1, err)
		}
		if totalTurns >= r.maxTurns {
			break // fall through to truncation path
		}

		coderResp, err := r.client.Respond(ctx, miniCoder, tr.Entries())
		if err != nil {
			return res, fmt.Errorf("twinsubagent: mini-Coder API call failed (turn %d): %w", totalTurns+1, err)
		}
		totalTurns++
		res.Turns = totalTurns

		if len(coderResp.ToolCalls) == 0 {
			// mini-Coder sent plain text (a plan or status update) — append and
			// give it another turn. Same behaviour as loop.runActiveCycle.
			_ = tr.Append(transcript.Entry{
				Speaker:   CoderSpeakerLabel(id),
				Type:      transcript.TypeMessage,
				Content:   coderResp.Text,
				Timestamp: time.Now(),
			})
			continue
		}

		toolCall := coderResp.ToolCalls[0]

		// If mini-Coder proposes multiple tool calls in one turn, accept the
		// first and append a note so the model can correct on the next turn.
		if len(coderResp.ToolCalls) > 1 {
			_ = tr.Append(transcript.Entry{
				Speaker:   transcript.SpeakerSystem,
				Type:      transcript.TypeMessage,
				Content:   fmt.Sprintf("Note: twin runner only processes the first of %d tool calls in this turn; please call tools one at a time.", len(coderResp.ToolCalls)),
				Timestamp: time.Now(),
			})
		}

		// Record the proposed action in the twin transcript.
		_ = tr.Append(transcript.Entry{
			Speaker:   CoderSpeakerLabel(id),
			Type:      transcript.TypeProposedAction,
			Content:   formatProposedAction(toolCall),
			Timestamp: time.Now(),
		})

		// --- Reviewer→approve/object inner loop ---
		approved, taskDone, reviewTurns, reviewErr := r.runReviewCycle(ctx, tr, id, toolCall, miniReviewer, miniCoder, totalTurns)
		totalTurns += reviewTurns
		res.Turns = totalTurns

		if reviewErr != nil {
			return res, reviewErr
		}

		if taskDone {
			// task_complete was called and approved. Extract the summary from the
			// twin's transcript and return a clean Result.
			res.Summary = r.extractCompletionSummary(tr, id)
			logger.L().Info("twinsubagent finished cleanly",
				"id", id, "turns", res.Turns, "summary_len", len(res.Summary),
			)
			_ = tracelog.Append(tracePath, tracelog.Entry{
				Entity:      fmt.Sprintf("twin:%s", id),
				EventType:   tracelog.EventTwinComplete,
				Description: fmt.Sprintf("Completed twin subagent %s (%d turns, truncated=false)", id, res.Turns),
			})
			return res, nil
		}

		if !approved {
			// runReviewCycle exhausted retries. Surface the deadlock as a system
			// note in the twin transcript and then hit the turn cap path below.
			_ = tr.Append(transcript.Entry{
				Speaker:   transcript.SpeakerSystem,
				Type:      transcript.TypeMessage,
				Content:   fmt.Sprintf("Approval deadlock on action %q after %d retries — turning cap applied.", toolCall.Function.Name, r.maxRetries),
				Timestamp: time.Now(),
			})
			break
		}
		// Approved and executed — continue the outer loop for the next mini-Coder turn.
	}

	// Turn cap reached (or deadlock surfaced) without clean task_complete.
	res.Truncated = true
	res.Summary = synthesizeTruncationSummary(tr.Entries(), id, r.maxTurns)
	logger.L().Warn("twinsubagent hit turn cap without task_complete",
		"id", id, "turns", res.Turns, "max_turns", r.maxTurns,
	)
	_ = tracelog.Append(tracePath, tracelog.Entry{
		Entity:      fmt.Sprintf("twin:%s", id),
		EventType:   tracelog.EventTwinComplete,
		Description: fmt.Sprintf("Completed twin subagent %s (%d turns, truncated=true)", id, res.Turns),
	})
	return res, nil
}

// runReviewCycle is the private Reviewer→approve/object loop for a single
// proposed tool call. It mirrors internal/loop.runReviewCycle but operates on
// the twin's own transcript and uses the twin's mini-Reviewer config.
//
// Returns:
//   - approved: true if the Reviewer eventually approved the action
//   - taskDone: true if task_complete was approved (caller should wrap up)
//   - turns: number of model API calls consumed in this review cycle
//   - err: non-nil on context cancellation or API failure
func (r *Runner) runReviewCycle(
	ctx context.Context,
	tr *transcript.Transcript,
	id string,
	toolCall agent.ToolCall,
	miniReviewer, miniCoder agent.AgentConfig,
	turnsBefore int,
) (approved bool, taskDone bool, turns int, err error) {
	isTaskComplete := toolCall.Function.Name == "task_complete"

	for attempt := 1; attempt <= r.maxRetries; attempt++ {
		if turns+turnsBefore >= r.maxTurns {
			// Hard cap — stop without approval.
			return false, false, turns, nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return false, false, turns, fmt.Errorf("twinsubagent: context cancelled during review (attempt %d): %w", attempt, ctxErr)
		}

		// --- mini-Reviewer turn ---
		reviewerResp, err := r.client.Respond(ctx, miniReviewer, tr.Entries())
		if err != nil {
			return false, false, turns, fmt.Errorf("twinsubagent: mini-Reviewer API call failed (attempt %d): %w", attempt, err)
		}
		turns++

		reviewerText := strings.TrimSpace(reviewerResp.Text)
		_ = tr.Append(transcript.Entry{
			Speaker:   transcript.SpeakerReviewer,
			Type:      transcript.TypeMessage,
			Content:   reviewerText,
			Timestamp: time.Now(),
		})

		upper := strings.ToUpper(reviewerText)
		if strings.HasPrefix(upper, "APPROVED") {
			// Approved.
			if isTaskComplete {
				return true, true, turns, nil
			}
			// Execute the tool.
			result, execErr := r.executeToolCall(tr, id, toolCall)
			if execErr != nil {
				return false, false, turns, execErr
			}
			_ = result // result already appended to transcript by executeToolCall
			return true, false, turns, nil
		}

		// Reviewer objected.
		if attempt >= r.maxRetries {
			// Retries exhausted.
			return false, false, turns, nil
		}

		// Give mini-Coder a turn to revise.
		if turns+turnsBefore >= r.maxTurns {
			return false, false, turns, nil
		}
		coderResp, err := r.client.Respond(ctx, miniCoder, tr.Entries())
		if err != nil {
			return false, false, turns, fmt.Errorf("twinsubagent: mini-Coder revision API call failed (attempt %d): %w", attempt, err)
		}
		turns++

		if len(coderResp.ToolCalls) == 0 {
			// mini-Coder sent a message instead of a revised proposal.
			_ = tr.Append(transcript.Entry{
				Speaker:   CoderSpeakerLabel(id),
				Type:      transcript.TypeMessage,
				Content:   coderResp.Text,
				Timestamp: time.Now(),
			})
			continue
		}
		// Coder provided a revised proposal.
		toolCall = coderResp.ToolCalls[0]
		isTaskComplete = toolCall.Function.Name == "task_complete"
		_ = tr.Append(transcript.Entry{
			Speaker:   CoderSpeakerLabel(id),
			Type:      transcript.TypeProposedAction,
			Content:   formatProposedAction(toolCall),
			Timestamp: time.Now(),
		})
	}

	return false, false, turns, nil
}

// executeToolCall dispatches an approved tool call for the mini-Coder.
// Allowed tools: read_file, write_file, run_command. Any other tool name
// results in a system note (the reviewer should have caught it; this is a
// belt-and-suspenders guard).
func (r *Runner) executeToolCall(tr *transcript.Transcript, id string, tc agent.ToolCall) (string, error) {
	switch tc.Function.Name {
	case "read_file", "write_file", "run_command":
		result, err := agent.ExecuteTool(r.workDir, tc, r.commandTimeout)
		resultContent := result
		if err != nil {
			resultContent = "ERROR: " + err.Error()
		}
		_ = tr.Append(transcript.Entry{
			Speaker:   transcript.SpeakerSystem,
			Type:      transcript.TypeActionResult,
			Content:   resultContent,
			Timestamp: time.Now(),
		})

		// Auto-commit any filesystem changes made by write_file or run_command,
		// with a commit message that attributes the change to the twin pair.
		// Commit messages use "[triad:twin #<id>]" prefix per §6.7 (wired fully
		// in that task, but the basic commit attribution is set up here so any
		// changes during 6.1–6.5 testing are already correctly attributed).
		if err == nil && gitcommit.IsRepo(r.workDir) {
			r.commitTwinChanges(tr, id, tc)
		}
		return resultContent, nil

	default:
		// §6.8 — Nesting/depth guard (belt-and-suspenders):
		// spawn_subagent and spawn_twin_subagent are structurally absent
		// from MiniCoderTools() (the API schema sent to the model contains
		// no definition for them), so a well-behaved model will never call
		// them. If one slips through (e.g. due to a model hallucination or
		// a tool-list bug), we reject it here with an explicit message that
		// names the specific guard, rather than the generic "not allowed" note.
		var note string
		if tc.Function.Name == "spawn_subagent" || tc.Function.Name == "spawn_twin_subagent" {
			note = fmt.Sprintf(
				"Depth guard: tool %q is not permitted inside a twin subagent (depth stops at one level, §6.8). "+
					"Use read_file, write_file, or run_command to complete the task directly.",
				tc.Function.Name,
			)
		} else {
			note = fmt.Sprintf("Tool %q is not allowed for mini-Coder (allowed: read_file, write_file, run_command, task_complete).", tc.Function.Name)
		}
		_ = tr.Append(transcript.Entry{
			Speaker:   transcript.SpeakerSystem,
			Type:      transcript.TypeMessage,
			Content:   note,
			Timestamp: time.Now(),
		})
		return note, nil
	}
}

// commitTwinChanges commits any filesystem changes made by the twin pair's
// mini-Coder, with a commit message that clearly attributes the change to
// the twin subagent (§6.7). The "[triad:twin #<id>]" prefix makes twin
// commits visually distinguishable from main-session commits in git log.
func (r *Runner) commitTwinChanges(tr *transcript.Transcript, id string, tc agent.ToolCall) {
	paths, err := gitcommit.ChangedPaths(r.workDir)
	if err != nil || len(paths) == 0 {
		return
	}

	intentTool := tc.Function.Name
	intentArg := ""
	if tc.Function.Arguments != "" && tc.Function.Arguments != "{}" {
		var m map[string]any
		if jsonErr := json.Unmarshal([]byte(tc.Function.Arguments), &m); jsonErr == nil {
			for _, k := range []string{"path", "command"} {
				if v, ok := m[k].(string); ok {
					intentArg = v
					break
				}
			}
		}
	}
	intent := fmt.Sprintf("[triad:twin #%s] %s: %s", id, intentTool, intentArg)

	msg := gitcommit.CommitMessage{
		EntryID:     lastActionResultID(tr),
		Intent:      intent,
		ToolName:    tc.Function.Name,
		SessionPath: r.sessionDir,
		ProposedBy:  CoderSpeakerLabel(id),
		ApprovedBy:  transcript.SpeakerReviewer + "-twin:" + id,
	}
	if _, cerr := gitcommit.CommitAction(r.workDir, paths, msg); cerr != nil {
		if !gitcommit.IsNotConfigured(cerr) {
			_ = tr.Append(transcript.Entry{
				Speaker:   transcript.SpeakerSystem,
				Type:      transcript.TypeMessage,
				Content:   fmt.Sprintf("twin auto-commit failed: %v", cerr),
				Timestamp: time.Now(),
			})
		}
	}
}

// extractCompletionSummary builds the final summary from the twin pair's
// transcript on a clean task_complete completion. It collects the mini-Coder's
// last few plain-text messages (reasoning / status updates) and produces a
// compact summary the Orchestrator can include in the main transcript.
func (r *Runner) extractCompletionSummary(tr *transcript.Transcript, id string) string {
	entries := tr.Entries()
	coderSpeaker := CoderSpeakerLabel(id)

	const maxExcerpts = 3
	var excerpts []string
	for i := len(entries) - 1; i >= 0 && len(excerpts) < maxExcerpts; i-- {
		e := entries[i]
		if e.Speaker != coderSpeaker {
			continue
		}
		if e.Type != transcript.TypeMessage {
			continue
		}
		text := strings.TrimSpace(e.Content)
		if text == "" {
			continue
		}
		excerpts = append([]string{text}, excerpts...)
	}
	if len(excerpts) == 0 {
		return fmt.Sprintf("[twin %s]: task_complete signalled (no mini-Coder messages recorded)", id)
	}
	return strings.Join(excerpts, "\n---\n")
}

// synthesizeTruncationSummary builds a fallback summary when the turn cap is
// hit before task_complete. Mirrors subagent.synthesizeTruncationSummary.
func synthesizeTruncationSummary(entries []transcript.Entry, id string, maxTurns int) string {
	coderSpeaker := CoderSpeakerLabel(id)
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[twin %s hit %d-turn cap; partial findings — treat as low-confidence]\n\n", id, maxTurns))

	const maxExcerpts = 4
	count := 0
	for i := len(entries) - 1; i >= 0 && count < maxExcerpts; i-- {
		e := entries[i]
		if e.Speaker != coderSpeaker {
			continue
		}
		if e.Type != transcript.TypeMessage {
			continue
		}
		sb.WriteString("- ")
		sb.WriteString(strings.TrimSpace(e.Content))
		sb.WriteString("\n")
		count++
	}
	if count == 0 {
		sb.WriteString("(no mini-Coder messages recorded before the cap)\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

// lastActionResultID returns the ID of the most recent action_result entry
// in the twin pair's transcript. Used for the auto-commit message reference.
func lastActionResultID(tr *transcript.Transcript) int {
	entries := tr.Entries()
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Type == transcript.TypeActionResult {
			return entries[i].ID
		}
	}
	return 0
}

// formatProposedAction renders a ToolCall into "Tool: X\nArguments: Y" format.
// Duplicated here (not imported from loop) to avoid the import cycle between
// loop → subagent and loop → twinsubagent. Keep in sync with
// loop.FormatProposedAction if that format ever changes.
func formatProposedAction(tc agent.ToolCall) string {
	if tc.Function.Arguments == "" || tc.Function.Arguments == "{}" {
		return "Tool: " + tc.Function.Name + "\n(no arguments)"
	}
	return "Tool: " + tc.Function.Name + "\nArguments: " + tc.Function.Arguments
}
