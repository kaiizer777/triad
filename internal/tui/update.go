package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/kaiizer777/triad/internal/loop"
	"github.com/kaiizer777/triad/internal/transcript"
)

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

		// Fixed header & footer lines:
		// Header (1) + PipelineDock (1) + Status (1) + InputBar (3) = 6 lines
		availHeight := msg.Height - 6
		if availHeight < 1 {
			availHeight = 1
		}

		// Responsive sidebar width:
		// Hide sidebar on narrow terminals (< 75 cols)
		var sidebarWidth int
		if msg.Width < 75 {
			sidebarWidth = 0
		} else if msg.Width < 100 {
			sidebarWidth = 26
		} else {
			sidebarWidth = 30
		}

		mainContainerWidth := msg.Width - sidebarWidth
		if mainContainerWidth < 10 {
			mainContainerWidth = 10
		}

		// Viewport inner dimensions using exact style frame sizes
		vpWidth := max(1, mainContainerWidth-m.styles.ViewportContainer.GetHorizontalFrameSize())
		vpHeight := max(1, availHeight-m.styles.ViewportContainer.GetVerticalFrameSize())

		// Input box width:
		inputContainerContentWidth := max(1, msg.Width-m.styles.InputContainer.GetHorizontalFrameSize())
		inputWidth := inputContainerContentWidth - 30
		if inputWidth < 10 {
			inputWidth = 10
		}
		m.input.SetWidth(inputWidth)

		if !m.ready {
			m.viewport = viewport.New(viewport.WithWidth(vpWidth), viewport.WithHeight(vpHeight))
			m.ready = true
		} else {
			m.viewport.SetWidth(vpWidth)
			m.viewport.SetHeight(vpHeight)
		}
		m.viewport.SetContent(m.renderTranscript())
		m.viewport.GotoBottom()

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			return m, tea.Quit
		case "enter":
			if m.input.Focused() {
				val := strings.TrimSpace(m.input.Value())
				m.input.SetValue("")
				if val != "" {
					return m, func() tea.Msg { return humanInputMsg{content: val} }
				}
			}
		}

	case humanInputMsg:
		entry := transcript.Entry{
			Speaker:   transcript.SpeakerYou,
			Type:      transcript.TypeMessage,
			Content:   msg.content,
			Timestamp: time.Now(),
		}
		_ = m.transcript.Append(entry)
		m.refreshViewport()

		if m.sessionState == loop.StateIdle {
			m.sessionState = loop.StateActive
			m.statusMessage = "Coder is thinking..."
			return m, cmdCoderTurn(m.transcript, m.coder, m.client)
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

		if msg.speaker == transcript.SpeakerCoder {
			if len(msg.resp.ToolCalls) == 0 {
				// Plain text message / plan from Coder — no tool call yet.
				entry := transcript.Entry{
					Speaker:   transcript.SpeakerCoder,
					Type:      transcript.TypeMessage,
					Content:   msg.resp.Text,
					Timestamp: time.Now(),
				}
				_ = m.transcript.Append(entry)
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
				return m, cmdCoderTurn(m.transcript, m.coder, m.client)
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
			m.refreshViewport()
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
					return m, cmdCoderTurn(m.transcript, m.coder, m.client)
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
		m.activeToolCall = nil
		m.retryCount = 0
		m.statusMessage = "Action executed. Coder considering next step..."
		m.refreshViewport()
		return m, cmdCoderTurn(m.transcript, m.coder, m.client)
	}

	// Update textinput component
	var inputCmd tea.Cmd
	m.input, inputCmd = m.input.Update(msg)
	cmds = append(cmds, inputCmd)

	// Update viewport component for scrolling keypresses
	var vpCmd tea.Cmd
	m.viewport, vpCmd = m.viewport.Update(msg)
	cmds = append(cmds, vpCmd)

	return m, tea.Batch(cmds...)
}

// refreshViewport updates the content in the viewport and auto-scrolls to the bottom.
func (m *Model) refreshViewport() {
	m.viewport.SetContent(m.renderTranscript())
	m.viewport.GotoBottom()
}
