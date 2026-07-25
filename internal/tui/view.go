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
		return tea.NewView(" Initializing Triad Studio...")
	}

	width := m.width
	height := m.height
	if width < 10 {
		width = 10
	}
	if height < 10 {
		height = 10
	}

	// Layout budget: Header(1) + Pipeline(1) + Status(1) + InputBar(3) = 6 lines reserved
	availHeight := height - 6
	if availHeight < 1 {
		availHeight = 1
	}

	// 1. Header
	header := m.renderTitleBar(width)

	// 2. Middle panel: Sidebar + Viewport
	var middlePanel string
	if width < 80 {
		// Narrow: hide sidebar
		vpContentWidth := max(1, width-m.styles.ViewportContainer.GetHorizontalFrameSize())
		vpContentHeight := max(1, availHeight-m.styles.ViewportContainer.GetVerticalFrameSize())
		middlePanel = m.styles.ViewportContainer.
			Width(vpContentWidth).
			Height(vpContentHeight).
			Render(m.viewport.View())
	} else {
		sidebarWidth := 32
		if width < 110 {
			sidebarWidth = 28
		}
		sidebar := m.renderSidebar(sidebarWidth, availHeight)

		mainContainerWidth := width - sidebarWidth
		if mainContainerWidth < 10 {
			mainContainerWidth = 10
		}
		vpContentWidth := max(1, mainContainerWidth-m.styles.ViewportContainer.GetHorizontalFrameSize())
		vpContentHeight := max(1, availHeight-m.styles.ViewportContainer.GetVerticalFrameSize())
		vpContainer := m.styles.ViewportContainer.
			Width(vpContentWidth).
			Height(vpContentHeight).
			Render(m.viewport.View())
		middlePanel = lipgloss.JoinHorizontal(lipgloss.Top, sidebar, vpContainer)
	}

	// 3. Pipeline Dock
	pipelineDock := m.renderPipelineDock(width)

	// 4. Status Bar
	statusBar := m.renderStatusBar(width)

	// 5. Input Bar
	inputBar := m.renderInputBar(width)

	content := lipgloss.JoinVertical(
		lipgloss.Left,
		header,
		middlePanel,
		pipelineDock,
		statusBar,
		inputBar,
	)

	formattedContent := fitToCanvas(content, width, height)
	v := tea.NewView(formattedContent)
	v.AltScreen = true
	return v
}

// renderTitleBar builds the studio toolbar: brand, session path, live state, keycaps.
func (m Model) renderTitleBar(width int) string {
	brand := m.styles.TitleBrand.Render(" TRIAD ")
	version := m.styles.TitleVersion.Render(" v1.0 ")
	sep := m.styles.TitleCenter.Render(" | ")
	left := lipgloss.JoinHorizontal(lipgloss.Top, brand, version, sep)

	var rightParts []string
	if width >= 100 {
		rightParts = append(rightParts,
			m.styles.TitleKeycapKey.Render("tt"),
			m.styles.TitleKeycapLabel.Render(" Scroll "),
		)
	}
	rightParts = append(rightParts,
		m.styles.TitleKeycapKey.Render("Enter"),
		m.styles.TitleKeycapLabel.Render(" Submit "),
		m.styles.TitleKeycapKey.Render("Esc"),
		m.styles.TitleKeycapLabel.Render(" Quit"),
	)
	right := lipgloss.JoinHorizontal(lipgloss.Top, rightParts...)

	leftW := lipgloss.Width(left)
	rightW := lipgloss.Width(right)
	centerWidth := width - leftW - rightW
	if centerWidth < 0 {
		centerWidth = 0
	}

	var stateStr string
	switch {
	case m.sessionState == loop.StateIdle:
		stateStr = " IDLE"
	case m.activeToolCall != nil:
		stateStr = " EXEC " + m.spinner.View()
	default:
		stateStr = " THINK " + m.spinner.View()
	}

	centerFrameW := m.styles.TitleCenter.GetHorizontalFrameSize()
	centerStyleW := max(0, centerWidth-centerFrameW)
	sessFile := truncatePath(m.transcript.FilePath(), max(5, centerStyleW-lipgloss.Width(stateStr)-4))
	centerContent := " " + sessFile + "  " + stateStr + " "
	center := m.styles.TitleCenter.Width(centerStyleW).Render(truncateLine(centerContent, centerStyleW))

	res := lipgloss.JoinHorizontal(lipgloss.Top, left, center, right)
	return clipLines(res, 1)
}

