// Package subagent implements short-lived, isolated-context subagents that
// Coder can spawn via the spawn_subagent tool (docs/work2.md §3).
//
// Design summary (the full design rationale lives in docs/work2.md §3):
//
//   - Each subagent gets its own JSONL transcript at
//     <sessionDir>/subagents/<id>.jsonl. The parent loop never sees those
//     intermediate entries; the only thing the subagent hands back to the
//     parent is a final summary string (per §3.2.4 / §3.2.5).
//   - The subagent is configured with a NARROWER tool set than the parent
//     Coder: read_file and run_command only. write_file is intentionally
//     omitted so the subagent can't bypass the parent's propose/review/
//     execute loop for the actual risky work of the task. spawn_subagent
//     itself is also omitted so a subagent can't spawn further subagents
//     — the depth guard (§3.2.6) is a hard cap of 1, enforced both
//     structurally (no spawn_subagent in the subagent's tool list) and
//     with a runtime check in NewRunner.
//   - The subagent's run_command calls do get executed against the same
//     workDir, and any files they touch are committed via the existing
//     gitcommit package. The commit message is attributed to the subagent
//     (ProposedBy: "Subagent:<id>") so git log stays honest about what
//     wrote what.
//   - The subagent's API call goes through the same *agent.Client the
//     parent uses. Same retry / rate-limit / HTTP plumbing — we just
//     hand it a different agent config (with the narrower tools and a
//     subagent-specific system prompt) and a fresh transcript.
//
// The Runner is a single struct with a Run method. Tests construct one
// with a mock client (any agent.AgentClient) and assert on the returned
// Result.
package subagent

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
	"github.com/kaiizer777/triad/internal/skills"
	"github.com/kaiizer777/triad/internal/transcript"
)

// ---------------------------------------------------------------------------
// Speaker helpers — used in the subagent's own transcript and in summary
// entries that get bubbled back to the parent.
// ---------------------------------------------------------------------------

// SpeakerLabel returns the speaker string used in transcript entries for
// the subagent itself (its own per-run transcript). Format: "Subagent:<id>".
// The main transcript can use this same label for the action_result entry
// that captures the subagent's final summary, so Reviewer can see that a
// delegation happened and what it concluded.
func SpeakerLabel(id string) string {
	return transcript.SpeakerSubagent + ":" + id
}

// ---------------------------------------------------------------------------
// Configuration constants
// ---------------------------------------------------------------------------

// DefaultMaxTurns caps how many agent turns a subagent can take before the
// runner gives up and returns a synthetic summary. Free-tier rate limits
// (docs/work2.md §7) make unbounded subagent execution a real risk; this
// concrete number keeps it bounded. Empirically 8 turns is plenty for
// "read 3-4 files, run the tests, summarise" tasks.
const DefaultMaxTurns = 8

// MaxAllowedDepth is the maximum subagent depth supported by this package.
// Per docs/work2.md §3.2.6, v1 of the feature hard-caps depth at 1 — a
// subagent cannot itself spawn a subagent. NewRunner refuses depth > 1
// as a hard error so the limit is impossible to violate.
const MaxAllowedDepth = 1

// SummaryPrefix is the marker the subagent's system prompt tells it to
// emit at the start of its final summary message. The Runner scans the
// last plain-text response for this prefix to detect "I'm done, here is
// the summary." Anything before this prefix in the final message is
// trimmed off before the summary is returned to the parent.
const SummaryPrefix = "SUMMARY:"

// summaryScanLines is how many trailing lines of the last plain-text
// message we scan when looking for the summary prefix. We keep it small
// because a runaway subagent that emits huge final messages would
// otherwise let one prefix on line 500 slip past us — but realistically
// the summary should always begin the message, so 5 lines is plenty.
const summaryScanLines = 5

// ---------------------------------------------------------------------------
// Result
// ---------------------------------------------------------------------------

