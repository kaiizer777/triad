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
		return tea.NewView(" Initializing Triad Studio UI...")
	}

	// 1. Top Title Bar
	header := m.renderTitleBar(m.width)

	// 2. Middle 2-Panel Section (Sidebar + Viewport)
	sidebarWidth := 30
	if m.width < 70 {
		sidebarWidth = m.width / 3
		if sidebarWidth < 18 {
			sidebarWidth = 18
		}
	}
	availHeight := m.height - 6 // Header(1) + PipelineDock(1) + Status(1) + InputBar(3) = 6 lines
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

	// 3. Pipeline Step Dock & Status Bar
	pipelineDock := m.renderPipelineDock(m.width)

	var statusText string
	if m.sessionState == loop.StateActive {
		statusText = fmt.Sprintf(" %s  %s", m.spinner.View(), m.statusMessage)
	} else {
		statusText = fmt.Sprintf("  Status: %s", m.statusMessage)
	}
	statusBar := m.styles.StatusBar.Width(m.width).Render(statusText)

	// 4. Input Bar
	inputBar := m.renderInputBar(m.width)

	// Combine all vertically
	content := lipgloss.JoinVertical(
		lipgloss.Left,
		header,
		middlePanel,
		pipelineDock,
		statusBar,
		inputBar,
	)

	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

// renderTitleBar constructs the gradient/accent top header strip.
func (m Model) renderTitleBar(width int) string {
	brand := m.styles.TitleBrand.Render(" ⚡ TRIAD STUDIO ")
	version := m.styles.TitleVersion.Render(" v1.0.0 ")
	left := lipgloss.JoinHorizontal(lipgloss.Top, brand, version)

	// Right side keycaps
	k1 := m.styles.TitleKeycapKey.Render("Esc")
	l1 := m.styles.TitleKeycapLabel.Render("Quit")
	k2 := m.styles.TitleKeycapKey.Render("Enter")
	l2 := m.styles.TitleKeycapLabel.Render("Submit")
	k3 := m.styles.TitleKeycapKey.Render("↑↓")
	l3 := m.styles.TitleKeycapLabel.Render("Scroll ")

	right := lipgloss.JoinHorizontal(lipgloss.Top, k1, l1, k2, l2, k3, l3)

	centerWidth := width - lipgloss.Width(left) - lipgloss.Width(right)
	if centerWidth < 0 {
		centerWidth = 0
	}

	sessionFile := truncatePath(m.transcript.FilePath(), centerWidth-20)
	var stateIndicator string
	if m.sessionState == loop.StateIdle {
		stateIndicator = "🟢 IDLE"
	} else if m.activeToolCall != nil {
		stateIndicator = "⚡ EXECUTING"
	} else {
		stateIndicator = "🧠 THINKING"
	}

	centerContent := fmt.Sprintf(" 📁 Session: %s  [%s] ", sessionFile, stateIndicator)
	center := m.styles.TitleCenter.Width(centerWidth).Render(centerContent)

	return lipgloss.JoinHorizontal(lipgloss.Top, left, center, right)
}

