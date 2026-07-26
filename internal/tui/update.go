package tui

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/kaiizer777/triad/internal/agent"
	"github.com/kaiizer777/triad/internal/browser"
	"github.com/kaiizer777/triad/internal/clarify"
	"github.com/kaiizer777/triad/internal/gitcommit"
	"github.com/kaiizer777/triad/internal/journey"
	"github.com/kaiizer777/triad/internal/loop"
	"github.com/kaiizer777/triad/internal/skills"
	"github.com/kaiizer777/triad/internal/tracelog"
	"github.com/kaiizer777/triad/internal/transcript"
)

// jsonUnmarshalRaw is a thin wrapper around encoding/json so the TUI
// internals can decode tool-call argument strings without each call
// site spelling out the import. Defined here rather than re-imported
// from internal/agent to keep the TUI package's import surface small.
func jsonUnmarshalRaw(data string, dst any) error {
	return json.Unmarshal([]byte(data), dst)
}

// MaxPlainTextTurns is the maximum number of consecutive plain-text (no tool call)
// Coder responses allowed before the TUI treats it as a stall and returns to idle.
// Set to 1: Coder gets one chance to follow up a planning message with a tool call.
// This prevents conversational inputs (e.g. "hi") from triggering multiple re-calls.
const MaxPlainTextTurns = 1