// Result is what a finished subagent returns to the parent loop / TUI.
// Only Summary, Truncated, and Turns are surfaced to the human in the
// main transcript; TranscriptPath is recorded for debugging and
// post-mortem (the developer can `cat` the subagent's JSONL to see the
// full exploration).
type Result struct {
	// Summary is the subagent's final summary, with the SummaryPrefix
	// marker stripped. This is what the parent loop hands back to the
	// main transcript as the action_result content attributed to the
	// subagent (docs/work2.md §3.2.5).
	Summary string

	// TranscriptPath is the path to the subagent's isolated JSONL
	// transcript. Useful for debugging / post-mortem. Not surfaced
	// in the main transcript (kept in the runner's log line).
	TranscriptPath string

	// Turns is how many subagent turns (model API calls) were consumed.
	// Capped by Runner.maxTurns.
	Turns int

	// Truncated is true if the subagent hit the turn cap before emitting
	// a Summary-prefixed final message. The Summary field in that case
	// is a synthetic "truncated, partial findings:" message, not a real
	// subagent summary.
	Truncated bool
}

// ---------------------------------------------------------------------------
// Runner
// ---------------------------------------------------------------------------

// Client is the subset of *agent.Client the Runner actually uses. Defined
// as an interface so tests can inject a mock without standing up an HTTP
// server — same pattern the headless loop uses (loop.AgentClient).
type Client interface {
	Respond(ctx context.Context, cfg agent.AgentConfig, entries []transcript.Entry) (agent.AgentResponse, error)
}

// Runner drives one subagent to completion. Construct one per spawn via
// NewRunner; the struct is not safe for concurrent use.
type Runner struct {
	client         Client
	workDir        string
	sessionDir     string
	commandTimeout time.Duration
	maxTurns       int
	depth          int

	// skillsRegistry is the loaded skills directory for the parent
	// session, propagated to the subagent so its Coder turns go
	// through the same Stage-1 / Stage-2 funnel as the parent.
	// Nil (or empty) means the subagent's Coder turns see the
	// unmodified SubagentSystemPrompt — same behavior as the parent
	// loop / TUI when no skills are configured.
	//
	// work.md §3: coding subagents spawned under Orchestrator mode
	// receive skill content (not the parent Coder / Reviewer-only
	// exception). The subagent's loaded set is per-run (independent
	// of the parent's), so a subagent's first selection of a
	// section injects Main regardless of whether the parent has
	// already loaded it.
	skillsRegistry *skills.Registry
	loadedSkills   *skills.LoadedSet
}

// NewRunner constructs a subagent runner. depth is the current nesting
// level — 0 for a top-level call, 1+ for nested. NewRunner refuses any
// depth > MaxAllowedDepth (1) so the recursion guard in docs/work2.md
// §3.2.6 is enforced at construction time, not just at runtime.
//
// sessionDir is the directory under which the subagent's per-run
// transcript lives — typically the parent session directory, so the
// subagent's JSONL lands at <sessionDir>/subagents/<id>.jsonl. The
// directory is created on first write if it doesn't exist yet.
//
// commandTimeout caps the subagent's run_command executions; pass 0
// to use the agent package's default. maxTurns caps how many model
// calls the subagent can make; pass 0 to use DefaultMaxTurns.
func NewRunner(client Client, workDir, sessionDir string, commandTimeout time.Duration, maxTurns, depth int) (*Runner, error) {
	if client == nil {
		return nil, fmt.Errorf("subagent: client must not be nil")
	}
	if workDir == "" {
		return nil, fmt.Errorf("subagent: workDir must not be empty")
	}
	if sessionDir == "" {
		return nil, fmt.Errorf("subagent: sessionDir must not be empty")
	}
	if depth > MaxAllowedDepth {
		return nil, fmt.Errorf("subagent: depth %d exceeds MaxAllowedDepth %d (recursion guard, docs/work2.md §3.2.6)", depth, MaxAllowedDepth)
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
		depth:          depth,
		loadedSkills:   skills.NewLoadedSet(),
	}, nil
}