// renderSidebar constructs the left metadata & controls sidebar panel.
func (m Model) renderSidebar(width int, height int) string {
	var sb strings.Builder

	// Session Overview Section
	sb.WriteString(m.styles.SidebarHeader.Render("◈ SESSION OVERVIEW"))
	sb.WriteString("\n\n")

	// State Card
	sb.WriteString(m.styles.SidebarLabel.Render("State: "))
	if m.sessionState == loop.StateIdle {
		sb.WriteString(m.styles.SidebarBadgeIdle.Render(" 🟢 IDLE "))
	} else if m.activeToolCall != nil {
		sb.WriteString(m.styles.SidebarBadgeActive.Render(" ⚙ EXECUTING "))
	} else {
		sb.WriteString(m.styles.SidebarBadgeThink.Render(" 🧠 THINKING "))
	}
	sb.WriteString("\n\n")

	// Workspace & File
	sb.WriteString(m.styles.SidebarLabel.Render("Dir: "))
	sb.WriteString(m.styles.SidebarValue.Render(truncatePath(m.workDir, width-8)))
	sb.WriteString("\n")

	filePath := m.transcript.FilePath()
	sb.WriteString(m.styles.SidebarLabel.Render("Session: "))
	sb.WriteString(m.styles.SidebarValue.Render(truncatePath(filePath, width-12)))
	sb.WriteString("\n\n")

	// Agents Engine Section
	sb.WriteString(m.styles.SidebarHeader.Render("◈ DUAL AGENT ENGINE"))
	sb.WriteString("\n\n")

	sb.WriteString(m.styles.SidebarLabel.Render("🤖 Coder (Tools ON):\n"))
	sb.WriteString("  " + m.styles.SidebarValue.Render(truncatePath(m.coder.Model, width-6)))
	sb.WriteString("\n")

	sb.WriteString(m.styles.SidebarLabel.Render("🛡 Reviewer (Veto Gate):\n"))
	sb.WriteString("  " + m.styles.SidebarValue.Render(truncatePath(m.reviewer.Model, width-6)))
	sb.WriteString("\n\n")

	// Loop State & Retries Meter Section
	sb.WriteString(m.styles.SidebarHeader.Render("◈ PIPELINE METRICS"))
	sb.WriteString("\n\n")

	sb.WriteString(m.styles.SidebarLabel.Render("Retries: "))
	sb.WriteString(renderProgressBar(m.retryCount, m.MaxRetries, m.styles))
	sb.WriteString(fmt.Sprintf(" %d/%d\n", m.retryCount, m.MaxRetries))

	sb.WriteString(m.styles.SidebarLabel.Render("Plain Turns: "))
	sb.WriteString(m.styles.SidebarValue.Render(fmt.Sprintf("%d/%d\n\n", m.plainTextTurns, MaxPlainTextTurns)))

	// Controls Section
	sb.WriteString(m.styles.SidebarHeader.Render("◈ CONTROLS"))
	sb.WriteString("\n")
	sb.WriteString(m.styles.SidebarValue.Render("• [Enter] Send prompt"))
	sb.WriteString("\n")
	sb.WriteString(m.styles.SidebarValue.Render("• [Esc] Exit app"))
	sb.WriteString("\n")
	sb.WriteString(m.styles.SidebarValue.Render("• [↑/↓] Viewport scroll"))

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

// renderPipelineDock builds a visual multi-step pipeline indicator strip.
func (m Model) renderPipelineDock(width int) string {
	s1 := m.styles.PipelineStepDone.Render(" 1. User Prompt ")
	s2 := m.styles.PipelineStepPending.Render(" 2. Coder Propose ")
	s3 := m.styles.PipelineStepPending.Render(" 3. Reviewer Check ")
	s4 := m.styles.PipelineStepPending.Render(" 4. Exec Tool ")

	if m.sessionState == loop.StateActive {
		if m.activeToolCall != nil {
			s2 = m.styles.PipelineStepDone.Render(" 2. Coder Propose ")
			s3 = m.styles.PipelineStepDone.Render(" 3. Reviewer Check ")
			s4 = m.styles.PipelineStepActive.Render(" 4. Exec Tool ")
		} else if strings.Contains(m.statusMessage, "Reviewer") {
			s2 = m.styles.PipelineStepDone.Render(" 2. Coder Propose ")
			s3 = m.styles.PipelineStepActive.Render(" 3. Reviewer Check ")
		} else {
			s2 = m.styles.PipelineStepActive.Render(" 2. Coder Propose ")
		}
	} else if m.sessionState == loop.StateIdle {
		s1 = m.styles.PipelineStepPending.Render(" 1. User Prompt ")
	}

	arrow := m.styles.PipelineArrow.Render(" ➔ ")
	dockContent := lipgloss.JoinHorizontal(lipgloss.Top, s1, arrow, s2, arrow, s3, arrow, s4)

	return m.styles.TitleCenter.Width(width).Render(" PIPELINE: " + dockContent)
}

// renderInputBar constructs the bottom bordered input bar with hint.
func (m Model) renderInputBar(width int) string {
	pill := m.styles.InputPill.Render(" 💬 YOU ")
	inputView := m.input.View()

	containerWidth := width - 2
	if containerWidth < 10 {
		containerWidth = 10
	}

	hint := m.styles.InputHint.Render(" [Enter ↵] ")

	content := lipgloss.JoinHorizontal(
		lipgloss.Center,
		pill,
		" ",
		inputView,
		hint,
	)

	return m.styles.InputContainer.Width(containerWidth).Render(content)
}

// renderProposedAction formats a proposed action as a syntax-highlighted code block box.
func (m Model) renderProposedAction(content string, width int) string {
	lines := strings.Split(content, "\n")
	var sb strings.Builder

	funcName := "action"
	if len(lines) > 0 && strings.HasPrefix(lines[0], "Proposed tool call:") {
		funcName = strings.TrimSpace(strings.TrimPrefix(lines[0], "Proposed tool call:"))
	}

	header := fmt.Sprintf(" ⚡ TOOL CALL: %s", m.styles.ToolCallFunc.Render(funcName))
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

			sb.WriteString("  ")
			sb.WriteString(m.styles.ToolCallKey.Render(key))
			sb.WriteString(" ")
			if strings.Contains(val, `"`) {
				sb.WriteString(m.styles.ToolCallVal.Render(val))
			} else {
				sb.WriteString(m.styles.ToolCallNum.Render(val))
			}
		} else {
			sb.WriteString("  ")
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
		return m.styles.StatusBar.Render(" Transcript empty. Enter a task below to start!")
	}

	var sb strings.Builder
	for i, entry := range entries {
		ts := m.styles.Timestamp.Render(entry.Timestamp.Format("15:04:05"))

		var pill string
		var accentBar lipgloss.Style

		switch entry.Speaker {
		case transcript.SpeakerYou:
			pill = m.styles.YouPill.Render(" 👤 YOU ")
			accentBar = m.styles.YouMessageBar
		case transcript.SpeakerCoder:
			pill = m.styles.CoderPill.Render(" 🤖 CODER ")
			accentBar = m.styles.CoderMessageBar
		case transcript.SpeakerReviewer:
			pill = m.styles.ReviewerPill.Render(" 🛡 REVIEWER ")
			accentBar = m.styles.ReviewerMessageBar
		case transcript.SpeakerSystem:
			pill = m.styles.SystemPill.Render(" ⚙ SYSTEM ")
			accentBar = m.styles.TitleKeycapKey
		default:
			pill = m.styles.SystemPill.Render(fmt.Sprintf(" %s ", entry.Speaker))
			accentBar = m.styles.TitleKeycapKey
		}

		header := fmt.Sprintf("%s %s", pill, ts)

		var body string
		switch entry.Type {
		case transcript.TypeProposedAction:
			body = m.renderProposedAction(entry.Content, m.viewport.Width())

		case transcript.TypeActionResult:
			body = fmt.Sprintf("  %s %s", m.styles.ToolCallKey.Render("❯"), m.styles.ActionResult.Render(entry.Content))

		default:
			content := entry.Content

			// Check for Approval or Objection banners
			if strings.HasPrefix(content, "APPROVED") {
				badge := m.styles.ApprovedBadge.Render("✓ APPROVED BY REVIEWER")
				content = badge + "\n" + strings.TrimPrefix(content, "APPROVED")
			} else if strings.HasPrefix(content, "OBJECTION") {
				badge := m.styles.ObjectionBadge.Render("🛑 OBJECTION BY REVIEWER")
				content = badge + "\n" + strings.TrimPrefix(content, "OBJECTION")
			}

			if entry.Speaker == transcript.SpeakerYou {
				// Render user prompt inside a callout card
				boxWidth := m.viewport.Width() - 4
				if boxWidth < 10 {
					boxWidth = 10
				}
				body = m.styles.UserCalloutBox.Width(boxWidth).Render(content)
			} else {
				// Format lines with left accent bar and markdown styling
				lines := strings.Split(content, "\n")
				var formattedLines []string
				for _, line := range lines {
					formattedLines = append(formattedLines, fmt.Sprintf("  %s %s", accentBar.Render("▎"), formatMarkdownLine(line, m.styles)))
				}
				body = strings.Join(formattedLines, "\n")
			}
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

// formatMarkdownLine applies basic inline styling for bold, code, and bullet points.
func formatMarkdownLine(line string, styles Styles) string {
	if strings.HasPrefix(strings.TrimSpace(line), "• ") || strings.HasPrefix(strings.TrimSpace(line), "- ") {
		line = styles.MdBullet.Render("• ") + strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(line), "• "), "- ")
	}
	return styles.EntryContent.Render(line)
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

func renderProgressBar(value int, max int, styles Styles) string {
	totalBlocks := 5
	filled := 0
	if max > 0 {
		filled = (value * totalBlocks) / max
	}
	if filled > totalBlocks {
		filled = totalBlocks
	}

	var sb strings.Builder
	sb.WriteString("[")
	for i := 0; i < totalBlocks; i++ {
		if i < filled {
			sb.WriteString(styles.SidebarMeterFill.Render("█"))
		} else {
			sb.WriteString(styles.SidebarMeterEmpty.Render("░"))
		}
	}
	sb.WriteString("]")
	return sb.String()
}