// renderSidebar builds the left panel with glowing section headers, agent info,
// progress meters, and controls. All widths are measured with lipgloss.Width()
// to handle multi-byte/wide Unicode characters correctly on Windows Terminal.
func (m Model) renderSidebar(width int, height int) string {
	frameHoriz := m.styles.SidebarContainer.GetHorizontalFrameSize()
	frameVert := m.styles.SidebarContainer.GetVerticalFrameSize()

	innerWidth := width - frameHoriz
	if innerWidth < 6 {
		innerWidth = 6
	}
	innerHeight := height - frameVert
	if innerHeight < 1 {
		innerHeight = 1
	}

	// Use ASCII-safe rule character to guarantee 1-wide per char
	rule := m.styles.SidebarSubHeader.Render(strings.Repeat("-", innerWidth))

	var sb strings.Builder

	// ── SESSION OVERVIEW ─────────────────────────────────────────
	sb.WriteString(m.styles.SidebarHeader.Render(">> SESSION OVERVIEW"))
	sb.WriteString("\n")
	sb.WriteString(rule)
	sb.WriteString("\n")

	// State badge
	stateLabelW := lipgloss.Width(m.styles.SidebarLabel.Render(" State "))
	_ = stateLabelW
	sb.WriteString(m.styles.SidebarLabel.Render(" State  "))
	switch {
	case m.sessionState == loop.StateIdle:
		sb.WriteString(m.styles.SidebarBadgeIdle.Render(" IDLE "))
	case m.activeToolCall != nil:
		sb.WriteString(m.styles.SidebarBadgeActive.Render(" EXEC "))
	default:
		sb.WriteString(m.styles.SidebarBadgeThink.Render(" THINK "))
	}
	sb.WriteString("\n")

	// Workdir
	dirLabel := m.styles.SidebarLabel.Render(" Dir  ")
	dirLabelW := lipgloss.Width(dirLabel)
	dirVal := truncatePath(m.workDir, max(3, innerWidth-dirLabelW))
	sb.WriteString(dirLabel)
	sb.WriteString(m.styles.SidebarValue.Render(dirVal))
	sb.WriteString("\n")

	// Session file
	fileLabel := m.styles.SidebarLabel.Render(" File ")
	fileLabelW := lipgloss.Width(fileLabel)
	fileVal := truncatePath(m.transcript.FilePath(), max(3, innerWidth-fileLabelW))
	sb.WriteString(fileLabel)
	sb.WriteString(m.styles.SidebarValue.Render(fileVal))
	sb.WriteString("\n")

	// ── DUAL AGENT ENGINE ────────────────────────────────────────
	if innerHeight >= 11 {
		sb.WriteString("\n")
		sb.WriteString(m.styles.SidebarHeader.Render(">> DUAL AGENT ENGINE"))
		sb.WriteString("\n")
		sb.WriteString(rule)
		sb.WriteString("\n")

		coderPillW := lipgloss.Width(m.styles.CoderPill.Render(" CODER ")) + 1
		coderVal := truncatePath(m.coder.Model, max(3, innerWidth-coderPillW))
		sb.WriteString(m.styles.CoderPill.Render(" CODER "))
		sb.WriteString(" ")
		sb.WriteString(m.styles.SidebarValue.Render(coderVal))
		sb.WriteString("\n")

		revPillW := lipgloss.Width(m.styles.ReviewerPill.Render(" REVIEWER ")) + 1
		revVal := truncatePath(m.reviewer.Model, max(3, innerWidth-revPillW))
		sb.WriteString(m.styles.ReviewerPill.Render(" REVIEWER "))
		sb.WriteString(" ")
		sb.WriteString(m.styles.SidebarValue.Render(revVal))
		sb.WriteString("\n")
	}

	// ── PIPELINE METRICS ─────────────────────────────────────────
	if innerHeight >= 17 {
		sb.WriteString("\n")
		sb.WriteString(m.styles.SidebarHeader.Render(">> PIPELINE METRICS"))
		sb.WriteString("\n")
		sb.WriteString(rule)
		sb.WriteString("\n")

		// Retries meter — measure all parts precisely
		retryLabelStr := " Retries "
		retryLabel := m.styles.SidebarLabel.Render(retryLabelStr)
		retryLabelW := lipgloss.Width(retryLabel)
		retriesStr := fmt.Sprintf("%d/%d", m.retryCount, m.MaxRetries)
		retriesStrW := lipgloss.Width(retriesStr)
		retryMeterW := max(2, innerWidth-retryLabelW-retriesStrW-1)
		sb.WriteString(retryLabel)
		sb.WriteString(renderProgressBar(m.retryCount, m.MaxRetries, retryMeterW, m.styles))
		sb.WriteString(" ")
		sb.WriteString(m.styles.SidebarValue.Render(retriesStr))
		sb.WriteString("\n")

		// Turns meter
		turnLabelStr := " Turns   "
		turnLabel := m.styles.SidebarLabel.Render(turnLabelStr)
		turnLabelW := lipgloss.Width(turnLabel)
		turnsStr := fmt.Sprintf("%d/%d", m.plainTextTurns, MaxPlainTextTurns)
		turnsStrW := lipgloss.Width(turnsStr)
		turnMeterW := max(2, innerWidth-turnLabelW-turnsStrW-1)
		sb.WriteString(turnLabel)
		sb.WriteString(renderProgressBar(m.plainTextTurns, MaxPlainTextTurns, turnMeterW, m.styles))
		sb.WriteString(" ")
		sb.WriteString(m.styles.SidebarValue.Render(turnsStr))
		sb.WriteString("\n")
	}

	// ── CONTROLS ─────────────────────────────────────────────────
	if innerHeight >= 22 {
		sb.WriteString("\n")
		sb.WriteString(m.styles.SidebarHeader.Render(">> CONTROLS"))
		sb.WriteString("\n")
		sb.WriteString(rule)
		sb.WriteString("\n")

		enterKey := m.styles.TitleKeycapKey.Render("Enter")
		escKey := m.styles.TitleKeycapKey.Render("Esc")
		scrollKey := m.styles.TitleKeycapKey.Render("tt")
		submitLbl := m.styles.SidebarSubHeader.Render(" Send  ")
		quitLbl := m.styles.SidebarSubHeader.Render(" Quit")
		scrollLbl := m.styles.SidebarSubHeader.Render(" Scroll")

		row1 := lipgloss.JoinHorizontal(lipgloss.Top, enterKey, submitLbl, escKey, quitLbl)
		sb.WriteString(row1)
		sb.WriteString("\n")
		row2 := lipgloss.JoinHorizontal(lipgloss.Top, scrollKey, scrollLbl)
		sb.WriteString(row2)
	}

	clipped := clipLines(sb.String(), innerHeight)
	return m.styles.SidebarContainer.
		Width(innerWidth).
		Height(innerHeight).
		Render(clipped)
}