// SetSkillsRegistry attaches a loaded skills.Registry to the runner
// so the subagent's Coder turns go through the same Stage-1 /
// Stage-2 funnel as the parent. Pass nil to disable the funnel
// (the subagent then sees the unmodified SubagentSystemPrompt).
//
// The loaded set is per-run — created lazily in NewRunner — so a
// subagent's first selection of any section always injects Main,
// independent of whether the parent Coder has already loaded that
// section. This is deliberate: the subagent is a fresh context
// with no prior knowledge, and a subagent that ignores the
// project's Mini pointers won't be much use.
func (r *Runner) SetSkillsRegistry(reg *skills.Registry) {
	r.skillsRegistry = reg
	if r.loadedSkills == nil {
		r.loadedSkills = skills.NewLoadedSet()
	}
}

// SpeakerName returns the speaker label used for this subagent's entries
// in the subagent's own transcript and in the summary entry written to
// the main transcript by the caller. Format: "Subagent:<id>".
func (r *Runner) SpeakerName(id string) string {
	return SpeakerLabel(id)
}

// Run executes the subagent's task to completion (or to the turn cap) and
// returns the Result. The subagent's transcript is written to
// <sessionDir>/subagents/<id>.jsonl as a side effect; the path is
// returned in Result.TranscriptPath so the caller can reference it.
//
// task is the focused description of what the subagent should do.
// extraContext is optional bounded context (code excerpts, file paths)
// the parent hands in. parent is the parent Coder's agent config — the
// subagent inherits BaseURL / APIKey / Model from it, but gets its own
// system prompt and narrower tool set.
//
// On error (context cancelled, model call failure, malformed input),
// the partial Result is returned with whatever turns it managed plus
// a non-nil error so the caller can surface a meaningful failure to
// the main transcript.
func (r *Runner) Run(ctx context.Context, id, task, extraContext string, parent agent.AgentConfig) (Result, error) {
	if id == "" {
		return Result{}, fmt.Errorf("subagent: id must not be empty")
	}
	if strings.TrimSpace(task) == "" {
		return Result{}, fmt.Errorf("subagent: task must not be empty")
	}
	if parent.BaseURL == "" || parent.Model == "" {
		return Result{}, fmt.Errorf("subagent: parent config must have BaseURL and Model set")
	}

	transcriptPath := filepath.Join(r.sessionDir, "subagents", id+".jsonl")
	tr := transcript.NewTranscript(transcriptPath)

	// Seed the subagent's transcript with the task + context as a single
	// "You" entry. Using a single entry (not two) keeps the message
	// short and unambiguous on the first model turn.
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
		return Result{}, fmt.Errorf("subagent: failed to seed transcript: %w", err)
	}

	// Subagent config inherits the parent's transport / model but gets
	// its own system prompt AND its own narrower tool list. The tool
	// list is the subagent-specific schema (read_file + run_command
	// only) — see SubagentTools(). This is the structural half of the
	// recursion guard: the subagent's model literally cannot call
	// write_file or spawn_subagent because they aren't in the schema
	// sent to it.
	cfg := agent.AgentConfig{
		Name:         SpeakerLabel(id),
		BaseURL:      parent.BaseURL,
		APIKey:       parent.APIKey,
		Model:        parent.Model,
		HasTools:     true,
		SystemPrompt: SubagentSystemPrompt,
		Tools:        SubagentTools(),
	}

	logger.L().Info("subagent starting",
		"id", id,
		"transcript", transcriptPath,
		"max_turns", r.maxTurns,
		"depth", r.depth,
	)

	res := Result{TranscriptPath: transcriptPath}
	if r.skillsRegistry != nil && r.skillsRegistry.Count() > 0 {
		r.loadedSkills.BeginTask()
	}

	// Inner turn loop. Capped by r.maxTurns. We break out of the loop
	// when the subagent emits a SUMMARY:-prefixed plain-text message.
	for turn := 1; turn <= r.maxTurns; turn++ {
		res.Turns = turn

		if err := ctx.Err(); err != nil {
			return res, fmt.Errorf("subagent: context cancelled on turn %d: %w", turn, err)
		}

		// Call the subagent's model. Apply the Stage-1 / Stage-2
		// skills funnel on every turn: build a per-turn cfg with
		// the extension appended (so the persistent cfg stays
		// clean and we don't accumulate Mini bodies across turns),
		// then call Respond with that modified cfg. After the
		// response, if it was plain text, parse the
		// SELECTED_SECTIONS line, apply the selection to the
		// subagent's per-run loaded set, and log the system
		// entry. The [Skills] system entry lands in the
		// SUBAGENT's transcript (not the parent's), so the
		// parent's observability sees the final summary only —
		// matches work.md §3's "subagent is opaque" contract.
		turnCfg := cfg
		turnCfg.SystemPrompt = turnCfg.SystemPrompt + skills.BuildCoderSystemPromptExtension(r.skillsRegistry, r.loadedSkills)
		if r.loadedSkills != nil && r.loadedSkills.SelectionRequired() {
			turnCfg.HasTools = false
			turnCfg.Tools = nil
		}
		resp, err := r.client.Respond(ctx, turnCfg, tr.Entries())
		if err != nil {
			return res, fmt.Errorf("subagent: model call failed on turn %d: %w", turn, err)
		}

		// Post-call: parse a selection out of any plain-text
		// response. Tool-call responses have no plain text to
		// scan, so the parse is a no-op in that case.
		if len(resp.ToolCalls) == 0 && resp.Text != "" {
			cleaned, _ := skills.ParseAndApply(resp.Text, r.skillsRegistry, r.loadedSkills, tr, seed)
			resp.Text = cleaned
		}

		// Orchestrator-spawned coding subagents use the same mandatory
		// section → skill funnel as the primary Triad Coder. Do not let a
		// tool call or prose response bypass it.
		if r.skillsRegistry != nil && r.skillsRegistry.Count() > 0 && r.loadedSkills.SelectionRequired() {
			if len(resp.ToolCalls) > 0 || strings.TrimSpace(resp.Text) != "" {
				_ = tr.Append(transcript.Entry{Speaker: transcript.SpeakerSystem, Type: transcript.TypeMessage, Content: "Skill selection is required before work can begin.", Timestamp: time.Now()})
			}
			turn-- // selection turns do not consume the execution turn budget.
			continue
		}

		// Two cases: tool call(s) or plain text.
		if len(resp.ToolCalls) > 0 {
			// Process the FIRST tool call. A subagent that emits
			// multiple tool calls in one turn is being greedy — we
			// accept the first and append a system note to the
			// subagent's transcript so the model knows to slow down
			// next turn.
			tc := resp.ToolCalls[0]
			if len(resp.ToolCalls) > 1 {
				_ = tr.Append(transcript.Entry{
					Speaker:   transcript.SpeakerSystem,
					Type:      transcript.TypeMessage,
					Content:   fmt.Sprintf("Note: subagent runner only processes the first of %d tool calls in this turn; please call tools one at a time.", len(resp.ToolCalls)),
					Timestamp: time.Now(),
				})
			}
			if err := r.runOneToolCall(ctx, tr, id, tc); err != nil {
				return res, fmt.Errorf("subagent: tool execution failed on turn %d: %w", turn, err)
			}
			continue
		}

		// Plain text response. Append it and check for SUMMARY prefix.
		_ = tr.Append(transcript.Entry{
			Speaker:   SpeakerLabel(id),
			Type:      transcript.TypeMessage,
			Content:   resp.Text,
			Timestamp: time.Now(),
		})

		if summary, ok := extractSummary(resp.Text); ok {
			res.Summary = summary
			logger.L().Info("subagent finished with summary",
				"id", id, "turns", turn, "summary_len", len(summary),
			)
			return res, nil
		}

		// No summary yet — the model is still exploring. If this was
		// the last allowed turn, fall through to the truncation
		// path below.
		if turn == r.maxTurns {
			break
		}

		// Otherwise, ask the model to keep going by appending a small
		// system nudge. This is gentler than just calling again and
		// hoping the model remembers it's a subagent with a budget.
		_ = tr.Append(transcript.Entry{
			Speaker:   transcript.SpeakerSystem,
			Type:      transcript.TypeMessage,
			Content:   fmt.Sprintf("Turn %d/%d. If you have enough to answer, end with a line starting with %q followed by your findings. Otherwise call another tool.", turn, r.maxTurns, SummaryPrefix),
			Timestamp: time.Now(),
		})
	}

	// Turn cap reached without a SUMMARY. Synthesize a partial result.
	res.Truncated = true
	res.Summary = synthesizeTruncationSummary(tr.Entries())
	logger.L().Warn("subagent hit turn cap without summary",
		"id", id, "turns", res.Turns, "max_turns", r.maxTurns,
	)
	return res, nil
}

