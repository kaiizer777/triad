package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/kaiizer777/triad/internal/loop"
	"github.com/kaiizer777/triad/internal/transcript"
)

// View renders the complete TUI layout.
func (m Model) View() tea.View {
	if !m.ready {
		return tea.NewView("Initializing Triad UI...")
	}

	// 1. Top Title Bar
	header := m.renderTitleBar(m.width)

	// 2. Middle 2-Panel Section (Sidebar + Viewport)
	sidebarWidth := 28
	if m.width < 60 {
		sidebarWidth = m.width / 3
		if sidebarWidth < 15 {
			sidebarWidth = 15
		}
	}
	availHeight := m.height - 5
	if availHeight < 1 {
		availHeight = 1
	}

	sidebar := m.renderSidebar(sidebarWidth, availHeight)

	mainContainerWidth := m.width - sidebarWidth
	if mainContainerWidth < 10 {
		mainContainerWidth = 10
	}

	vpContainer := m.styles.ViewportContainer.
		Width(mainContainerWidth - 2).
		Height(availHeight - 2).
		Render(m.viewport.View())

	middlePanel := lipgloss.JoinHorizontal(lipgloss.Top, sidebar, vpContainer)

	// 3. Status Bar (with Spinner when Active)
	var statusText string
	if m.sessionState == loop.StateActive {
		statusText = fmt.Sprintf(" %s %s", m.spinner.View(), m.statusMessage)
	} else {
		statusText = fmt.Sprintf(" Status: %s", m.statusMessage)
	}
	statusBar := m.styles.StatusBar.Render(statusText)

	// 4. Input Bar
	inputBar := m.renderInputBar(m.width)

	// Combine all vertically
	content := lipgloss.JoinVertical(
		lipgloss.Left,
		header,
		middlePanel,
		statusBar,
		inputBar,
	)

	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

// renderTitleBar constructs the gradient/accent top header strip.
func (m Model) renderTitleBar(width int) string {
	left := m.styles.TitleBarLeft.Render(" ⚡ TRIAD v1.0 ")
	right := m.styles.TitleBarRight.Render(" [ESC] Quit · [↑/↓] Scroll · [Enter] Send ")

	filePath := m.transcript.FilePath()
	centerWidth := width - lipgloss.Width(left) - lipgloss.Width(right)
	if centerWidth < 0 {
		centerWidth = 0
	}

	centerContent := fmt.Sprintf(" 📁 Session: %s ", truncatePath(filePath, centerWidth-15))
	center := m.styles.TitleBarCenter.Width(centerWidth).Render(centerContent)

	return lipgloss.JoinHorizontal(lipgloss.Top, left, center, right)
}

// renderSidebar constructs the left metadata & controls sidebar panel.
func (m Model) renderSidebar(width int, height int) string {
	var sb strings.Builder

	sb.WriteString(m.styles.SidebarHeader.Render("📌 SESSION INFO"))
	sb.WriteString("\n\n")

	sb.WriteString(m.styles.SidebarLabel.Render("State: "))
	if m.sessionState == loop.StateIdle {
		sb.WriteString(m.styles.SidebarBadgeIdle.Render("🟢 IDLE"))
	} else {
		sb.WriteString(m.styles.SidebarBadgeActive.Render("⚡ ACTIVE"))
	}
	sb.WriteString("\n\n")

	sb.WriteString(m.styles.SidebarLabel.Render("Dir: \n"))
	sb.WriteString(m.styles.SidebarValue.Render(truncatePath(m.workDir, width-6)))
	sb.WriteString("\n\n")

	filePath := m.transcript.FilePath()
	sb.WriteString(m.styles.SidebarLabel.Render("Session: \n"))
	sb.WriteString(m.styles.SidebarValue.Render(truncatePath(filePath, width-6)))
	sb.WriteString("\n\n")

	sb.WriteString(m.styles.SidebarLabel.Render("Coder: \n"))
	sb.WriteString(m.styles.SidebarValue.Render(truncatePath(m.coder.Model, width-6)))
	sb.WriteString("\n\n")

	sb.WriteString(m.styles.SidebarLabel.Render("Reviewer: \n"))
	sb.WriteString(m.styles.SidebarValue.Render(truncatePath(m.reviewer.Model, width-6)))
	sb.WriteString("\n\n")

	sb.WriteString(m.styles.SidebarLabel.Render("Retries: "))
	sb.WriteString(m.styles.SidebarValue.Render(fmt.Sprintf("%d / %d", m.retryCount, m.MaxRetries)))
	sb.WriteString("\n\n")

	sb.WriteString(m.styles.SidebarHeader.Render("⌨ CONTROLS"))
	sb.WriteString("\n")
	sb.WriteString(m.styles.SidebarValue.Render("• [Enter] Send"))
	sb.WriteString("\n")
	sb.WriteString(m.styles.SidebarValue.Render("• [Esc] Quit"))
	sb.WriteString("\n")
	sb.WriteString(m.styles.SidebarValue.Render("• [↑/↓] Scroll"))

	containerWidth := width - 2
	if containerWidth < 10 {
		containerWidth = 10
	}
	containerHeight := height - 2
	if containerHeight < 5 {
		containerHeight = 5
	}

	return m.styles.SidebarContainer.
		Width(containerWidth).
		Height(containerHeight).
		Render(sb.String())
}

// renderInputBar constructs the bottom bordered input bar.
func (m Model) renderInputBar(width int) string {
	pill := m.styles.InputPill.Render(" 👤 You ")
	inputView := m.input.View()

	containerWidth := width - 2
	if containerWidth < 10 {
		containerWidth = 10
	}

	content := lipgloss.JoinHorizontal(lipgloss.Center, pill, " ", inputView)
	return m.styles.InputContainer.Width(containerWidth).Render(content)
}

// renderProposedAction formats a proposed action as a syntax-highlighted code block box.
func (m Model) renderProposedAction(content string, width int) string {
	lines := strings.Split(content, "\n")
	var sb strings.Builder

	funcName := ""
	if len(lines) > 0 && strings.HasPrefix(lines[0], "Proposed tool call:") {
		funcName = strings.TrimSpace(strings.TrimPrefix(lines[0], "Proposed tool call:"))
	}

	header := fmt.Sprintf("🛠  PROPOSED ACTION: %s", m.styles.ToolCallFunc.Render(funcName))
	sb.WriteString(header)
	sb.WriteString("\n")

	argLines := lines
	if len(lines) > 1 {
		argLines = lines[1:]
	}

	for _, line := range argLines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed == "Arguments:" {
			continue
		}

		if idx := strings.Index(line, ":"); idx != -1 && strings.Contains(line[:idx], `"`) {
			key := line[:idx+1]
			val := line[idx+1:]
			sb.WriteString(m.styles.ToolCallKey.Render(key))
			sb.WriteString(m.styles.ToolCallVal.Render(val))
		} else {
			sb.WriteString(m.styles.ToolCallVal.Render(line))
		}
		sb.WriteString("\n")
	}

	boxWidth := width - 4
	if boxWidth < 10 {
		boxWidth = 10
	}
	return m.styles.ToolCallBox.Width(boxWidth).Render(strings.TrimRight(sb.String(), "\n"))
}

