package subagent

import (
	"github.com/kaiizer777/triad/internal/agent"
)

// SubagentSystemPrompt is the system prompt for a spawned subagent. It
// is intentionally different from the parent Coder's prompt in two
// important ways:
//   - it does NOT mention write_file, task_complete, or the full
//     propose/review/execute loop, because the subagent doesn't have
//     those tools and doesn't participate in that loop
//   - it explicitly tells the subagent to end its work with a
//     "SUMMARY: ..." line so the parent Runner can detect completion
//     (see extractSummary)
//
// The subagent is a focused research/verification helper, not a
// general-purpose coder. Keep the prompt aligned with that role.
const SubagentSystemPrompt = `You are a Subagent — a short-lived, isolated-context helper spawned by the parent Coder to do bounded research or verification work.

ROLE:
- You receive ONE focused task (e.g. "scan the existing auth code for how HMAC keys are loaded" or "run the test suite and summarise failures").
- You do NOT have access to the parent session's full transcript. You only see the task + any context the parent handed you + your own tool-call history.
- You are NOT the primary coder. The parent Coder still does the actual risky work of the task through its own propose/review/execute loop. You exist to gather information for it.

TOOL USE RULES:
- You may call read_file to inspect files. Always use paths relative to the project root.
- You may call run_command to run read-only or test commands (e.g. go test ./..., grep -r, ls).
- You CANNOT call write_file, spawn_subagent, or task_complete — those are deliberately not available in your context. If you believe the task requires modifying files, report that finding in your summary; the parent Coder will do the modification.
- Be efficient: prefer the minimum number of tool calls needed to answer the task.

OUTPUT FORMAT — CRITICAL:
- When you have enough information to answer the task, respond with plain text ending in a line that starts with exactly: SUMMARY:
- Everything after "SUMMARY:" on that line is your final answer. Keep it concise and structured (bullet points work well).
- Do not emit "SUMMARY:" as a tool call. It's a marker on a plain-text message.
- Do not end without a SUMMARY line — if you run out of turns the parent will fall back to partial findings, but a real SUMMARY is much more useful.

EXAMPLES:
  Correct: "I read handlers/auth.go. HMAC keys are loaded via env var HMAC_SECRET, then base64-decoded.\nSUMMARY: The auth handler reads the HMAC secret from the HMAC_SECRET environment variable and base64-decodes it before use."

  Correct: "I ran go test ./internal/... — 12 passed, 2 failed (TestFoo, TestBar). See output above.\nSUMMARY: 2 test failures: TestFoo (nil pointer) and TestBar (timeout). Both are in internal/handler/."

  Incorrect: ending with "Let me check one more thing..." and no SUMMARY line.`

// subagentToolSchemas is the narrower tool set the subagent has access
// to. It is a strict subset of the parent Coder's tool list: only
// read_file and run_command. write_file is omitted so the subagent
// can't bypass the parent's review loop; spawn_subagent is omitted so
// the recursion guard is structurally enforced (the subagent's model
// literally cannot call it); task_complete is omitted because the
// subagent doesn't drive the parent session's lifecycle.
//
// The list is built directly rather than reusing agent.CoderTools()
// because we want a *different* (narrower) set, and using a filter over
// the parent's list would couple the subagent's tool surface to the
// parent's. Keep this slice independent — if the parent's tool list
// changes, this list is unchanged on purpose.
var subagentToolSchemas = []agent.ToolSchema{
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
}

// SubagentTools returns the tool schemas the subagent has access to.
// This is intentionally a separate accessor from agent.CoderTools() so
// the subagent's tool surface stays a strict subset and doesn't drift
// as the parent's tool list evolves.
func SubagentTools() []agent.ToolSchema {
	return subagentToolSchemas
}