// runOneToolCall dispatches a single tool call in the subagent context.
// The allowed tools are read_file and run_command; anything else is
// rejected with a system note written to the subagent's transcript so
// the model can correct on the next turn. (write_file is intentionally
// NOT allowed — the subagent must not modify the world through the
// parent's review loop; that's the parent's job.)
func (r *Runner) runOneToolCall(_ context.Context, tr *transcript.Transcript, id string, tc agent.ToolCall) error {
	// Record the proposed action in the subagent's transcript so the
	// transcript tells a complete story if the developer reads it back
	// for debugging. No review step here — the subagent has no Reviewer.
	// We format the proposed action with a local helper (not
	// loop.FormatProposedAction) to avoid an import cycle — the loop
	// package is one of the consumers of this subagent package.
	_ = tr.Append(transcript.Entry{
		Speaker:   SpeakerLabel(id),
		Type:      transcript.TypeProposedAction,
		Content:   formatProposedAction(tc),
		Timestamp: time.Now(),
	})

	switch tc.Function.Name {
	case "read_file":
		// Standard file read.
		var args agent.ExecuteToolArgs
		if err := decodeSubagentArgs(tc.Function.Arguments, &args); err != nil {
			r.appendSystemError(tr, tc, err)
			return nil
		}
		if strings.TrimSpace(args.Path) == "" {
			r.appendSystemError(tr, tc, fmt.Errorf("read_file: required argument 'path' is missing or empty"))
			return nil
		}
		result, err := agent.ExecuteReadFile(r.workDir, args.Path)
		r.appendToolResult(tr, tc, result, err)
		return nil

	case "run_command":
		// run_command IS allowed for the subagent (so it can run tests,
		// grep, etc.), but with the same ExecuteRunCommand guard the
		// parent uses for timeouts. If the command writes files, those
		// are committed separately below.
		var args agent.ExecuteToolArgs
		if err := decodeSubagentArgs(tc.Function.Arguments, &args); err != nil {
			r.appendSystemError(tr, tc, err)
			return nil
		}
		if strings.TrimSpace(args.Command) == "" {
			r.appendSystemError(tr, tc, fmt.Errorf("run_command: required argument 'command' is missing or empty"))
			return nil
		}
		result, err := agent.ExecuteRunCommand(r.workDir, args.Command, r.commandTimeout)
		r.appendToolResult(tr, tc, result, err)

		// If the command succeeded AND modified files, commit them now
		// (with a subagent-tagged message). This mirrors the parent
		// loop's auto-commit-on-every-edit behaviour but attributes the
		// commit to the subagent instead of the parent Coder. Skip if
		// not in a git repo (test environments).
		if err == nil && gitcommit.IsRepo(r.workDir) {
			r.commitSubagentChanges(tr, id, tc, result)
		}
		return nil

	case "write_file", "spawn_subagent", "task_complete":
		// write_file and spawn_subagent are intentionally not allowed
		// for subagents. The subagent must not bypass the parent's
		// propose/review/execute loop for the actual risky work, and
		// the recursion guard says subagents can't spawn subagents.
		// task_complete is also irrelevant — subagents don't drive
		// the parent's session lifecycle.
		r.appendSystemError(tr, tc, fmt.Errorf("subagent: tool %q is not allowed in subagent context (allowed: read_file, run_command)", tc.Function.Name))
		return nil

	default:
		r.appendSystemError(tr, tc, fmt.Errorf("subagent: unknown tool %q", tc.Function.Name))
		return nil
	}
}

