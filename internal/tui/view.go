package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/kaiizer777/triad/internal/transcript"
)

// View renders the complete TUI layout.
func (m Model) View() tea.View {
	if !m.ready {
		return tea.NewView("Initializing Triad session...")
	}

	header := m.styles.TitleBar.Render(" ⚡ TRIAD — Coder/Reviewer Shared Session ")
	viewportView := m.viewport.View()
	statusBar := m.styles.StatusBar.Render(" Status: " + m.statusMessage)
	inputView := m.input.View()

	content := lipgloss.JoinVertical(
		lipgloss.Left,
		header,
		viewportView,
		statusBar,
		inputView,
	)

	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

// renderTranscript formats all transcript entries with Lipgloss styles for the viewport.
func (m Model) renderTranscript() string {
	entries := m.transcript.Entries()
	if len(entries) == 0 {
		return m.styles.StatusBar.Render("Transcript empty. Enter a task below to start!")
	}

	var sb strings.Builder
	for i, entry := range entries {
		ts := entry.Timestamp.Format("15:04:05")

		var header string
		switch entry.Speaker {
		case transcript.SpeakerYou:
			header = m.styles.YouHeader.Render(fmt.Sprintf("[%s] 👤 You:", ts))
		case transcript.SpeakerCoder:
			if entry.Type == transcript.TypeProposedAction {
				header = m.styles.CoderHeader.Render(fmt.Sprintf("[%s] 🔧 Coder [proposed_action]:", ts))
			} else {
				header = m.styles.CoderHeader.Render(fmt.Sprintf("[%s] 🔧 Coder:", ts))
			}
		case transcript.SpeakerReviewer:
			header = m.styles.ReviewerHeader.Render(fmt.Sprintf("[%s] 🔍 Reviewer:", ts))
		case transcript.SpeakerSystem:
			if entry.Type == transcript.TypeActionResult {
				header = m.styles.SystemHeader.Render(fmt.Sprintf("[%s] ⚙  System [result]:", ts))
			} else {
				header = m.styles.SystemHeader.Render(fmt.Sprintf("[%s] ℹ  System:", ts))
			}
		default:
			header = m.styles.SystemHeader.Render(fmt.Sprintf("[%s] %s:", ts, entry.Speaker))
		}

		var body string
		switch entry.Type {
		case transcript.TypeProposedAction:
			// Clamp to terminal width to prevent overflow on narrow windows.
			// m.width is 0 before the first WindowSizeMsg; fall back to unclamped render.
			if m.width > 4 {
				body = m.styles.ProposedAction.Width(m.width - 4).Render(entry.Content)
			} else {
				body = m.styles.ProposedAction.Render(entry.Content)
			}
		case transcript.TypeActionResult:
			body = m.styles.ActionResult.Render(entry.Content)
		default:
			body = m.styles.EntryContent.Render(entry.Content)
		}

		sb.WriteString(header)
		sb.WriteString("\n")
		sb.WriteString(body)
		if i < len(entries)-1 {
			sb.WriteString("\n\n")
		}
	}

	return sb.String()
}
