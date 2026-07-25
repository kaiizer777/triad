package twinsubagent

import (
	"github.com/kaiizer777/triad/internal/agent"
)

// MiniCoderSystemPrompt is the system prompt for the mini-Coder half of a
// twin subagent pair. It is intentionally different from the parent main-session
// Coder's prompt in a few important ways:
//
//   - It does NOT mention spawn_subagent or nested twins — those tools are
//     structurally absent from the mini-Coder's tool list (depth guard, §6.8).
//   - It explicitly references the private propose→review→execute loop it
//     participates in, so it knows to wait for a Reviewer response.
//   - It knows to call task_complete when done so the twin pair's loop can exit.
//
// The mini-Coder is a focused code-change executor, not a general-purpose
// conversation partner. Keep this prompt aligned with that role.
const MiniCoderSystemPrompt = `You are a mini-Coder — one half of an isolated twin subagent pair spawned by the Orchestrator to handle a medium-complexity task.

CONTEXT:
- You received exactly ONE task description. There is no parent session transcript visible to you.
- You work alongside a mini-Reviewer in a private propose→review→execute loop.
- Your tool calls are shown to the mini-Reviewer before execution. They can approve or object.
- On approval, the tool runs and results appear in this transcript.
- On objection, you must revise your proposal and try again.

TOOL USE RULES:
- You may call: read_file, write_file, run_command.
- spawn_subagent and nested twin spawning are NOT available — you are already one level of delegation.
- Use the minimum tool calls required to complete the task.
- After completing all necessary edits and verification, call task_complete.

WORKFLOW:
1. Read relevant files to understand context.
2. Propose your changes via write_file or run_command.
3. If the mini-Reviewer objects, read their objection, revise, and re-propose.
4. Verify your changes (e.g. run tests with run_command).
5. When the task is done and verified, call task_complete.

OUTPUT FORMAT:
- Plain text reasoning between tool calls is fine and encouraged.
- End your work with task_complete — do NOT emit a "SUMMARY:" line (that is the twin pair runner's job, not yours).`

// MiniReviewerSystemPrompt is the system prompt for the mini-Reviewer half of
// a twin subagent pair. Critical invariant (§6.5): the mini-Reviewer MUST NOT
// call any tools — ever. This is enforced both here (the prompt explicitly
// states it) and structurally (the mini-Reviewer AgentConfig has HasTools:false
// and a nil Tools list, so the API schema sent to the model contains no tools).
//
// The review vocabulary is intentionally identical to the main-session Reviewer
// (APPROVED / OBJECTION:) so the twin pair's loop can reuse the same
// ParseReviewerDecision logic without modification.
const MiniReviewerSystemPrompt = `You are a mini-Reviewer — one half of an isolated twin subagent pair. Your sole job is to review proposed tool calls from the mini-Coder and approve or object.

CRITICAL INVARIANT — NO TOOL ACCESS:
You do NOT have any tools. You cannot call read_file, write_file, run_command, or anything else. Your only output is a plain-text approval or objection. This is a hard constraint, not a suggestion.

REVIEW CRITERIA:
- Approve proposals that are safe, correct, and scoped to the stated task.
- Object to proposals that: are overly broad, touch files outside the task scope, could cause data loss, use dangerous shell commands (rm -rf, DROP TABLE, etc.), or appear to be attempting to bypass restrictions.
- Object to any attempt by the mini-Coder to call spawn_subagent, spawn_twin_subagent, or similar delegation tools — those are not allowed at this depth.

RESPONSE FORMAT — CRITICAL:
- Start with exactly "APPROVED" (if approving) or "OBJECTION:" (if objecting).
- "APPROVED" on the first line means the tool call executes immediately.
- "OBJECTION: <reason>" means the mini-Coder must revise and re-propose.
- Do not include any tool calls in your response — they will be ignored and may cause errors.

EXAMPLES:
  Correct approval:   "APPROVED. The write_file call looks correct and scoped to the task."
  Correct objection:  "OBJECTION: The run_command uses rm -rf which is too destructive. Please use a more targeted deletion."
  Incorrect:          Calling any tool. This is never allowed.`

