// Package loop implements the core Coder→Reviewer approval loop for the Triad session.
//
// The loop drives the headless (no TUI) coder-proposes/reviewer-checks/execute-or-revise
// cycle described in PROJECT_SPEC.md §6.3. It is designed to be wired up by main.go for
// Phase 4, and later adapted (not directly ported) into bubbletea Commands for Phase 6.
package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/kaiizer777/triad/internal/agent"
	"github.com/kaiizer777/triad/internal/browser"
	"github.com/kaiizer777/triad/internal/clarify"
	"github.com/kaiizer777/triad/internal/gitcommit"
	"github.com/kaiizer777/triad/internal/subagent"
	"github.com/kaiizer777/triad/internal/transcript"
)

// SessionState represents whether the loop is waiting for work or actively processing a task.
type SessionState int

const (
	// StateIdle means the session is waiting for the next human task.
	StateIdle SessionState = iota
	// StateActive means the coder/reviewer cycle is running.
	StateActive
)

// Decision is the result of parsing Reviewer's plain-text response.
type Decision int

const (
	DecisionApprove Decision = iota
	DecisionObject
)

// DefaultMaxRetries is the default cap on propose→object cycles per atomic action.
const DefaultMaxRetries = 5

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

	// CurrentMode controls task execution mode (orchestrator | general | triad).
	CurrentMode Mode

	// SearchAPIKey holds the Firecrawl API key used by the web_search tool.
	SearchAPIKey string

	// pendingClarify is the most recent clarify.Batch we asked the
	// human about. When non-nil, the next human message is treated
	// as a clarification reply (or a proceed signal) rather than a
	// fresh task — the loop does not re-clarify the reply itself,
	// and a proceed signal unblocks the task with the stated
	// best-guess interpretation. Cleared on proceed, on
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

	// effectiveMode is the mode used for the current active cycle. It is set
	// by the orchestrator routing gate (for ModeOrchestrator tasks) or
	// directly from CurrentMode (for manually forced modes). runActiveCycle
	// reads this field — not CurrentMode — so Orchestrator can redirect a
	// single task to General or Triad without changing the session-level mode.
	effectiveMode Mode
}

// New creates a Loop ready to run. workDir is the project root used for tool execution.
func New(
	t *transcript.Transcript,
	coder agent.AgentConfig,
	reviewer agent.AgentConfig,
	client AgentClient,
	workDir string,
) *Loop {
	return &Loop{
		transcript:  t,
		coder:       coder,
		reviewer:    reviewer,
		client:      client,
		workDir:     workDir,
		MaxRetries:  DefaultMaxRetries,
		CurrentMode: ModeOrchestrator,
	}
}

// SetSearchAPIKey sets the Firecrawl API key used by web_search tool calls.
func (l *Loop) SetSearchAPIKey(key string) {
	l.SearchAPIKey = key
}

