package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/kaiizer777/triad/internal/agent"
	"github.com/kaiizer777/triad/internal/loop"
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