// SummaryPrefix is the marker the twin pair's runner emits (on behalf of the
// completed pair) at the start of the final summary message appended to the
// main session transcript. Using the same prefix as the single subagent
// package keeps the result-detection logic uniform.
const SummaryPrefix = "SUMMARY:"

// SpeakerTwinPrefix is the first component of the speaker label used for
// twin-pair entries ("Twin"). It is exported so callers (e.g. tests) can
// build expected speaker strings ("Twin:<id>") and commit prefixes
// ("[triad:twin #<id>]") without importing the transcript package directly.
// It mirrors transcript.SpeakerTwin — keep the two in sync.
const SpeakerTwinPrefix = "Twin"


// miniCoderToolSchemas is the tool set available to the mini-Coder.
// It includes read_file, write_file, and run_command — the same set
// the main-session Coder uses for actual code edits. spawn_subagent
// is intentionally absent (depth guard, §6.8). Browser and web_search
// tools are also excluded to keep twin scope tightly bounded to
// code-change work.
//
// task_complete is included so the mini-Coder can signal it is done.
// The twin pair's loop intercepts this call and begins the
// shutdown/summary sequence rather than executing it as a side-effect.
var miniCoderToolSchemas = []agent.ToolSchema{
	{
		Type: "function",
		Function: agent.ToolFunctionSpec{
			Name:        "read_file",
			Description: "Read and return the content of a file at the given path (relative to the project working directory).",
			Parameters: agent.ToolParamSchema{
				Type: "object",
				Properties: map[string]agent.ToolParamProperty{
					"path": {
						Type:        "string",
						Description: "Relative path to the file (e.g. 'internal/handler.go'). Must not be absolute or contain '..'.",
					},
				},
				Required: []string{"path"},
			},
		},
	},
	{
		Type: "function",
		Function: agent.ToolFunctionSpec{
			Name:        "write_file",
			Description: "Write content to a file at the given path (relative to the project working directory). Creates the file and any missing parent directories if needed. Overwrites existing content.",
			Parameters: agent.ToolParamSchema{
				Type: "object",
				Properties: map[string]agent.ToolParamProperty{
					"path": {
						Type:        "string",
						Description: "Relative path to the file to write (e.g. 'internal/handler.go').",
					},
					"content": {
						Type:        "string",
						Description: "The full content to write to the file.",
					},
				},
				Required: []string{"path", "content"},
			},
		},
	},
	{
		Type: "function",
		Function: agent.ToolFunctionSpec{
			Name:        "run_command",
			Description: "Execute a shell command in the project working directory and return its combined stdout and stderr output along with the exit code.",
			Parameters: agent.ToolParamSchema{
				Type: "object",
				Properties: map[string]agent.ToolParamProperty{
					"command": {
						Type:        "string",
						Description: "The shell command to run (e.g. 'go test ./...' or 'grep -r TODO internal/').",
					},
				},
				Required: []string{"command"},
			},
		},
	},
	{
		Type: "function",
		Function: agent.ToolFunctionSpec{
			Name:        "task_complete",
			Description: "Signal that the task is fully complete. Call this only when all requested changes are made and verified. The twin pair's runner will then produce a summary and return control to the Orchestrator.",
			Parameters: agent.ToolParamSchema{
				Type:       "object",
				Properties: map[string]agent.ToolParamProperty{},
				Required:   []string{},
			},
		},
	},
}

// MiniCoderTools returns the tool schemas available to the mini-Coder.
// This is intentionally a separate accessor from agent.CoderTools() so
// the mini-Coder's tool surface stays independent and doesn't drift
// as the parent's tool list evolves.
func MiniCoderTools() []agent.ToolSchema {
	return miniCoderToolSchemas
}
