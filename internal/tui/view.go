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
		return tea.NewView(" ⚡ Initializing Triad Studio UI...")
	}

	// 1. Top Title Bar (1 line)
	header := m.renderTitleBar(m.width)

	// 2. Middle 2-Panel Section (Sidebar + Viewport)
	// Vertical budget: Header(1) + PipelineDock(1) + Status(1) + InputBar(3) = 6 lines
	availHeight := m.height - 6
	if availHeight < 1 {
		availHeight = 1
	}

	var middlePanel string
	if m.width < 75 {
		// Responsive mode: hide sidebar on small terminal width
		vpContainer := m.styles.ViewportContainer.
			Width(m.width - 2).
			Height(availHeight).
			Render(m.viewport.View())
		middlePanel = vpContainer
	} else {
		sidebarWidth := 30
		if m.width < 100 {
			sidebarWidth = 26
		}

		sidebar := m.renderSidebar(sidebarWidth, availHeight)

		mainContainerWidth := m.width - sidebarWidth
		if mainContainerWidth < 10 {
			mainContainerWidth = 10
		}

		vpContainer := m.styles.ViewportContainer.
			Width(mainContainerWidth - 2).
			Height(availHeight).
			Render(m.viewport.View())

		middlePanel = lipgloss.JoinHorizontal(lipgloss.Top, sidebar, vpContainer)
	}

	// 3. Pipeline Step Dock & Status Bar (1 line each)
	pipelineDock := m.renderPipelineDock(m.width)
	statusBar := m.renderStatusBar(m.width)

	// 4. Input Bar (3 lines)
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

	// Fail-safe height clipping: guarantee no terminal vertical scrolling
	content = fitToHeight(content, m.height)

	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

// renderTitleBar constructs the high-tech studio toolbar with brand tags,
// breadcrumbs, live state indicator, and keycap shortcut badges.
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

	sessionFile := truncatePath(m.transcript.FilePath(), max(5, centerWidth-22))

	var stateIndicator string
	switch {
	case m.sessionState == loop.StateIdle:
		stateIndicator = "🟢 IDLE"
	case m.activeToolCall != nil:
		stateIndicator = "⚡ EXECUTING " + m.spinner.View()
	default:
		stateIndicator = "🧠 THINKING " + m.spinner.View()
	}

	centerContent := fmt.Sprintf(" 📁 %s   ⋮   %s ", sessionFile, stateIndicator)
	center := m.styles.TitleCenter.Width(centerWidth).Render(truncateLine(centerContent, centerWidth))

	res := lipgloss.JoinHorizontal(lipgloss.Top, left, center, right)
	return clipLines(res, 1)
}

// renderSidebar constructs the left metadata & controls sidebar panel.
func (m Model) renderSidebar(width int, height int) string {
	containerHeight := height - 2 // inner content height inside borders
	if containerHeight < 1 {
		containerHeight = 1
	}

	var sb strings.Builder

	// Header / Title
	sb.WriteString(m.styles.SidebarHeader.Render("◈ SESSION OVERVIEW"))
	sb.WriteString("\n")
	sb.WriteString(m.styles.SidebarSubHeader.Render(strings.Repeat("─", max(1, width-4))))
	sb.WriteString("\n")

	// State
	sb.WriteString(m.styles.SidebarLabel.Render("State: "))
	switch {
	case m.sessionState == loop.StateIdle:
		sb.WriteString(m.styles.SidebarBadgeIdle.Render(" 🟢 IDLE "))
	case m.activeToolCall != nil:
		sb.WriteString(m.styles.SidebarBadgeActive.Render(" ⚙ EXECUTING "))
	default:
		sb.WriteString(m.styles.SidebarBadgeThink.Render(" 🧠 THINKING "))
	}
	sb.WriteString("\n")

	// Paths
	sb.WriteString(m.styles.SidebarLabel.Render("📂 Workdir: "))
	sb.WriteString(m.styles.SidebarValue.Render(truncatePath(m.workDir, max(4, width-14))))
	sb.WriteString("\n")

	filePath := m.transcript.FilePath()
	sb.WriteString(m.styles.SidebarLabel.Render("📝 Session: "))
	sb.WriteString(m.styles.SidebarValue.Render(truncatePath(filePath, max(4, width-14))))
	sb.WriteString("\n\n")

	// Dual Agents
	if containerHeight >= 12 {
		sb.WriteString(m.styles.SidebarHeader.Render("◈ DUAL AGENTS"))
		sb.WriteString("\n")
		sb.WriteString(m.styles.SidebarSubHeader.Render(strings.Repeat("─", max(1, width-4))))
		sb.WriteString("\n")
		sb.WriteString(m.styles.CoderPill.Render(" CODER ") + " " + m.styles.SidebarValue.Render(truncatePath(m.coder.Model, max(4, width-12))))
		sb.WriteString("\n")
		sb.WriteString(m.styles.ReviewerPill.Render(" REVIEWER ") + " " + m.styles.SidebarValue.Render(truncatePath(m.reviewer.Model, max(4, width-14))))
		sb.WriteString("\n\n")
	}

	// Metrics
	if containerHeight >= 16 {
		sb.WriteString(m.styles.SidebarHeader.Render("◈ METRICS"))
		sb.WriteString("\n")
		sb.WriteString(m.styles.SidebarSubHeader.Render(strings.Repeat("─", max(1, width-4))))
		sb.WriteString("\n")
		sb.WriteString(m.styles.SidebarLabel.Render("↻ Retries  "))
		sb.WriteString(renderProgressBar(m.retryCount, m.MaxRetries, m.styles))
		sb.WriteString(m.styles.SidebarValue.Render(fmt.Sprintf(" %d/%d", m.retryCount, m.MaxRetries)))
		sb.WriteString("\n")

		sb.WriteString(m.styles.SidebarLabel.Render("💬 Turns    "))
		sb.WriteString(renderProgressBar(m.plainTextTurns, MaxPlainTextTurns, m.styles))
		sb.WriteString(m.styles.SidebarValue.Render(fmt.Sprintf(" %d/%d", m.plainTextTurns, MaxPlainTextTurns)))
		sb.WriteString("\n\n")
	}

	// Controls
	if containerHeight >= 20 {
		sb.WriteString(m.styles.SidebarHeader.Render("◈ CONTROLS"))
		sb.WriteString("\n")
		sb.WriteString(m.styles.SidebarSubHeader.Render(strings.Repeat("─", max(1, width-4))))
		sb.WriteString("\n")
		sb.WriteString(m.styles.TitleKeycapKey.Render("Enter") + " " + m.styles.SidebarValue.Render("Submit"))
		sb.WriteString("  ")
		sb.WriteString(m.styles.TitleKeycapKey.Render("Esc") + " " + m.styles.SidebarValue.Render("Quit"))
		sb.WriteString("\n")
		sb.WriteString(m.styles.TitleKeycapKey.Render("↑/↓") + " " + m.styles.SidebarValue.Render("Scroll Feed"))
	}

	containerWidth := width - 2
	if containerWidth < 10 {
		containerWidth = 10
	}

	clipped := clipLines(sb.String(), containerHeight)

	return m.styles.SidebarContainer.
		Width(containerWidth).
		Height(height).
		Render(clipped)
}