// appendSystemError appends a System entry to the subagent's transcript
// describing why a tool call was rejected. The runner then loops back
// for the next turn — the subagent's model sees this feedback and can
// correct course.
func (r *Runner) appendSystemError(tr *transcript.Transcript, tc agent.ToolCall, err error) {
	_ = tr.Append(transcript.Entry{
		Speaker:   transcript.SpeakerSystem,
		Type:      transcript.TypeMessage,
		Content:   fmt.Sprintf("Tool %q rejected: %v", tc.Function.Name, err),
		Timestamp: time.Now(),
	})
}

// appendToolResult appends the result of a subagent-allowed tool call
// to the transcript as an action_result. On error, the result string
// is prefixed with "ERROR:" so the subagent's model can see the
// failure mode clearly.
func (r *Runner) appendToolResult(tr *transcript.Transcript, tc agent.ToolCall, result string, err error) {
	content := result
	if err != nil {
		content = "ERROR: " + err.Error()
	}
	_ = tr.Append(transcript.Entry{
		Speaker:   transcript.SpeakerSystem,
		Type:      transcript.TypeActionResult,
		Content:   content,
		Timestamp: time.Now(),
	})
}

// commitSubagentChanges commits any files the subagent's run_command
// touched, with a commit message that clearly attributes the change to
// the subagent. The ProposedBy field is set to "Subagent:<id>" so
// `git log` is clearly honest about which commits were driven by the
// parent Coder vs a delegated subagent.
func (r *Runner) commitSubagentChanges(tr *transcript.Transcript, id string, tc agent.ToolCall, _ string) {
	paths, err := gitcommit.ChangedPaths(r.workDir)
	if err != nil {
		_ = tr.Append(transcript.Entry{
			Speaker:   transcript.SpeakerSystem,
			Type:      transcript.TypeMessage,
			Content:   fmt.Sprintf("git status failed after subagent run_command: %v", err),
			Timestamp: time.Now(),
		})
		return
	}
	if len(paths) == 0 {
		return
	}
	msg := gitcommit.CommitMessage{
		// Use the action_result entry's ID if we can find it; falling
		// back to 0 is fine — the commit body still names the tool,
		// the subagent, and the session.
		EntryID:     lastActionResultID(tr),
		Intent:      "subagent run_command: " + firstJSONStringField(tc.Function.Arguments, "command"),
		ToolName:    "run_command",
		SessionPath: r.sessionDir,
		ProposedBy:  SpeakerLabel(id),
		ApprovedBy:  "(subagent — no Reviewer)",
	}
	if _, cerr := gitcommit.CommitAction(r.workDir, paths, msg); cerr != nil {
		if gitcommit.IsNotConfigured(cerr) {
			_ = tr.Append(transcript.Entry{
				Speaker:   transcript.SpeakerSystem,
				Type:      transcript.TypeMessage,
				Content:   "subagent auto-commit skipped: git user.name / user.email not configured.",
				Timestamp: time.Now(),
			})
			return
		}
		_ = tr.Append(transcript.Entry{
			Speaker:   transcript.SpeakerSystem,
			Type:      transcript.TypeMessage,
			Content:   fmt.Sprintf("subagent auto-commit failed: %v", cerr),
			Timestamp: time.Now(),
		})
	}
}