// renderPipelineDock builds a 4-step visual pipeline tracker:
// [1 Prompt] -> [2 Propose] -> [3 Review] -> [4 Exec]
func (m Model) renderPipelineDock(width int) string {
	type stepState int
	const (
		stepPending stepState = iota
		stepActive
		stepDone
	)

	step1, step2, step3, step4 := stepDone, stepPending, stepPending, stepPending
	if m.sessionState == loop.StateIdle {
		step1 = stepPending
	} else if m.sessionState == loop.StateActive {
		if m.activeToolCall != nil {
			step2, step3, step4 = stepDone, stepDone, stepActive
		} else if strings.Contains(m.statusMessage, "Reviewer") {
			step2, step3 = stepDone, stepActive
		} else {
			step2 = stepActive
		}
	}

	renderStep := func(n int, label string, state stepState) string {
		var icon string
		switch state {
		case stepDone:
			icon = "+"
		case stepActive:
			icon = m.spinner.View()
		default:
			icon = "o"
		}
		var text string
		if width < 90 {
			text = fmt.Sprintf(" %d%s ", n, icon)
		} else {
			text = fmt.Sprintf(" %d %s %s ", n, icon, label)
		}
		switch state {
		case stepActive:
			return m.styles.PipelineStepActive.Render(text)
		case stepDone:
			return m.styles.PipelineStepDone.Render(text)
		default:
			return m.styles.PipelineStepPending.Render(text)
		}
	}

	arrow := m.styles.PipelineArrow.Render(" > ")
	s1 := renderStep(1, "Prompt", step1)
	s2 := renderStep(2, "Propose", step2)
	s3 := renderStep(3, "Review", step3)
	s4 := renderStep(4, "Exec", step4)

	dockContent := lipgloss.JoinHorizontal(lipgloss.Top, s1, arrow, s2, arrow, s3, arrow, s4)

	prefix := ""
	if width >= 85 {
		prefix = m.styles.SidebarSubHeader.Render(" PIPELINE  ")
	}
	full := prefix + dockContent

	dockW := max(0, width-m.styles.PipelineDock.GetHorizontalFrameSize())
	res := m.styles.PipelineDock.Width(dockW).Render(truncateLine(full, width))
	return clipLines(res, 1)
}