// renderPipelineDock builds a visual multi-step pipeline indicator strip
// with the active step dynamically highlighted alongside spinner motion.
func (m Model) renderPipelineDock(width int) string {
	var s1, s2, s3, s4 string

	if width < 75 {
		s1 = m.styles.PipelineStepDone.Render(" 1 ⬤ ")
		s2 = m.styles.PipelineStepPending.Render(" 2 ○ ")
		s3 = m.styles.PipelineStepPending.Render(" 3 ○ ")
		s4 = m.styles.PipelineStepPending.Render(" 4 ○ ")

		if m.sessionState == loop.StateActive {
			if m.activeToolCall != nil {
				s2 = m.styles.PipelineStepDone.Render(" 2 ✓ ")
				s3 = m.styles.PipelineStepDone.Render(" 3 ✓ ")
				s4 = m.styles.PipelineStepActive.Render(" 4 " + m.spinner.View() + " ")
			} else if strings.Contains(m.statusMessage, "Reviewer") {
				s2 = m.styles.PipelineStepDone.Render(" 2 ✓ ")
				s3 = m.styles.PipelineStepActive.Render(" 3 " + m.spinner.View() + " ")
			} else {
				s2 = m.styles.PipelineStepActive.Render(" 2 " + m.spinner.View() + " ")
			}
		} else if m.sessionState == loop.StateIdle {
			s1 = m.styles.PipelineStepPending.Render(" 1 ○ ")
		}
	} else {
		s1 = m.styles.PipelineStepDone.Render(" 1 ⬤ User Prompt ")
		s2 = m.styles.PipelineStepPending.Render(" 2 ○ Coder Propose ")
		s3 = m.styles.PipelineStepPending.Render(" 3 ○ Reviewer Check ")
		s4 = m.styles.PipelineStepPending.Render(" 4 ○ Exec Tool ")

		if m.sessionState == loop.StateActive {
			if m.activeToolCall != nil {
				s2 = m.styles.PipelineStepDone.Render(" 2 ✓ Coder Propose ")
				s3 = m.styles.PipelineStepDone.Render(" 3 ✓ Reviewer Check ")
				s4 = m.styles.PipelineStepActive.Render(" 4 " + m.spinner.View() + " Exec Tool ")
			} else if strings.Contains(m.statusMessage, "Reviewer") {
				s2 = m.styles.PipelineStepDone.Render(" 2 ✓ Coder Propose ")
				s3 = m.styles.PipelineStepActive.Render(" 3 " + m.spinner.View() + " Reviewer Check ")
			} else {
				s2 = m.styles.PipelineStepActive.Render(" 2 " + m.spinner.View() + " Coder Propose ")
			}
		} else if m.sessionState == loop.StateIdle {
			s1 = m.styles.PipelineStepPending.Render(" 1 ○ User Prompt ")
		}
	}

	arrow := m.styles.PipelineArrow.Render(" ➔ ")
	dockContent := lipgloss.JoinHorizontal(lipgloss.Top, s1, arrow, s2, arrow, s3, arrow, s4)

	res := m.styles.TitleCenter.Width(width).Render(" ⛭ PIPELINE  " + dockContent)
	return clipLines(res, 1)
}