// Update handles incoming messages and updates the model state.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case spinner.TickMsg:
		var spinCmd tea.Cmd
		m.spinner, spinCmd = m.spinner.Update(msg)
		return m, spinCmd

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		bodyHeight := msg.Height - 1
		if bodyHeight < 1 {
			bodyHeight = 1
		}

		// Responsive sidebar width:
		// Hide sidebar on narrow terminals (< 75 cols)
		var sidebarWidth int
		if msg.Width < 75 {
			sidebarWidth = 0
		} else if msg.Width < 95 {
			sidebarWidth = 28
		} else if msg.Width < 120 {
			sidebarWidth = 32
		} else {
			sidebarWidth = 36
		}

		mainContainerWidth := msg.Width - sidebarWidth
		if mainContainerWidth < 10 {
			mainContainerWidth = 10
		}

		rightCardHorizFrame := m.styles.RightCardContainer.GetHorizontalFrameSize()
		rightCardVertFrame := m.styles.RightCardContainer.GetVerticalFrameSize()

		rightCardInnerWidth := max(1, mainContainerWidth-rightCardHorizFrame)
		rightCardInnerHeight := max(1, bodyHeight-rightCardVertFrame)

		// Input box width based on rightCardInnerWidth
		pillW := lipgloss.Width(m.styles.InputPill.Render(" ❯ YOU "))
		hintW := lipgloss.Width(m.styles.TitleKeycapKey.Render("Enter")) + lipgloss.Width(m.styles.TitleKeycapLabel.Render(" Submit"))
		inputContainerContentWidth := max(1, rightCardInnerWidth-m.styles.InputContainer.GetHorizontalFrameSize())
		inputWidth := max(10, inputContainerContentWidth-pillW-hintW-6)
		m.input.SetWidth(inputWidth)

		// Viewport inner dimensions using exact input bar height:
		inputBarHeight := lipgloss.Height(m.renderInputBar(rightCardInnerWidth))
		bottomRows := inputBarHeight
		if sidebarWidth == 0 {
			bottomRows += 1
		}
		vpWidth := max(1, rightCardInnerWidth-m.styles.ViewportContainer.GetHorizontalFrameSize())
		vpHeight := max(1, rightCardInnerHeight-bottomRows)

		if !m.ready {
			m.viewport = viewport.New(viewport.WithWidth(vpWidth), viewport.WithHeight(vpHeight))
			m.sysViewport = viewport.New(viewport.WithWidth(max(10, sidebarWidth-4)), viewport.WithHeight(8))
			m.journeyViewport = viewport.New(viewport.WithWidth(max(10, sidebarWidth-4)), viewport.WithHeight(10))
			m.ready = true
			m.reloadJourneyEntries()
		} else {
			m.viewport.SetWidth(vpWidth)
			m.viewport.SetHeight(vpHeight)
			m.sysViewport.SetWidth(max(10, sidebarWidth-4))
			m.journeyViewport.SetWidth(max(10, sidebarWidth-4))
		}
		m.viewport.SetContent(m.renderTranscript())
		m.viewport.GotoBottom()
		m.sysViewport.SetContent(m.renderSystemLogs())
		m.sysViewport.GotoBottom()

	case tea.KeyMsg:
		// Skill editor intercept: when the inline editor is
		// open (m.skillEditor != nil), ALL keystrokes go to
		// the editor until it closes. This is the same
		// single-focus shape the input box uses when it has
		// focus — we just route to a different component.
		// Ctrl-S saves, Esc/Ctrl-C cancel, anything else
		// falls through to the textarea.
		if m.skillEditor != nil {
			if keyMatchesCancel(msg) {
				m.skillEditor = nil
				m.pendingSkillAction = nil
				m.statusMessage = "Skill edit cancelled (changes discarded)."
				m.refreshViewport()
				return m, nil
			}
			if keyMatchesCtrlS(msg) {
				if _, err := saveSkillEditor(m.skillEditor); err != nil {
					m.statusMessage = fmt.Sprintf("Save failed: %v", err)
					return m, nil
				}
				saved := m.skillEditor
				m.skillEditor = nil
				m.pendingSkillAction = nil
				// Re-load the registry so the next Stage 1
				// scan reflects the new file content. This is
				// what Phase 3.7 wants: "add a new custom
				// skill via /skill add, confirm it immediately
				// shows up in the next session's Stage 1
				// section scan without any code change or
				// restart." Within the same session, the same
				// effect comes from re-loading here.
				m.reloadSkillsRegistry()
				_ = m.transcript.Append(transcript.Entry{
					Speaker:   transcript.SpeakerSystem,
					Type:      transcript.TypeMessage,
					Content:   fmt.Sprintf("[Skill] Saved %s.", saved.path),
					Timestamp: time.Now(),
				})
				m.statusMessage = fmt.Sprintf("Saved %s.", filepath.Base(saved.path))
				m.refreshViewport()
				return m, nil
			}
			var taCmd tea.Cmd
			m.skillEditor.textarea, taCmd = m.skillEditor.textarea.Update(msg)
			return m, taCmd
		}
		// Picker intercept: when /models or /provider is open, route
		// all keystrokes to the picker. The skill editor above takes
		// priority; the picker takes priority over the text input.
		if m.picker != nil {
			if msg.String() == "ctrl+c" {
				return m, tea.Quit
			}
			_, cmd := m.pickerKey(msg)
			return m, cmd
		}
		if m.autocompleteActive && len(m.autocompleteCmds) > 0 {
			switch msg.String() {
			case "ctrl+c":
				return m, tea.Quit
			case "esc":
				m.autocompleteActive = false
				m.dismissedInput = m.input.Value()
				return m, nil
			case "up":
				m.autocompleteIndex--
				if m.autocompleteIndex < 0 {
					m.autocompleteIndex = len(m.autocompleteCmds) - 1
				}
				return m, nil
			case "down":
				m.autocompleteIndex++
				if m.autocompleteIndex >= len(m.autocompleteCmds) {
					m.autocompleteIndex = 0
				}
				return m, nil
			case "tab", "enter":
				if m.autocompleteIndex >= 0 && m.autocompleteIndex < len(m.autocompleteCmds) {
					selected := m.autocompleteCmds[m.autocompleteIndex]
					newVal := "/" + selected.Name
					if !strings.Contains(selected.Name, " ") && selected.Name != "help" && selected.Name != "status" && selected.Name != "summary" && selected.Name != "undo" && selected.Name != "trace" && selected.Name != "learn" && selected.Name != "journey" {
						newVal += " "
					}
					m.input.SetValue(newVal)
					m.input.CursorEnd()
					if selected.Name == "mode" {
						m.dismissedInput = ""
						m.syncAutocompleteState()
					} else {
						m.autocompleteActive = false
						m.dismissedInput = newVal
					}
					return m, nil
				}
			}
		}

		switch msg.String() {
		case "ctrl+c", "esc":
			return m, tea.Quit
		case "enter":
			if m.input.Focused() {
				val := strings.TrimSpace(m.input.Value())
				m.input.SetValue("")
				m.autocompleteActive = false
				m.dismissedInput = ""
				if val != "" {
					return m, func() tea.Msg { return humanInputMsg{content: val} }
				}
			}
		}

	case humanInputMsg:
		// Skill delete confirmation gate: if the previous
		// `/skill delete <name>` set a pendingSkillAction of
		// kind skillActionDelete, this human input IS the
		// confirmation reply — not a new task. We consume it
		// here, run or cancel the delete accordingly, and
		// never let it reach the slash-command or Coder path.
		if m.pendingSkillAction != nil && m.pendingSkillAction.Kind == skillActionDelete {
			reply := strings.TrimSpace(strings.ToLower(msg.content))
			targetName := m.pendingSkillAction.Name
			// Clear the gate before we recurse into the
			// handler, so a nested slash command in the
			// reply (e.g. `/status`) doesn't see a stale
			// pending action.
			m.pendingSkillAction = nil
			if reply == "yes" || reply == "y" {
				// Run the deferred delete via the
				// skills-package ExecutePending so the
				// actual file removal lives in one place
				// (cmd.go) and is testable in isolation.
				res := skills.ExecutePending(
					&skills.PendingAction{
						Kind: skills.PendingActionDelete,
						Name: targetName,
					},
					m.skillsRegistry,
					m.workDir,
				)
				if res.Reload {
					m.reloadSkillsRegistry()
				}
				_ = m.transcript.Append(transcript.Entry{
					Speaker:   transcript.SpeakerSystem,
					Type:      transcript.TypeMessage,
					Content:   res.Body,
					Timestamp: time.Now(),
				})
				m.statusMessage = fmt.Sprintf("Skill %q deleted.", targetName)
			} else {
				_ = m.transcript.Append(transcript.Entry{
					Speaker:   transcript.SpeakerSystem,
					Type:      transcript.TypeMessage,
					Content:   fmt.Sprintf("[Skill] Delete of %q cancelled.", targetName),
					Timestamp: time.Now(),
				})
				m.statusMessage = "Delete cancelled."
			}
			m.refreshViewport()
			return m, nil
		}

		// Phase 3 (clarify): handle the /proceed slash command as a
		// first-class signal BEFORE the slash-command lookup, so the
		// user can say "/proceed" or "proceed" interchangeably to
		// unblock a pending clarification round. We do not register
		// /proceed in the slash-command registry because the registry
		// is markdown-template based (not appropriate for a runtime
		// state change) and we want this to work in TUI mode only.
		trimmed := strings.TrimSpace(msg.content)
		if strings.HasPrefix(trimmed, "/proceed") {
			// Treat as a regular "proceed" text message — the
			// clarify-reply branch below will pick it up.
			msg.content = "proceed"
		}

		// Slash command detection: if the input begins with "/", try to look
		// it up as a registered command before treating it as a plain message.
		// Per docs/work2.md §1.2.5: only the leading "/" matters — the rest
		// of the input becomes the command's arguments.
		expanded, cmdHandled, systemHandled, errMsg := m.expandSlashCommand(msg.content)
		if errMsg != "" {
			// Unknown or invalid command — surface a system error entry but
			// don't inject the raw "/foo" into the transcript as a You
			// message (that would be misleading: the agent didn't actually
			// receive a coherent instruction).
			errEntry := transcript.Entry{
				Speaker:   transcript.SpeakerSystem,
				Type:      transcript.TypeMessage,
				Content:   errMsg,
				Timestamp: time.Now(),
			}
			_ = m.transcript.Append(errEntry)
			m.statusMessage = "Command not recognized."
			m.refreshViewport()
			return m, nil
		}
		if systemHandled {
			// System-target command (e.g. /status). The helper already wrote
			// the appropriate System entry to the transcript; do not also
			// inject it as a You message and do not trigger a Coder turn.
			// If the system command asked the TUI to launch the
			// /models or /provider picker, fire that now.
			if m.pendingPickerLaunch != nil {
				launch := m.pendingPickerLaunch
				m.pendingPickerLaunch = nil
				m.refreshViewport()
				var pickerCmd tea.Cmd
				switch launch.Kind {
				case pickerLaunchModels:
					pickerCmd = m.startModelPicker(false)
				case pickerLaunchProvider:
					pickerCmd = m.startProviderPicker(launch.ProviderName)
				}
				return m, pickerCmd
			}
			m.refreshViewport()
			return m, nil
		}
		if cmdHandled {
			// Use the expanded command text as the You message.
			msg.content = expanded
		}

		entry := transcript.Entry{
			Speaker:   transcript.SpeakerYou,
			Type:      transcript.TypeMessage,
			Content:   msg.content,
			Timestamp: time.Now(),
		}
		_ = m.transcript.Append(entry)
		m.refreshViewport()

		// --- Clarify phase (Phase 3, docs/x.md §Phase 3) ---
		//
		// Same shared step as the headless loop. If we have a
		// pending clarify round, this submission is a REPLY
		// (answers or a proceed signal). Treat the proceed case
		// explicitly so the System note records the best-guess
		// interpretation; otherwise the reply is just acked and
		// we fall through to the active cycle.
		//
		// Otherwise (no pending round), this is a fresh task.
		// Assess it. If ambiguous, append a single batched
		// System entry, set pendingClarify, and return to idle
		// without firing a Coder turn — the same shape the
		// headless loop uses.
		//
		// We skip the clarify step entirely when the input was a
		// slash command that got expanded (cmdHandled): the human
		// already committed to a deliberate workflow by typing the
		// command, so a "are you sure you meant X?" interruption
		// would be obnoxious. The original slash command was
		// unambiguous by construction; the expanded text just
		// happens to contain trigger keywords.
		if !cmdHandled && m.pendingClarify != nil {
			if clarify.IsProceedCommand(msg.content) {
				_ = m.transcript.Append(transcript.Entry{
					Speaker:   transcript.SpeakerSystem,
					Type:      transcript.TypeMessage,
					Content:   clarify.FormatProceedNote(*m.pendingClarify),
					Timestamp: time.Now(),
				})
				m.pendingClarify = nil
				m.refreshViewport()
				// Fall through to start the active cycle below.
			} else {
				_ = m.transcript.Append(transcript.Entry{
					Speaker:   transcript.SpeakerSystem,
					Type:      transcript.TypeMessage,
					Content:   "[System]: Clarification received — proceeding.",
					Timestamp: time.Now(),
				})
				m.pendingClarify = nil
				m.refreshViewport()
				// Fall through to start the active cycle below.
			}
		} else if !cmdHandled {
			batch := clarify.AssessAmbiguity(msg.content)
			if batch.NeedsClarification {
				tracePath := tracelog.TracePathForSession(m.transcript.FilePath())
				_ = tracelog.Append(tracePath, tracelog.Entry{
					Entity:      "clarify",
					EventType:   tracelog.EventClarifyTrigger,
					Description: fmt.Sprintf("Clarification requested (%d question(s)) for task: %s", len(batch.Questions), msg.content),
				})
				_ = m.transcript.Append(transcript.Entry{
					Speaker:   transcript.SpeakerSystem,
					Type:      transcript.TypeMessage,
					Content:   clarify.FormatClarifyBlock(batch),
					Timestamp: time.Now(),
				})
				stored := batch
				m.pendingClarify = &stored
				m.sessionState = loop.StateIdle
				m.statusMessage = "Clarifying questions pending. Answer them, or type /proceed to use defaults."
				m.refreshViewport()
				return m, nil
			}
			// Task is unambiguous — proceed to the active cycle
			// below without emitting any extra System note
			// (would be noisy on every clear task).
		}

		if m.sessionState == loop.StateIdle {
			m.sessionState = loop.StateActive
			if note := loop.CheckModeMismatch(m.currentMode, msg.content); note != "" {
				_ = m.transcript.Append(transcript.Entry{
					Speaker:   transcript.SpeakerSystem,
					Type:      transcript.TypeMessage,
					Content:   note,
					Timestamp: time.Now(),
				})
				m.refreshViewport()
			}
			m.statusMessage = "Coder is thinking..."
			return m, m.coderTurnCmd()
		}

		// Active cycle human interjection mid-flight.
		// Reset stale per-action state so the new human message starts a clean context:
		// the active tool call and retry count belong to the previous proposed action,
		// which is now superseded by the interjection.
		m.activeToolCall = nil
		m.retryCount = 0
		m.plainTextTurns = 0
		m.statusMessage = "Interjection appended. Next agent step will observe it."

	case agentResponseMsg:
		if msg.err != nil {
			errEntry := transcript.Entry{
				Speaker:   transcript.SpeakerSystem,
				Type:      transcript.TypeMessage,
				Content:   fmt.Sprintf("Error: %v", msg.err),
				Timestamp: time.Now(),
			}
			_ = m.transcript.Append(errEntry)
			m.sessionState = loop.StateIdle
			m.statusMessage = "Error encountered. Returned to idle."
			m.refreshViewport()
			return m, nil
		}

		// Record token usage metrics
		if msg.speaker == transcript.SpeakerCoder {
			m.stats.Coder.PromptTokens += msg.resp.Usage.PromptTokens
			m.stats.Coder.CompletionTokens += msg.resp.Usage.CompletionTokens
			m.stats.Coder.CachedTokens += msg.resp.Usage.GetCachedTokens()
		} else if msg.speaker == transcript.SpeakerReviewer {
			m.stats.Reviewer.PromptTokens += msg.resp.Usage.PromptTokens
			m.stats.Reviewer.CompletionTokens += msg.resp.Usage.CompletionTokens
			m.stats.Reviewer.CachedTokens += msg.resp.Usage.GetCachedTokens()
		}
		if msg.resp.Usage.PromptTokens > 0 {
			m.stats.LastPromptTokens = msg.resp.Usage.PromptTokens
		}
		if msg.resp.Usage.GetCachedTokens() > 0 {
			m.stats.HasCacheData = true
		}

		if msg.speaker == transcript.SpeakerCoder {
			if m.currentMode == loop.ModeGeneral {
				if len(msg.resp.ToolCalls) == 0 {
					entry := transcript.Entry{
						Speaker:   transcript.SpeakerCoder,
						Type:      transcript.TypeMessage,
						Content:   msg.resp.Text,
						Timestamp: time.Now(),
					}
					_ = m.transcript.Append(entry)
					m.sessionState = loop.StateIdle
					m.plainTextTurns = 0
					m.statusMessage = "Task complete (General Chat). Session idle."
					m.refreshViewport()
					return m, nil
				}

				toolCall := msg.resp.ToolCalls[0]
				if toolCall.Function.Name == "task_complete" {
					doneEntry := transcript.Entry{
						Speaker:   transcript.SpeakerSystem,
						Type:      transcript.TypeMessage,
						Content:   "Task complete. Session is now idle. Enter your next task.",
						Timestamp: time.Now(),
					}
					_ = m.transcript.Append(doneEntry)
					m.sessionState = loop.StateIdle
					m.activeToolCall = nil
					m.statusMessage = "Task complete. Session idle."
					m.refreshViewport()
					return m, nil
				}

				m.activeToolCall = &toolCall
				proposedContent := loop.FormatProposedAction(toolCall)
				proposedEntry := transcript.Entry{
					Speaker:   transcript.SpeakerCoder,
					Type:      transcript.TypeProposedAction,
					Content:   proposedContent,
					Timestamp: time.Now(),
				}
				_ = m.transcript.Append(proposedEntry)
				m.refreshViewport()

				if toolCall.Function.Name == "spawn_subagent" {
					m.statusMessage = "Running subagent..."
					return m, cmdSpawnSubagent(
						m.transcript.FilePath(),
						m.workDir,
						m.coder,
						m.client,
						m.commandTimeout,
						toolCall,
						m.skillsRegistry,
					)
				}
				if browser.IsBrowserTool(toolCall.Function.Name) {
					m.statusMessage = fmt.Sprintf("Executing browser tool %q...", toolCall.Function.Name)
					return m, cmdExecuteBrowserTool(m.workDir, m.browser, toolCall)
				}
				if toolCall.Function.Name == "web_search" {
					m.statusMessage = "Searching the web..."
					return m, cmdExecuteWebSearch(m.searchAPIKey, toolCall)
				}
				m.statusMessage = fmt.Sprintf("Executing tool %q...", toolCall.Function.Name)
				return m, cmdExecuteTool(m.workDir, toolCall, m.commandTimeout)
			}

			if len(msg.resp.ToolCalls) == 0 {
				// Plain text message / plan from Coder — no tool call yet.
				entry := transcript.Entry{
					Speaker:   transcript.SpeakerCoder,
					Type:      transcript.TypeMessage,
					Content:   msg.resp.Text,
					Timestamp: time.Now(),
				}
				_ = m.transcript.Append(entry)
				// Remember this reasoning so the auto-commit for the
				// next executed action can quote it as the "intent".
				m.lastCoderMessage = msg.resp.Text
				m.plainTextTurns++
				m.refreshViewport()

				// Guard against infinite plain-text loops: if Coder keeps sending
				// messages without ever producing a tool call, surface the stall.
				if m.plainTextTurns >= MaxPlainTextTurns {
					stallEntry := transcript.Entry{
						Speaker:   transcript.SpeakerSystem,
						Type:      transcript.TypeMessage,
						Content:   fmt.Sprintf("Coder sent %d consecutive plain-text messages without proposing an action. Session returned to idle — please clarify the task.", m.plainTextTurns),
						Timestamp: time.Now(),
					}
					_ = m.transcript.Append(stallEntry)
					m.sessionState = loop.StateIdle
					m.plainTextTurns = 0
					m.statusMessage = "Coder stalled (no action proposed). Session idle."
					m.refreshViewport()
					return m, nil
				}

				m.statusMessage = fmt.Sprintf("Coder sent plan/message (%d/%d). Awaiting action proposal...", m.plainTextTurns, MaxPlainTextTurns)
				return m, m.coderTurnCmd()
			}

			// Coder proposed an action — reset the plain-text stall counter.
			m.plainTextTurns = 0
			toolCall := msg.resp.ToolCalls[0]
			m.activeToolCall = &toolCall
			proposedContent := loop.FormatProposedAction(toolCall)
			proposedEntry := transcript.Entry{
				Speaker:   transcript.SpeakerCoder,
				Type:      transcript.TypeProposedAction,
				Content:   proposedContent,
				Timestamp: time.Now(),
			}
			_ = m.transcript.Append(proposedEntry)
			entries := m.transcript.Entries()
			if len(entries) > 0 {
				m.lastProposedEntryID = entries[len(entries)-1].ID
			}
			m.refreshViewport()

			// Orchestrator mode: the orchestrator agent itself doesn't
			// use a reviewer — it routes tasks to general or triad.
			// Execute directly, same as general mode.
			if m.currentMode == loop.ModeOrchestrator {
				if toolCall.Function.Name == "spawn_subagent" {
					m.statusMessage = "Running subagent..."
					return m, cmdSpawnSubagent(
						m.transcript.FilePath(),
						m.workDir,
						m.coder,
						m.client,
						m.commandTimeout,
						toolCall,
						m.skillsRegistry,
					)
				}
				if browser.IsBrowserTool(toolCall.Function.Name) {
					m.statusMessage = fmt.Sprintf("Executing browser tool %q...", toolCall.Function.Name)
					return m, cmdExecuteBrowserTool(m.workDir, m.browser, toolCall)
				}
				if toolCall.Function.Name == "web_search" {
					m.statusMessage = "Searching the web..."
					return m, cmdExecuteWebSearch(m.searchAPIKey, toolCall)
				}
				m.statusMessage = fmt.Sprintf("Executing tool %q...", toolCall.Function.Name)
				return m, cmdExecuteTool(m.workDir, toolCall, m.commandTimeout)
			}

			m.statusMessage = fmt.Sprintf("Reviewer inspecting proposed action %q...", toolCall.Function.Name)
			return m, cmdReviewerTurn(m.transcript, m.reviewer, m.client)
		}

		if msg.speaker == transcript.SpeakerReviewer {
			text := strings.TrimSpace(msg.resp.Text)
			reviewerEntry := transcript.Entry{
				Speaker:   transcript.SpeakerReviewer,
				Type:      transcript.TypeMessage,
				Content:   text,
				Timestamp: time.Now(),
			}
			_ = m.transcript.Append(reviewerEntry)
			m.refreshViewport()

			decision := loop.ParseReviewerDecision(text)
			switch decision {
			case loop.DecisionApprove:
				if m.activeToolCall != nil && m.activeToolCall.Function.Name == "task_complete" {
					// Task complete
					doneEntry := transcript.Entry{
						Speaker:   transcript.SpeakerSystem,
						Type:      transcript.TypeMessage,
						Content:   "Task complete. Session is now idle. Enter your next task.",
						Timestamp: time.Now(),
					}
					_ = m.transcript.Append(doneEntry)
					m.sessionState = loop.StateIdle
					m.activeToolCall = nil
					m.retryCount = 0
					m.statusMessage = "Task complete. Session idle."
					m.refreshViewport()
					return m, nil
				}

				if m.activeToolCall != nil {
					tc := *m.activeToolCall
					// spawn_subagent and browser_* are special-cased:
					// cmdExecuteTool doesn't know how to run a
					// subagent (no client / session dir / parent
					// config) or a browser tool (no Manager). The TUI
					// dispatches them via cmdSpawnSubagent and
					// cmdExecuteBrowserTool respectively, which run in
					// the background and emit a toolResultMsg the same
					// shape cmdExecuteTool would. All other tools
					// fall through to the normal executor.
					if tc.Function.Name == "spawn_subagent" {
						m.statusMessage = "Running subagent..."
						return m, cmdSpawnSubagent(
							m.transcript.FilePath(),
							m.workDir,
							m.coder,
							m.client,
							m.commandTimeout,
							tc,
							m.skillsRegistry,
						)
					}
					if browser.IsBrowserTool(tc.Function.Name) {
						m.statusMessage = fmt.Sprintf("Executing browser tool %q...", tc.Function.Name)
						return m, cmdExecuteBrowserTool(m.workDir, m.browser, tc)
					}
					if tc.Function.Name == "web_search" {
						m.statusMessage = "Searching the web..."
						return m, cmdExecuteWebSearch(m.searchAPIKey, tc)
					}
					m.statusMessage = fmt.Sprintf("Executing approved tool %q...", tc.Function.Name)
					return m, cmdExecuteTool(m.workDir, tc, m.commandTimeout)
				}

				m.sessionState = loop.StateIdle
				m.statusMessage = "Approved. Session idle."
				return m, nil

			case loop.DecisionObject:
				m.retryCount++
				if m.retryCount < m.MaxRetries {
					m.statusMessage = fmt.Sprintf("Reviewer objected (%d/%d). Coder revising...", m.retryCount, m.MaxRetries)
					return m, m.coderTurnCmd()
				}

				// Retry cap reached
				deadlockEntry := transcript.Entry{
					Speaker:   transcript.SpeakerSystem,
					Type:      transcript.TypeMessage,
					Content:   fmt.Sprintf("Approval deadlock: Coder and Reviewer could not agree after %d attempts. Human intervention required.", m.MaxRetries),
					Timestamp: time.Now(),
				}
				_ = m.transcript.Append(deadlockEntry)
				m.sessionState = loop.StateIdle
				m.activeToolCall = nil
				m.retryCount = 0
				m.statusMessage = "Approval deadlock. Session idle."
				m.refreshViewport()
				return m, nil

			default:
				// Reviewer returned an ambiguous response that ParseReviewerDecision
				// could not classify. Surface it as a system warning and ask Reviewer
				// again (counts against the retry cap to prevent infinite loops).
				m.retryCount++
				ambigEntry := transcript.Entry{
					Speaker:   transcript.SpeakerSystem,
					Type:      transcript.TypeMessage,
					Content:   fmt.Sprintf("Reviewer returned an ambiguous response (retry %d/%d). Asking Reviewer again...", m.retryCount, m.MaxRetries),
					Timestamp: time.Now(),
				}
				_ = m.transcript.Append(ambigEntry)
				m.refreshViewport()
				if m.retryCount < m.MaxRetries {
					m.statusMessage = fmt.Sprintf("Reviewer ambiguous (%d/%d). Retrying...", m.retryCount, m.MaxRetries)
					return m, cmdReviewerTurn(m.transcript, m.reviewer, m.client)
				}
				// Exhausted retries on ambiguous responses — surface and idle.
				exhaustedEntry := transcript.Entry{
					Speaker:   transcript.SpeakerSystem,
					Type:      transcript.TypeMessage,
					Content:   fmt.Sprintf("Reviewer failed to give a clear decision after %d attempts. Human intervention required.", m.MaxRetries),
					Timestamp: time.Now(),
				}
				_ = m.transcript.Append(exhaustedEntry)
				m.sessionState = loop.StateIdle
				m.activeToolCall = nil
				m.retryCount = 0
				m.statusMessage = "Reviewer unresponsive. Session idle."
				m.refreshViewport()
				return m, nil
			}
		}

	case toolResultMsg:
		resultContent := msg.result
		if msg.err != nil {
			resultContent = fmt.Sprintf("ERROR: %v", msg.err)
		}
		resultEntry := transcript.Entry{
			Speaker:   transcript.SpeakerSystem,
			Type:      transcript.TypeActionResult,
			Content:   resultContent,
			Timestamp: time.Now(),
		}
		_ = m.transcript.Append(resultEntry)
		var resultEntryID int
		entries := m.transcript.Entries()
		if len(entries) > 0 {
			resultEntryID = entries[len(entries)-1].ID
		}

		// Auto-commit on every executed edit (docs/work2.md §2.2).
		// Only acts on successful write_file / run_command; reads and
		// task_complete never touch the filesystem. Rejected proposals
		// never reach this code path, so they can't touch git either.
		var commitNote string
		if msg.err == nil && !m.gitDisabled {
			commitNote = m.maybeAutoCommit(msg.toolCall, resultEntryID)
			if commitNote == gitDisabledSentinel {
				m.gitDisabled = true
				commitNote = "Auto-commit disabled for this session: " +
					"git user.name / user.email not configured. " +
					"Transcript continues normally; changes are not auto-committed."
			}
		}
		if commitNote != "" {
			_ = m.transcript.Append(transcript.Entry{
				Speaker:   transcript.SpeakerSystem,
				Type:      transcript.TypeMessage,
				Content:   commitNote,
				Timestamp: time.Now(),
			})
		}

		m.activeToolCall = nil
		m.retryCount = 0
		m.lastCoderMessage = ""
		m.lastProposedEntryID = 0
		if m.currentMode == loop.ModeGeneral {
			m.sessionState = loop.StateIdle
			m.statusMessage = "Action executed (General Chat). Session idle."
			m.refreshViewport()
			return m, nil
		}
		m.statusMessage = "Action executed. Coder considering next step..."
		m.refreshViewport()
		return m, m.coderTurnCmd()

	case pickerModelsReadyMsg:
		// Async result from the /models or /provider picker.
		if m.picker == nil {
			return m, nil
		}
		if len(msg.Errs) > 0 && len(msg.Models) == 0 {
			// Total failure — close the picker and surface a
			// single System entry.
			m.picker = nil
			parts := make([]string, 0, len(msg.Errs))
			for _, e := range msg.Errs {
				parts = append(parts, fmt.Sprintf("%s: %s", e.Provider, e.Err))
			}
			_ = m.transcript.Append(transcript.Entry{
				Speaker:   transcript.SpeakerSystem,
				Type:      transcript.TypeMessage,
				Content:   "[Models] Could not load any model list: " + strings.Join(parts, "; "),
				Timestamp: time.Now(),
			})
			m.statusMessage = "Failed to load models."
			m.refreshViewport()
			return m, nil
		}
		m.picker.Models = msg.Models
		m.picker.ProviderErrs = msg.Errs
		// Seed the cursor at the current active provider + model
		// if present, otherwise the first row.
		if m.agentCfg != nil {
			for i, am := range msg.Models {
				if am.Provider == m.agentCfg.ActiveProvider && am.Info.ID == m.agentCfg.Model {
					m.picker.Index = i
					break
				}
			}
		}
		m.picker.Step = pickerStepModel
		// If the picker is a provider-only launch, pre-select
		// the user's current model in this provider if any.
		if m.picker.IsProviderOnly {
			if m.agentCfg != nil {
				for i, am := range msg.Models {
					if am.Info.ID == m.agentCfg.Model {
						m.picker.Index = i
						break
					}
				}
			}
		}
		if len(msg.Errs) > 0 {
			// Partial success — note which providers failed in
			// the status bar; the user can see the successful
			// models in the picker.
			parts := make([]string, 0, len(msg.Errs))
			for _, e := range msg.Errs {
				parts = append(parts, e.Provider)
			}
			m.statusMessage = fmt.Sprintf("Loaded %d model(s); %d provider(s) failed: %s",
				len(msg.Models), len(msg.Errs), strings.Join(parts, ", "))
		} else {
			m.statusMessage = fmt.Sprintf("Loaded %d model(s).", len(msg.Models))
		}
		m.refreshViewport()
		return m, nil

	case systemStatusMsg:
		// Generic System entry the picker writes on apply /
		// cancel / save failure.
		_ = m.transcript.Append(transcript.Entry{
			Speaker:   transcript.SpeakerSystem,
			Type:      transcript.TypeMessage,
			Content:   msg.Message,
			Timestamp: time.Now(),
		})
		m.statusMessage = msg.Message
		m.refreshViewport()
		return m, nil
	}
	// Update textinput component
	var inputCmd tea.Cmd
	m.input, inputCmd = m.input.Update(msg)
	cmds = append(cmds, inputCmd)

	m.syncAutocompleteState()

	// Update viewport components for scrolling keypresses
	var vpCmd, sysCmd, jCmd tea.Cmd
	m.viewport, vpCmd = m.viewport.Update(msg)
	m.sysViewport, sysCmd = m.sysViewport.Update(msg)
	m.journeyViewport, jCmd = m.journeyViewport.Update(msg)
	cmds = append(cmds, vpCmd, sysCmd, jCmd)

	return m, tea.Batch(cmds...)
}
// syncAutocompleteState evaluates the textinput value and updates live slash-command autocomplete state.
func (m *Model) syncAutocompleteState() {
	val := m.input.Value()
	if !strings.HasPrefix(val, "/") {
		m.autocompleteActive = false
		m.autocompleteCmds = nil
		m.autocompleteIndex = 0
		m.dismissedInput = ""
		return
	}

	if val == m.dismissedInput {
		return
	}
	m.dismissedInput = ""

	query := val[1:]
	matches := m.commands.Filter(query)
	if len(matches) == 0 {
		m.autocompleteActive = false
		m.autocompleteCmds = nil
		m.autocompleteIndex = 0
		return
	}

	m.autocompleteActive = true
	m.autocompleteCmds = matches
	if m.autocompleteIndex < 0 || m.autocompleteIndex >= len(matches) {
		m.autocompleteIndex = 0
	}
}

