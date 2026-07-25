package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/kaiizer777/triad/internal/agent"
	"github.com/kaiizer777/triad/internal/loop"
	"github.com/kaiizer777/triad/internal/subagent"
	"github.com/kaiizer777/triad/internal/transcript"
)

// cmdCoderTurn invokes the Coder agent asynchronously.
func cmdCoderTurn(tr *transcript.Transcript, coder agent.AgentConfig, client loop.AgentClient) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		resp, err := client.Respond(ctx, coder, tr.Entries())
		return agentResponseMsg{
			speaker: transcript.SpeakerCoder,
			resp:    resp,
			err:     err,
		}
	}
}

// cmdReviewerTurn invokes the Reviewer agent asynchronously.
func cmdReviewerTurn(tr *transcript.Transcript, reviewer agent.AgentConfig, client loop.AgentClient) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		resp, err := client.Respond(ctx, reviewer, tr.Entries())
		return agentResponseMsg{
			speaker: transcript.SpeakerReviewer,
			resp:    resp,
			err:     err,
		}
	}
}

// cmdExecuteTool executes an approved tool call asynchronously.
// commandTimeout caps run_command executions; 0 uses agent.DefaultCommandTimeout.
func cmdExecuteTool(workDir string, toolCall agent.ToolCall, commandTimeout time.Duration) tea.Cmd {
	return func() tea.Msg {
		res, err := agent.ExecuteTool(workDir, toolCall, commandTimeout)
		return toolResultMsg{
			toolCall: toolCall,
			result:   res,
			err:      err,
		}
	}
}

// cmdSpawnSubagent runs an approved spawn_subagent tool call in the
// background and returns a toolResultMsg so the existing toolResultMsg
// handler in update.go picks it up unchanged (no TUI-side special
// case for the result). The header / truncation tag is built here
// and prepended to the summary, matching the headless loop's
// behaviour (docs/work2.md §3.2.5).
//
// sessionFilePath is the parent session's JSONL file path; the
// subagent's own transcript lands next to it under <dir>/subagents/.
// coder is the parent Coder config — the subagent inherits BaseURL /
// APIKey / Model from it. client is the shared agent client. The
// subagent's own system prompt, tool set, and depth guard all live in
// the subagent package.
func cmdSpawnSubagent(
	sessionFilePath, workDir string,
	coder agent.AgentConfig,
	client loop.AgentClient,
	commandTimeout time.Duration,
	toolCall agent.ToolCall,
) tea.Cmd {
	return func() tea.Msg {
		var args agent.SpawnSubagentArgs
		if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &args); err != nil {
			return toolResultMsg{
				toolCall: toolCall,
				result:   fmt.Sprintf("ERROR: spawn_subagent: failed to parse arguments: %v", err),
				err:      err,
			}
		}
		if strings.TrimSpace(args.Task) == "" {
			err := fmt.Errorf("spawn_subagent: required argument 'task' is missing or empty")
			return toolResultMsg{
				toolCall: toolCall,
				result:   "ERROR: " + err.Error(),
				err:      err,
			}
		}

		sessionDir := filepath.Dir(sessionFilePath)
		if sessionDir == "" || sessionDir == "." {
			sessionDir = filepath.Join(workDir, "sessions")
		}

		runner, err := subagent.NewRunner(
			client,
			workDir,
			sessionDir,
			commandTimeout,
			0, // use default turn cap
			0, // depth 0 — top-level (subagents can't themselves spawn)
		)
		if err != nil {
			return toolResultMsg{
				toolCall: toolCall,
				result:   fmt.Sprintf("ERROR: spawn_subagent: %v", err),
				err:      err,
			}
		}

		id := subagent.NewID()
		res, runErr := runner.Run(context.Background(), id, args.Task, args.Context, coder)
		if runErr != nil {
			if res.Summary != "" {
				return toolResultMsg{
					toolCall: toolCall,
					result:   fmt.Sprintf("[subagent %s partial] %s\n\nerror: %v", id, res.Summary, runErr),
					err:      runErr,
				}
			}
			return toolResultMsg{
				toolCall: toolCall,
				result:   fmt.Sprintf("ERROR: spawn_subagent: %v", runErr),
				err:      runErr,
			}
		}

		header := fmt.Sprintf("Subagent %s: ", id)
		if res.Truncated {
			header = fmt.Sprintf("Subagent %s (truncated, %d turns): ", id, res.Turns)
		}
		return toolResultMsg{
			toolCall: toolCall,
			result:   header + res.Summary,
		}
	}
}
