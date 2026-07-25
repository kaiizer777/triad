// Package loop implements the core Coder→Reviewer approval loop for the Triad session.
//
// The loop drives the headless (no TUI) coder-proposes/reviewer-checks/execute-or-revise
// cycle described in PROJECT_SPEC.md §6.3. It is designed to be wired up by main.go for
// Phase 4, and later adapted (not directly ported) into bubbletea Commands for Phase 6.
package loop

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kaiizer777/triad/internal/agent"
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
		transcript: t,
		coder:      coder,
		reviewer:   reviewer,
		client:     client,
		workDir:    workDir,
		MaxRetries: DefaultMaxRetries,
	}
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
			result, execErr := agent.ExecuteTool(l.workDir, toolCall, agent.DefaultCommandTimeout)
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