// renderStatusBar renders the live status line with state icon and spinner.
func (m Model) renderStatusBar(width int) string {
	var icon string
	if m.sessionState == loop.StateActive {
		icon = m.spinner.View() + " "
	} else {
		icon = "* "
	}
	statusText := " " + icon + m.statusMessage
	truncated := truncateLine(statusText, max(10, width-2))
	contentW := max(0, width-m.styles.StatusBar.GetHorizontalFrameSize())
	res := m.styles.StatusBar.Width(contentW).Render(truncated)
	return clipLines(res, 1)
}

// renderInputBar renders the full-width bordered prompt dock.
func (m Model) renderInputBar(width int) string {
	pill := m.styles.InputPill.Render(" YOU > ")
	inputView := m.input.View()

	hintText := " [Enter]"
	if m.sessionState == loop.StateActive {
		hintText = " [Enter: Interject]"
	}
	hint := m.styles.InputHint.Render(hintText)

	content := lipgloss.JoinHorizontal(
		lipgloss.Center,
		pill,
		" ",
		inputView,
		hint,
	)

	containerW := max(0, width-m.styles.InputContainer.GetHorizontalFrameSize())
	res := m.styles.InputContainer.Width(containerW).Height(1).Render(content)
	return clipLines(res, 3)
}

// renderProposedAction formats a tool proposal card with syntax-highlighted args.
func (m Model) renderProposedAction(content string, width int) string {
	lines := strings.Split(content, "\n")
	var sb strings.Builder

	funcName := "action"
	if len(lines) > 0 && strings.HasPrefix(lines[0], "Proposed tool call:") {
		funcName = strings.TrimSpace(strings.TrimPrefix(lines[0], "Proposed tool call:"))
	}

	headerLabel := m.styles.ToolCallHeader.Render("  TOOL PROPOSAL  ")
	funcLabel := m.styles.ToolCallFunc.Render(" " + funcName + " ")
	sb.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, headerLabel, "  ", funcLabel))
	sb.WriteString("\n")
	sb.WriteString(m.styles.SidebarSubHeader.Render(strings.Repeat("-", max(1, width-m.styles.ToolCallBox.GetHorizontalFrameSize()-2))))
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

	boxW := max(0, width-m.styles.ToolCallBox.GetHorizontalFrameSize())
	return m.styles.ToolCallBox.Width(boxW).Render(strings.TrimRight(sb.String(), "\n"))
}

// renderTranscript formats all transcript entries for the viewport.
func (m Model) renderTranscript() string {
	entries := m.transcript.Entries()
	if len(entries) == 0 {
		return m.renderWelcomeScreen()
	}

	var sb strings.Builder
	for i, entry := range entries {
		ts := m.styles.Timestamp.Render(entry.Timestamp.Format("15:04"))

		var pill string
		var accentBar lipgloss.Style

		switch entry.Speaker {
		case transcript.SpeakerYou:
			pill = m.styles.YouPill.Render(" YOU ")
			accentBar = m.styles.YouMessageBar
		case transcript.SpeakerCoder:
			pill = m.styles.CoderPill.Render(" CODER ")
			accentBar = m.styles.CoderMessageBar
		case transcript.SpeakerReviewer:
			pill = m.styles.ReviewerPill.Render(" REVIEWER ")
			accentBar = m.styles.ReviewerMessageBar
		case transcript.SpeakerSystem:
			pill = m.styles.SystemPill.Render(" SYSTEM ")
			accentBar = m.styles.TitleKeycapKey
		default:
			pill = m.styles.SystemPill.Render(fmt.Sprintf(" %s ", entry.Speaker))
			accentBar = m.styles.TitleKeycapKey
		}

		entryHeader := lipgloss.JoinHorizontal(lipgloss.Top, pill, " ", ts)

		var body string
		switch entry.Type {
		case transcript.TypeProposedAction:
			body = m.renderProposedAction(entry.Content, m.viewport.Width())

		case transcript.TypeActionResult:
			resultIcon := m.styles.ToolCallFunc.Render(">")
			body = "  " + resultIcon + " " + m.styles.ActionResult.Render(entry.Content)

		default:
			content := entry.Content

			if strings.HasPrefix(content, "APPROVED") {
				badge := m.styles.ApprovedBadge.Render(" + APPROVED BY REVIEWER ")
				content = badge + "\n" + strings.TrimPrefix(content, "APPROVED")
			} else if strings.HasPrefix(content, "OBJECTION") {
				badge := m.styles.ObjectionBadge.Render(" ! OBJECTION BY REVIEWER ")
				content = badge + "\n" + strings.TrimPrefix(content, "OBJECTION")
			}

			if entry.Speaker == transcript.SpeakerYou {
				boxW := max(0, m.viewport.Width()-m.styles.UserCalloutBox.GetHorizontalFrameSize())
				body = m.styles.UserCalloutBox.Width(boxW).Render(content)
			} else {
				lines := strings.Split(content, "\n")
				var formatted []string
				for _, line := range lines {
					bar := accentBar.Render("|")
					formatted = append(formatted, "  "+bar+" "+formatMarkdownLine(line, m.styles))
				}
				body = strings.Join(formatted, "\n")
			}
		}

		sb.WriteString(entryHeader)
		sb.WriteString("\n")
		sb.WriteString(body)
		if i < len(entries)-1 {
			sb.WriteString("\n\n")
		}
	}
	return sb.String()
}