// lastActionResultID returns the ID of the most recent action_result
// entry in the subagent's transcript. Used to put a real transcript
// reference in the auto-commit message. Returns 0 if none exists.
func lastActionResultID(tr *transcript.Transcript) int {
	entries := tr.Entries()
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Type == transcript.TypeActionResult {
			return entries[i].ID
		}
	}
	return 0
}

// extractSummary scans the first few lines of text for the SUMMARY:
// prefix and returns the trimmed summary. Returns ok=false if the
// prefix isn't present (so the caller can keep going).
func extractSummary(text string) (string, bool) {
	scanCount := summaryScanLines
	if scanCount < 1 {
		scanCount = 1
	}
	lines := strings.SplitN(text, "\n", scanCount+1)
	upTo := len(lines)
	if upTo > scanCount {
		upTo = scanCount
	}
	for _, line := range lines[:upTo] {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, SummaryPrefix) {
			summary := strings.TrimSpace(strings.TrimPrefix(trimmed, SummaryPrefix))
			if summary == "" {
				// Empty SUMMARY: — treat as no summary so the runner
				// nudges the model to actually write something.
				return "", false
			}
			return summary, true
		}
	}
	return "", false
}

// synthesizeTruncationSummary builds a fallback summary for when the
// subagent hit the turn cap. It concatenates the last few subagent
// messages so the parent loop still gets SOMETHING to work with, but
// flags the result as truncated (in Result.Truncated) so the parent
// can decide whether to trust it or kick off another delegation.
func synthesizeTruncationSummary(entries []transcript.Entry) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[subagent hit %d-turn cap; partial findings — treat as low-confidence]\n\n", DefaultMaxTurns))

	const maxExcerpts = 4
	count := 0
	// Walk backwards, collecting the subagent's own message-type
	// entries. Skip tool calls, tool results, the seed "You" entry,
	// and the system nudges we appended — the parent only cares
	// about the model's reasoning.
	for i := len(entries) - 1; i >= 0 && count < maxExcerpts; i-- {
		e := entries[i]
		if !strings.HasPrefix(e.Speaker, transcript.SpeakerSubagent+":") {
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
		sb.WriteString("(no model messages recorded before the cap)\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

// decodeSubagentArgs decodes a tool call's raw JSON arguments into the
// shared agent.ExecuteToolArgs struct. Used by runOneToolCall for both
// read_file and run_command.
func decodeSubagentArgs(raw string, dst *agent.ExecuteToolArgs) error {
	if raw == "" || raw == "{}" {
		return nil
	}
	return json.Unmarshal([]byte(raw), dst)
}

// firstJSONStringField returns the value of a string field from a JSON
// object string, or "" if missing / malformed. Used to extract the
// run_command "command" string for the auto-commit message.
func firstJSONStringField(raw, field string) string {
	if raw == "" {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return ""
	}
	if v, ok := m[field].(string); ok {
		return v
	}
	return ""
}

// formatProposedAction renders a ToolCall into the same "Tool: X /
// Arguments: Y" format the headless loop and TUI use for the main
// transcript. Duplicated here (instead of importing loop) because the
// loop package is a consumer of this subagent package — importing it
// back would create a cycle. Keep the format in sync with
// loop.FormatProposedAction if that ever changes.
func formatProposedAction(tc agent.ToolCall) string {
	if tc.Function.Arguments == "" || tc.Function.Arguments == "{}" {
		return "Tool: " + tc.Function.Name + "\n(no arguments)"
	}
	return "Tool: " + tc.Function.Name + "\nArguments: " + tc.Function.Arguments
}
