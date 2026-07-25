package tui

import (
	"github.com/kaiizer777/triad/internal/agent"
)

// humanInputMsg is sent when the user presses Enter in the input box.
type humanInputMsg struct {
	content string
}

// agentResponseMsg is sent when an agent (Coder or Reviewer) returns an API response.
type agentResponseMsg struct {
	speaker string // "Coder" or "Reviewer"
	resp    agent.AgentResponse
	err     error
}

// toolResultMsg is sent when an approved tool call completes execution.
type toolResultMsg struct {
	toolCall agent.ToolCall
	result   string
	err      error
}