// renderWelcomeScreen renders the empty-session splash screen.
func (m Model) renderWelcomeScreen() string {
	var sb strings.Builder
	vw := m.viewport.Width()
	if vw < 10 {
		vw = 10
	}

	sb.WriteString("\n")
	sb.WriteString(m.styles.WelcomeTitle.Render("  TRIAD STUDIO  --  Dual-Agent Coding Engine"))
	sb.WriteString("\n")
	sb.WriteString(m.styles.SidebarSubHeader.Render("  " + strings.Repeat("-", max(10, vw-4))))
	sb.WriteString("\n\n")

	coderPill := m.styles.CoderPill.Render(" CODER ")
	reviewerPill := m.styles.ReviewerPill.Render(" REVIEWER ")

	sb.WriteString("  ")
	sb.WriteString(coderPill)
	sb.WriteString("  ")
	sb.WriteString(m.styles.WelcomeSub.Render("Proposes file edits, commands, and tool calls"))
	sb.WriteString("\n")
	sb.WriteString("  ")
	sb.WriteString(reviewerPill)
	sb.WriteString("  ")
	sb.WriteString(m.styles.WelcomeSub.Render("Inspects proposals & enforces safety veto gates"))
	sb.WriteString("\n\n")

	sb.WriteString("  ")
	sb.WriteString(m.styles.TitleKeycapKey.Render(" TIP "))
	sb.WriteString("  ")
	sb.WriteString(m.styles.WelcomeTip.Render("Type your task below and press "))
	sb.WriteString(m.styles.TitleKeycapKey.Render(" Enter "))
	sb.WriteString("\n")

	return sb.String()
}

// formatMarkdownLine applies basic inline styling for bullets and inline code.
func formatMarkdownLine(line string, styles Styles) string {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") || strings.HasPrefix(trimmed, "+ ") {
		rest := trimmed[2:]
		return styles.MdBullet.Render("  * ") + styles.EntryContent.Render(rest)
	}
	if strings.Contains(line, "`") {
		parts := strings.Split(line, "`")
		var out strings.Builder
		for i, p := range parts {
			if i%2 == 1 {
				out.WriteString(styles.MdInlineCode.Render(p))
			} else {
				out.WriteString(styles.EntryContent.Render(p))
			}
		}
		return out.String()
	}
	return styles.EntryContent.Render(line)
}

func truncatePath(path string, maxLen int) string {
	if maxLen <= 3 {
		return "..."
	}
	// Use lipgloss.Width to measure since path may contain non-ASCII
	if lipgloss.Width(path) <= maxLen {
		return path
	}
	runes := []rune(path)
	for len(runes) > 0 && lipgloss.Width("..."+string(runes)) > maxLen {
		runes = runes[1:]
	}
	return "..." + string(runes)
}

// renderProgressBar renders a progress meter using single-width block chars.
// Uses `#` for filled and `.` for empty — guaranteed 1-wide on all terminals.
func renderProgressBar(value int, maxVal int, totalBlocks int, styles Styles) string {
	if totalBlocks < 2 {
		totalBlocks = 2
	}
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
			sb.WriteString(styles.SidebarMeterFill.Render("#"))
		} else {
			sb.WriteString(styles.SidebarMeterEmpty.Render("."))
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

func padOrClipLine(line string, width int) string {
	w := lipgloss.Width(line)
	if w == width {
		return line
	}
	if w < width {
		return line + strings.Repeat(" ", width-w)
	}
	return truncateLine(line, width)
}

func fitToCanvas(s string, width int, height int) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, height)
	for i := 0; i < height; i++ {
		if i < len(lines) {
			out = append(out, padOrClipLine(lines[i], width))
		} else {
			out = append(out, strings.Repeat(" ", width))
		}
	}
	return strings.Join(out, "\n")
}

func truncateLine(s string, maxLen int) string {
	if maxLen <= 3 {
		return "..."
	}
	if lipgloss.Width(s) <= maxLen {
		return s
	}
	runes := []rune(s)
	for len(runes) > 0 && lipgloss.Width(string(runes))+3 > maxLen {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "..."
}
