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
	"github.com/kaiizer777/triad/internal/browser"
	"github.com/kaiizer777/triad/internal/loop"
	"github.com/kaiizer777/triad/internal/skills"
	"github.com/kaiizer777/triad/internal/subagent"
	"github.com/kaiizer777/triad/internal/transcript"
)

// cmdCoderTurn invokes the Coder agent asynchronously with the
// Stage-1 / Stage-2 skills funnel applied. The funnel wrapper:
//  1. Builds a per-turn Coder config with Stage 1 (bare section
//     labels) + Stage 2 (Mini bodies for already-loaded sections)
//     appended to the system prompt.
//  2. Calls the model with the modified config.
//  3. If Coder returned plain text, parses out the
//     SELECTED_SECTIONS line, applies the selection to the loaded
//     set, and returns the cleaned text. Tool-call responses pass
//     through unchanged.
//
// Pass `reg == nil` (or an empty registry) to disable the funnel —
// the call becomes a plain Coder turn identical to the pre-Phase-2
// behavior. The TUI's NewModel pre-allocates an empty
// `skills.NewLoadedSet()` so `loaded` is always non-nil at the
// call sites; the loop, which has its own loaded set, threads a
// separate one in when it constructs Coder turns.
//
// `recentTask` is the most recent You message — included in the
// [Skills] system entry ApplySelection writes so the
// observability layer can correlate skill choices with the task
// that triggered them. Pass "" if no human message exists yet
// (e.g. crash-resume before any You entry).
func cmdCoderTurn(
	tr *transcript.Transcript,
	coder agent.AgentConfig,
	client loop.AgentClient,
	reg *skills.Registry,
	loaded *skills.LoadedSet,
	recentTask string,
) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		for attempt := 0; attempt < 3; attempt++ {
			turnCoder := coder
			turnCoder.SystemPrompt += skills.BuildCoderSystemPromptExtension(reg, loaded)
			if loaded != nil && loaded.SelectionRequired() {
				turnCoder.HasTools = false
				turnCoder.Tools = nil
			}
			resp, err := client.Respond(ctx, turnCoder, tr.Entries())
			if err != nil {
				return agentResponseMsg{speaker: transcript.SpeakerCoder, resp: resp, err: err}
			}
			if reg == nil || reg.Count() == 0 {
				return agentResponseMsg{speaker: transcript.SpeakerCoder, resp: resp}
			}
			if len(resp.ToolCalls) == 0 {
				cleaned, _ := skills.ParseAndApply(resp.Text, reg, loaded, tr, recentTask)
				resp.Text = cleaned
			}
			if !loaded.SelectionRequired() {
				return agentResponseMsg{speaker: transcript.SpeakerCoder, resp: resp}
			}
			if len(resp.ToolCalls) > 0 || strings.TrimSpace(resp.Text) != "" {
				continue
			}
		}
		return agentResponseMsg{speaker: transcript.SpeakerCoder, err: fmt.Errorf("coder did not complete mandatory skill selection")}
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
// Standard tools (write_file, read_file, run_command) are retried on transient
// failures via the shared retry mechanism.
func cmdExecuteTool(workDir string, toolCall agent.ToolCall, commandTimeout time.Duration) tea.Cmd {
	return func() tea.Msg {
		res, err := agent.ExecuteTool(workDir, toolCall, commandTimeout, &agent.RetryOptions{
			MaxAttempts: agent.RetryMaxAttempts,
			BaseDelay:   agent.RetryBaseDelay,
		})
		return toolResultMsg{
			toolCall: toolCall,
			result:   res,
			err:      err,
		}
	}
}

// cmdExecuteBrowserTool runs an approved browser_* tool call
// against the shared browser.Manager (docs/work2.md §4.2).
// Like cmdExecuteTool and cmdSpawnSubagent, it returns a
// toolResultMsg so the existing toolResultMsg handler in
// update.go picks it up unchanged — no TUI-side special case
// for the result.
//
// The manager is shared across all browser_* tool calls within a
// session (matching a real human's single browser tab), so
// navigate/click/type/get_text calls in sequence all see the
// same page state. The manager's own mutex serialises them.
//
// Browser tools are retried on transient failures (navigation timeouts,
// page crashes, connection resets) via the shared retry mechanism.
func cmdExecuteBrowserTool(workDir string, bm *browser.Manager, toolCall agent.ToolCall) tea.Cmd {
	return func() tea.Msg {
		if bm == nil {
			return toolResultMsg{
				toolCall: toolCall,
				result:   fmt.Sprintf("ERROR: %s approved but no browser.Manager is configured on the TUI", toolCall.Function.Name),
				err:      fmt.Errorf("browser tool %q approved but no browser.Manager is configured", toolCall.Function.Name),
			}
		}
		res, err := agent.ExecuteWithRetry(agent.RetryOptions{
			MaxAttempts: agent.RetryMaxAttempts,
			BaseDelay:   agent.RetryBaseDelay,
		}, func() (string, error) {
			return bm.ExecuteTool(workDir, toolCall.Function.Name, toolCall.Function.Arguments)
		})
		return toolResultMsg{
			toolCall: toolCall,
			result:   res,
			err:      err,
		}
	}
}

// cmdExecuteWebSearch executes an approved web_search tool call asynchronously.
// Web search is retried on transient failures (network errors, API timeouts)
// via the shared retry mechanism.
func cmdExecuteWebSearch(apiKey string, toolCall agent.ToolCall) tea.Cmd {
	return func() tea.Msg {
		var args agent.ExecuteToolArgs
		if toolCall.Function.Arguments != "" && toolCall.Function.Arguments != "{}" {
			_ = json.Unmarshal([]byte(toolCall.Function.Arguments), &args)
		}
		res, err := agent.ExecuteWithRetry(agent.RetryOptions{
			MaxAttempts: agent.RetryMaxAttempts,
			BaseDelay:   agent.RetryBaseDelay,
		}, func() (string, error) {
			return agent.ExecuteWebSearch(args.Query, apiKey)
		})
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
// APIKey / Model from it. client is the shared agent client.
// skillsReg is the parent session's skills registry — propagated
// to the subagent so its Coder turns go through the same Stage-1
// / Stage-2 funnel as the parent (work.md §3: coding subagents
// spawned under Orchestrator mode receive skill content). The
// subagent's own system prompt, tool set, and depth guard all live in
// the subagent package.
func cmdSpawnSubagent(
	sessionFilePath, workDir string,
	coder agent.AgentConfig,
	client loop.AgentClient,
	commandTimeout time.Duration,
	toolCall agent.ToolCall,
	skillsReg *skills.Registry,
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
		// Propagate the parent session's skills registry so the
		// subagent's Coder turns go through the same Stage-1 /
		// Stage-2 funnel. The subagent gets a per-run loaded set
		// (independent of the parent's), so a subagent's first
		// selection of any section fires Main regardless of
		// whether the parent already loaded it.
		runner.SetSkillsRegistry(skillsReg)

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