// renderTranscript formats all transcript entries with Lipgloss styles for the viewport.
func (m Model) renderTranscript() string {
	entries := m.transcript.Entries()
	if len(entries) == 0 {
		return m.styles.StatusBar.Render("Transcript empty. Enter a task below to start!")
	}

	var sb strings.Builder
	for i, entry := range entries {
		ts := m.styles.Timestamp.Render(entry.Timestamp.Format("15:04:05"))

		var pill string
		switch entry.Speaker {
		case transcript.SpeakerYou:
			pill = m.styles.YouPill.Render(" 👤 You ")
		case transcript.SpeakerCoder:
			pill = m.styles.CoderPill.Render(" 🔧 Coder ")
		case transcript.SpeakerReviewer:
			pill = m.styles.ReviewerPill.Render(" 🔍 Reviewer ")
		case transcript.SpeakerSystem:
			pill = m.styles.SystemPill.Render(" ⚙ System ")
		default:
			pill = m.styles.SystemPill.Render(fmt.Sprintf(" %s ", entry.Speaker))
		}

		header := fmt.Sprintf("%s %s", pill, ts)

		var body string
		switch entry.Type {
		case transcript.TypeProposedAction:
			body = m.renderProposedAction(entry.Content, m.viewport.Width())
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

func truncatePath(path string, maxLen int) string {
	if maxLen <= 3 {
		return "..."
	}
	if len(path) <= maxLen {
		return path
	}
	return "..." + path[len(path)-(maxLen-3):]
}
