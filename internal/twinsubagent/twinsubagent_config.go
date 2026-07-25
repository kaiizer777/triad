package twinsubagent

import (
	"github.com/kaiizer777/triad/internal/agent"
	"github.com/kaiizer777/triad/internal/transcript"
)

// MiniCoderConfig builds the AgentConfig used for the mini-Coder half of a
// twin subagent pair. Exported so callers (e.g. tests) can inspect the
// config-level invariants the runner relies on — specifically that the
// mini-Coder gets a narrow tool set (read_file, write_file, run_command,
// task_complete) and notably does NOT get spawn_subagent (the depth guard
// from work.md §6.8 is structurally enforced by the tool list, not just
// the system prompt).
//
// Inherits transport (BaseURL / APIKey / Model) from parent and assigns
// its own Name, SystemPrompt, and Tools list.
func MiniCoderConfig(parent agent.AgentConfig, id string) agent.AgentConfig {
	return agent.AgentConfig{
		Name:         CoderSpeakerLabel(id),
		BaseURL:      parent.BaseURL,
		APIKey:       parent.APIKey,
		Model:        parent.Model,
		HasTools:     true,
		SystemPrompt: MiniCoderSystemPrompt,
		Tools:        MiniCoderTools(),
	}
}

// MiniReviewerConfig builds the AgentConfig used for the mini-Reviewer half
// of a twin subagent pair. This is the structural enforcement point of the
// "Reviewer never has tool access" invariant from work.md §6.5 and the
// core invariant from Workflow 1 §6.2: HasTools is false and Tools is nil,
// so the agent.Client sends NO tools field in the API request body — the
// model literally cannot call any tool, regardless of what its prompt
// says or what the response contains. The existing client-level test
// (TestClient_Respond_ToolsAttachedOnlyForCoder) covers the wire-level
// half of this invariant; this constructor plus the test that exercises
// it (TestMiniReviewerConfig_HasNoToolAccess, §6.12) cover the
// twinsubagent-package half.
//
// Exported (rather than kept package-private) so the test can assert on
// the actual config without reaching into Runner internals.
func MiniReviewerConfig(parent agent.AgentConfig, id string) agent.AgentConfig {
	return agent.AgentConfig{
		Name:         transcript.SpeakerReviewer + "-twin:" + id,
		BaseURL:      parent.BaseURL,
		APIKey:       parent.APIKey,
		Model:        parent.Model,
		HasTools:     false,
		SystemPrompt: MiniReviewerSystemPrompt,
		Tools:        nil,
	}
}