// renderStatusBar renders the live status line with spinner when active.
func (m Model) renderStatusBar(width int) string {
	var statusText string
	if m.sessionState == loop.StateActive {
		statusText = fmt.Sprintf(" %s %s", m.spinner.View(), m.statusMessage)
	} else {
		statusText = fmt.Sprintf(" ✓ %s", m.statusMessage)
	}
	truncated := truncateLine(statusText, max(10, width-2))
	res := m.styles.StatusBar.Width(width).Render(truncated)
	return clipLines(res, 1)
}

// renderInputBar constructs the bottom bordered input bar with hint.
func (m Model) renderInputBar(width int) string {
	pill := m.styles.InputPill.Render(" 💬 YOU ❯ ")
	inputView := m.input.View()

	containerWidth := width - 2
	if containerWidth < 10 {
		containerWidth = 10
	}

	hintText := " [Enter ↵ Submit] "
	if m.sessionState == loop.StateActive {
		hintText = " [Enter ↵ Interject] "
	}
	hint := m.styles.InputHint.Render(hintText)

	content := lipgloss.JoinHorizontal(
		lipgloss.Center,
		pill,
		" ",
		inputView,
		hint,
	)

	res := m.styles.InputContainer.Width(containerWidth).Render(content)
	return clipLines(res, 3)
}

// renderProposedAction formats a proposed action as a syntax-highlighted code block box.
func (m Model) renderProposedAction(content string, width int) string {
	lines := strings.Split(content, "\n")
	var sb strings.Builder

	funcName := "action"
	if len(lines) > 0 && strings.HasPrefix(lines[0], "Proposed tool call:") {
		funcName = strings.TrimSpace(strings.TrimPrefix(lines[0], "Proposed tool call:"))
	}

	header := fmt.Sprintf(" ⚡ TOOL PROPOSAL  %s", m.styles.ToolCallFunc.Render(funcName))
	sb.WriteString(header)
	sb.WriteString("\n")
	sb.WriteString(m.styles.SidebarSubHeader.Render(strings.Repeat("─", max(1, width-8))))
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
		var sb strings.Builder
		sb.WriteString("\n")
		sb.WriteString(m.styles.SidebarHeader.Render("  ⚡ TRIAD STUDIO v1.0.0 — Dual-Agent Coding Engine"))
		sb.WriteString("\n")
		sb.WriteString(m.styles.SidebarSubHeader.Render("  " + strings.Repeat("─", max(10, m.viewport.Width()-6))))
		sb.WriteString("\n\n")
		sb.WriteString("  🤖 " + m.styles.CoderPill.Render(" CODER ") + " " + m.styles.SidebarValue.Render("Proposes file edits, commands, and tool calls"))
		sb.WriteString("\n")
		sb.WriteString("  🛡 " + m.styles.ReviewerPill.Render(" REVIEWER ") + " " + m.styles.SidebarValue.Render("Inspects & enforces safety veto gates"))
		sb.WriteString("\n\n")
		sb.WriteString("  " + m.styles.TitleKeycapKey.Render(" Tip ") + " " + m.styles.SidebarSubHeader.Render("Type your instructions in the prompt box below and press ") + m.styles.TitleKeycapKey.Render("Enter ↵"))
		return sb.String()
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
				boxWidth := m.viewport.Width() - 4
				if boxWidth < 10 {
					boxWidth = 10
				}
				body = m.styles.UserCalloutBox.Width(boxWidth).Render(content)
			} else {
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
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "• ") || strings.HasPrefix(trimmed, "- ") {
		rest := strings.TrimPrefix(strings.TrimPrefix(trimmed, "• "), "- ")
		return styles.MdBullet.Render("● ") + styles.EntryContent.Render(rest)
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

func renderProgressBar(value int, maxVal int, styles Styles) string {
	totalBlocks := 8
	filled := 0
	if maxVal > 0 {
		filled = (value * totalBlocks) / maxVal
	}
	if filled > totalBlocks {
		filled = totalBlocks
	}

	var sb strings.Builder
	for i := 0; i < totalBlocks; i++ {
		if i < filled {
			sb.WriteString(styles.SidebarMeterFill.Render("▰"))
		} else {
			sb.WriteString(styles.SidebarMeterEmpty.Render("▱"))
		}
	}
	return sb.String()
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func clipLines(s string, maxLines int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= maxLines {
		return s
	}
	return strings.Join(lines[:maxLines], "\n")
}

func fitToHeight(s string, maxLines int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= maxLines {
		return s
	}
	return strings.Join(lines[:maxLines], "\n")
}

func truncateLine(s string, maxLen int) string {
	if maxLen <= 3 {
		return "..."
	}
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