// refreshViewport updates the content in the viewport and auto-scrolls to the bottom.
func (m *Model) refreshViewport() {
	m.viewport.SetContent(m.renderTranscript())
	m.viewport.GotoBottom()
	m.sysViewport.SetContent(m.renderSystemLogs())
	m.sysViewport.GotoBottom()
	if m.showJourney {
		m.journeyViewport.SetContent(journey.RenderSidebarTimeline(m.journeyEntries, max(10, m.journeyViewport.Width())))
	}
}

// gitDisabledSentinel is the special return value from maybeAutoCommit that
// signals the caller to flip m.gitDisabled to true (so we don't keep trying
// to commit on every subsequent action when git is misconfigured). The
// caller rewrites the sentinel into a human-readable System entry.
const gitDisabledSentinel = "__GIT_DISABLED__"

// maybeAutoCommit attempts to create a single git commit for an executed
// action. Returns an empty string when nothing happened (e.g. read_file,
// task_complete, or write_file with identical content), a one-line note
// suitable for a System transcript entry on success, or gitDisabledSentinel
// when the failure is permanent (no user.name/email) and the caller should
// stop trying for the rest of the session.
//
// The caller is responsible for:
//   - only calling this for actions that may have touched files
//     (write_file, run_command)
//   - only calling this when execution succeeded (msg.err == nil)
//   - writing the returned note as a System transcript entry
func (m *Model) maybeAutoCommit(toolCall agent.ToolCall, resultEntryID int) string {
	switch toolCall.Function.Name {
	case "write_file", "run_command":
		// proceed
	default:
		return ""
	}

	// Determine the file path(s) the action touched.
	var paths []string
	switch toolCall.Function.Name {
	case "write_file":
		var args agent.ExecuteToolArgs
		if err := decodeToolArgs(toolCall.Function.Arguments, &args); err != nil || strings.TrimSpace(args.Path) == "" {
			return ""
		}
		paths = []string{gitcommit.NormalizePath(m.workDir, args.Path)}
	case "run_command":
		// Shell commands don't declare which files they touch up front.
		// Discover via `git status --porcelain` after the fact.
		found, err := gitcommit.ChangedPaths(m.workDir)
		if err != nil {
			// git status itself failed (rare — would mean the repo
			// disappeared mid-session). Surface the error but don't
			// disable further attempts, since the issue is transient.
			return fmt.Sprintf("git status failed: %v", err)
		}
		paths = found
	}

	if len(paths) == 0 {
		// No filesystem changes (e.g. `go version` command) — skip.
		return ""
	}

	// Build the commit message. Use Coder's last plain-text reasoning as
	// the intent if we have it; fall back to a short tool+path description.
	intent := strings.TrimSpace(m.lastCoderMessage)
	if intent == "" {
		// No planning text — synthesise a minimal intent from the action.
		if toolCall.Function.Name == "write_file" {
			intent = "write " + firstJSONField(toolCall.Function.Arguments, "path")
		} else {
			intent = "run: " + firstJSONField(toolCall.Function.Arguments, "command")
		}
	}
	// Cap the intent length for the commit subject; the body keeps the
	// full session path and proposed-by / approved-by attribution.
	msg := gitcommit.CommitMessage{
		EntryID:     resultEntryID,
		Intent:      intent,
		ToolName:    toolCall.Function.Name,
		SessionPath: m.transcript.FilePath(),
		ProposedBy:  transcript.SpeakerCoder,
		ApprovedBy:  transcript.SpeakerReviewer,
	}

	res, err := gitcommit.CommitAction(m.workDir, paths, msg)
	if err != nil {
		if gitcommit.IsNotConfigured(err) {
			// Permanent failure — caller flips m.gitDisabled so we
			// don't surface this on every action.
			return gitDisabledSentinel
		}
		// Transient git failure (e.g. lock contention, missing binary).
		// Surface the error but keep trying on the next action.
		return fmt.Sprintf("auto-commit failed: %v", err)
	}
	if res.NoChanges {
		// Identical content / no actual diff — skip silently.
		return ""
	}
	if res.Hash != "" {
		m.reloadJourneyEntries()
		return fmt.Sprintf("auto-commit %s: %s", res.Hash, intent)
	}
	return ""
}

// decodeToolArgs decodes a tool call's raw JSON arguments into the shared
// agent.ExecuteToolArgs struct. Returns an error if the JSON is malformed;
// missing required fields are checked by the caller, since they may be
// optional for some tools.
func decodeToolArgs(raw string, dst *agent.ExecuteToolArgs) error {
	if raw == "" {
		return nil
	}
	return jsonUnmarshalRaw(raw, dst)
}

// firstJSONField returns the value of the given string field from a JSON
// object string, or "" if the field is absent or the JSON is malformed.
// Used to synthesise a short commit intent when Coder didn't provide a
// plain-text planning message.
func firstJSONField(raw, field string) string {
	if raw == "" {
		return ""
	}
	var m map[string]any
	if err := jsonUnmarshalRaw(raw, &m); err != nil {
		return ""
	}
	if v, ok := m[field].(string); ok {
		return v
	}
	return ""
}