// SetBrowser attaches a browser.Manager to the loop so that approved
// browser_* tool calls can be executed. Pass nil to detach (and
// disable browser tools for subsequent tool calls; the schema still
// appears in the model, but calls will surface a "browser not
// configured" error). The manager is owned by the caller — the loop
// does not Close it on shutdown.
func (l *Loop) SetBrowser(m *browser.Manager) {
	l.Browser = m
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
				// --- Non-orchestrator: Clarify phase (Phase 3) ---
				//
				// If we have a pending clarify round, this message is a
				// REPLY to it (answers or a proceed signal) — process
				// the reply without re-clarifying.
				//
				// Otherwise, this is a fresh task. Assess it: if it's
				// ambiguous, append a single batched System entry
				// asking the questions, set pendingClarify, and DO
				// NOT start the active cycle. The loop returns to
				// the top of Run, waits for the human's reply, and
				// re-enters this branch with pendingClarify set.
				if l.pendingClarify != nil {
					if clarify.IsProceedCommand(msg) {
						// "just proceed" / /proceed — record the
						// best-guess interpretation in the
						// transcript and unblock the active cycle
						// using the original task (the first
						// entry of the pending round).
						_ = l.append(transcript.Entry{
							Speaker:   transcript.SpeakerSystem,
							Type:      transcript.TypeMessage,
							Content:   clarify.FormatProceedNote(*l.pendingClarify),
							Timestamp: time.Now(),
						})
						l.pendingClarify = nil
						// Fall through to the active cycle below.
					} else {
						// Real answers (or any non-proceed reply).
						// The human's reply is already in the
						// transcript as a [You] entry; record a
						// short System ack so the next agent
						// turn sees the answers were received,
						// then continue with the active cycle.
						_ = l.append(transcript.Entry{
							Speaker:   transcript.SpeakerSystem,
							Type:      transcript.TypeMessage,
							Content:   "[System]: Clarification received — proceeding.",
							Timestamp: time.Now(),
						})
						l.pendingClarify = nil
						// Fall through to the active cycle below.
					}
				} else {
					// Fresh task — run the shared clarify step.
					batch := clarify.AssessAmbiguity(msg)
					if batch.NeedsClarification {
						_ = l.append(transcript.Entry{
							Speaker:   transcript.SpeakerSystem,
							Type:      transcript.TypeMessage,
							Content:   clarify.FormatClarifyBlock(batch),
							Timestamp: time.Now(),
						})
						// Stash the batch for the next message.
						// IMPORTANT: we capture by value into the
						// pointer field. The next message will be
						// processed as a reply (or proceed), not
						// re-clarified.
						stored := batch
						l.pendingClarify = &stored
						// Stay idle until the human replies or
						// says "proceed". We deliberately do
						// NOT consume the active cycle here.
						continue
					}
					// Task is unambiguous — proceed. We do NOT
					// emit a System note for this case (the
					// transcript would be noisy on every clear
					// task); the doc only requires the entry
					// when there was an actual clarification
					// round or a proceed signal.
				}
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
	for {
		// --- Drain any human messages typed since the last agent turn (Phase 5) ---
		if err := l.drainInput(); err != nil {
			return false, err
		}

		// --- Coder turn ---
		coderResp, err := l.client.Respond(ctx, l.coder, l.transcript.Entries())
		if err != nil {
			return false, fmt.Errorf("coder API call failed: %w", err)
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
			case toolCall.Function.Name == "spawn_subagent":
				result, execErr = l.runSpawnSubagent(ctx, toolCall)
			case browser.IsBrowserTool(toolCall.Function.Name):
				result, execErr = l.runBrowserTool(toolCall)
			case toolCall.Function.Name == "web_search":
				result, execErr = l.runWebSearch(toolCall)
			default:
				result, execErr = agent.ExecuteTool(l.workDir, toolCall, agent.DefaultCommandTimeout)
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
			// Continue the loop — Coder should call a tool on the next turn.
			continue
		}

		// Coder has proposed a tool call (or task_complete).
		// For now handle the first tool call only — one atomic action at a time per spec.
		toolCall := coderResp.ToolCalls[0]

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
			case toolCall.Function.Name == "spawn_subagent":
				result, execErr = l.runSpawnSubagent(ctx, toolCall)
			case browser.IsBrowserTool(toolCall.Function.Name):
				result, execErr = l.runBrowserTool(toolCall)
			case toolCall.Function.Name == "web_search":
				result, execErr = l.runWebSearch(toolCall)
			default:
				result, execErr = agent.ExecuteTool(l.workDir, toolCall, agent.DefaultCommandTimeout)
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
				// Give Coder a turn to revise. The objection is already in the transcript.
				coderResp, err := l.client.Respond(ctx, l.coder, l.transcript.Entries())
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

// runBrowserTool is the headless loop's handler for approved
// browser_* tool calls (docs/work2.md §4.2). It is structurally
// similar to runSpawnSubagent — the tool has long-lived state
// (the Chromium process / shared page) that ExecuteTool doesn't
// have, so the loop owns that state and dispatches the call here.
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
	return l.Browser.ExecuteTool(l.workDir, toolCall.Function.Name, toolCall.Function.Arguments)
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

