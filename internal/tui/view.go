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

	// 1. Header (1 line)
	header := m.renderTitleBar(width)

	// Available height for middle content body (below header)
	bodyHeight := height - 1
	if bodyHeight < 1 {
		bodyHeight = 1
	}

	sidebarWidth := 0
	if width >= 75 {
		sidebarWidth = 32
		if width >= 120 {
			sidebarWidth = 36
		} else if width < 95 {
			sidebarWidth = 28
		}
	}

	mainContainerWidth := width - sidebarWidth
	if mainContainerWidth < 10 {
		mainContainerWidth = 10
	}

	rightCardHorizFrame := m.styles.RightCardContainer.GetHorizontalFrameSize()
	rightCardVertFrame := m.styles.RightCardContainer.GetVerticalFrameSize()

	rightCardInnerWidth := max(1, mainContainerWidth-rightCardHorizFrame)
	rightCardInnerHeight := max(1, bodyHeight-rightCardVertFrame)

	var popup string
	popupHeight := 0
	if m.autocompleteActive && len(m.autocompleteCmds) > 0 {
		popup = m.renderAutocompletePopup(rightCardInnerWidth)
		if popup != "" {
			popupHeight = lipgloss.Height(popup)
		}
	}

	// Fixed bottom rows that must ALWAYS be visible:
	// Pipeline Dock (1) + Status Bar (1) + Input Separator (1) + Input Row (1) = 4 rows pinned at bottom.
	// Plus optional Autocomplete Popup height when active.
	const bottomRows = 4
	vpContentHeight := max(1, rightCardInnerHeight-bottomRows-popupHeight)
	vpContentWidth := max(1, rightCardInnerWidth-m.styles.ViewportContainer.GetHorizontalFrameSize())

	m.viewport.SetWidth(vpContentWidth)
	m.viewport.SetHeight(vpContentHeight)

	vpView := clipLines(m.viewport.View(), vpContentHeight)
	vpContainer := m.styles.ViewportContainer.
		Width(vpContentWidth).
		Height(vpContentHeight).
		Render(vpView)

	pipelineDock := m.renderPipelineDock(rightCardInnerWidth)
	statusBar := m.renderStatusBar(rightCardInnerWidth)
	inputBar := m.renderInputBar(rightCardInnerWidth)

	// Clip ONLY the scrollable viewport portion — never the pinned bottom dock.
	scrollableArea := clipLines(vpContainer, vpContentHeight)

	// Pin bottom: pipeline + status + (popup if active) + input are always rendered last, never clipped.
	var bottomDock string
	if popup != "" {
		bottomDock = lipgloss.JoinVertical(
			lipgloss.Left,
			pipelineDock,
			statusBar,
			popup,
			inputBar,
		)
	} else {
		bottomDock = lipgloss.JoinVertical(
			lipgloss.Left,
			pipelineDock,
			statusBar,
			inputBar,
		)
	}

	rightCardContent := lipgloss.JoinVertical(
		lipgloss.Left,
		scrollableArea,
		bottomDock,
	)

	rightCard := m.styles.RightCardContainer.
		Width(mainContainerWidth).
		Height(bodyHeight).
		Render(rightCardContent)

	var middlePanel string
	if sidebarWidth > 0 {
		sidebar := m.renderSidebar(sidebarWidth, bodyHeight)
		middlePanel = lipgloss.JoinHorizontal(lipgloss.Top, sidebar, rightCard)
	} else {
		middlePanel = rightCard
	}

	content := lipgloss.JoinVertical(
		lipgloss.Left,
		header,
		middlePanel,
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

	rule := m.styles.SidebarRule.Render(strings.Repeat("─", innerWidth))

	var sb strings.Builder

	sb.WriteString(m.styles.SidebarHeader.Render("▸ SESSION OVERVIEW"))
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
		sb.WriteString(m.styles.SidebarHeader.Render("▸ DUAL AGENT ENGINE"))
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
		sb.WriteString(m.styles.SidebarHeader.Render("▸ PIPELINE METRICS"))
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
		sb.WriteString(m.styles.SidebarHeader.Render("▸ CONTROLS"))
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

	lines := strings.Split(sb.String(), "\n")
	if len(lines) > innerHeight {
		lines = lines[:innerHeight]
	} else {
		for len(lines) < innerHeight {
			lines = append(lines, "")
		}
	}
	padded := strings.Join(lines, "\n")

	return m.styles.SidebarContainer.
		Width(width).
		Height(height).
		Render(padded)
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
			icon = "✓"
		case stepActive:
			icon = "●"
		default:
			icon = "○"
		}
		var text string
		if width < 85 {
			text = fmt.Sprintf(" %s%d ", icon, n)
		} else {
			text = fmt.Sprintf(" %s %d %s ", icon, n, label)
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

	arrow := m.styles.PipelineArrow.Render(" ❯ ")
	s1 := renderStep(1, "Prompt", step1)
	s2 := renderStep(2, "Propose", step2)
	s3 := renderStep(3, "Review", step3)
	s4 := renderStep(4, "Exec", step4)

	dockContent := lipgloss.JoinHorizontal(lipgloss.Top, s1, arrow, s2, arrow, s3, arrow, s4)

	prefix := ""
	if width >= 90 {
		prefix = m.styles.SidebarSubHeader.Render(" PIPELINE  ")
	}
	leftFull := prefix + dockContent

	var stateBadge string
	switch {
	case m.sessionState == loop.StateIdle:
		stateBadge = m.styles.SidebarBadgeIdle.Render(" IDLE ")
	case m.activeToolCall != nil:
		stateBadge = m.styles.SidebarBadgeActive.Render(" EXEC ")
	default:
		stateBadge = m.styles.SidebarBadgeThink.Render(" THINK ")
	}

	containerW := max(0, width-m.styles.PipelineDock.GetHorizontalFrameSize())

	leftW := lipgloss.Width(leftFull)
	badgeW := lipgloss.Width(stateBadge)
	gapW := containerW - leftW - badgeW

	var fullLine string
	if gapW > 1 && width >= 60 {
		fullLine = leftFull + strings.Repeat(" ", gapW) + stateBadge
	} else {
		fullLine = leftFull
	}

	res := m.styles.PipelineDock.Width(containerW).Render(truncateLine(fullLine, containerW))
	return clipLines(res, 1)
}

// renderStatusBar renders the live status line with state icon and spinner.
func (m Model) renderStatusBar(width int) string {
	var icon string
	switch {
	case m.sessionState == loop.StateIdle:
		icon = m.styles.SidebarBadgeIdle.Render(" ● ")
	case m.activeToolCall != nil:
		icon = m.styles.SidebarBadgeActive.Render(" ⚡ ") + " " + m.spinner.View()
	default:
		icon = m.styles.SidebarBadgeThink.Render(" ✦ ") + " " + m.spinner.View()
	}
	statusText := icon + "  " + m.statusMessage
	containerW := max(0, width-m.styles.StatusBar.GetHorizontalFrameSize())
	truncated := truncateLine(statusText, containerW)
	res := m.styles.StatusBar.Width(containerW).Render(truncated)
	return clipLines(res, 1)
}

// renderInputBar renders the prompt dock inside the right card.
// It is always pinned at the bottom and never clipped out of view.
func (m Model) renderInputBar(width int) string {
	// Separator line above the input bar for visual grounding
	sepW := max(0, width)
	sepLine := m.styles.InputSeparator.Render(strings.Repeat("─", max(1, sepW)))

	pill := m.styles.InputPill.Render(" ❯ YOU ")

	// Use compact hint symbols to prevent truncation on narrower terminals.
	var hintText string
	if m.sessionState == loop.StateActive {
		hintText = " ↵ "
	} else {
		hintText = " ↵ "
	}
	hint := m.styles.TitleKeycapKey.Render(hintText)

	containerW := max(0, width-m.styles.InputContainer.GetHorizontalFrameSize())
	pillW := lipgloss.Width(pill)
	hintW := lipgloss.Width(hint)

	// Reserve 8 chars (2 spaces + prompt/cursor width + safety buffer) to prevent soft-wrapping.
	inputW := max(10, containerW-pillW-hintW-8)
	m.input.SetWidth(inputW)

	inputView := m.input.View()

	content := lipgloss.JoinHorizontal(
		lipgloss.Center,
		pill,
		" ",
		inputView,
		" ",
		hint,
	)

	row := m.styles.InputContainer.Width(containerW).Render(content)
	return lipgloss.JoinVertical(lipgloss.Left, sepLine, row)
}

// renderProposedAction formats a tool proposal card with syntax-highlighted args.
func (m Model) renderProposedAction(content string, width int) string {
	lines := strings.Split(content, "\n")
	var sb strings.Builder

	funcName := "action"
	if len(lines) > 0 && strings.HasPrefix(lines[0], "Proposed tool call:") {
		funcName = strings.TrimSpace(strings.TrimPrefix(lines[0], "Proposed tool call:"))
	}

	headerLabel := m.styles.ToolCallHeader.Render(" ⚙ TOOL CALL ")
	funcLabel := m.styles.ToolCallFunc.Render(" ❯ " + funcName + " ")
	sb.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, headerLabel, "  ", funcLabel))
	sb.WriteString("\n")
	sb.WriteString(m.styles.SidebarRule.Render(strings.Repeat("─", max(1, width-m.styles.ToolCallBox.GetHorizontalFrameSize()-2))))
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
		var formattedLine string
		if idx := strings.Index(line, ":"); idx != -1 && strings.Contains(line[:idx], `"`) {
			key := line[:idx+1]
			val := line[idx+1:]
			formattedLine = "  " + m.styles.ToolCallKey.Render(key) + " "
			if strings.Contains(val, `"`) {
				formattedLine += m.styles.ToolCallVal.Render(val)
			} else {
				formattedLine += m.styles.ToolCallNum.Render(val)
			}
		} else {
			formattedLine = "  " + m.styles.ToolCallVal.Render(line)
		}
		
		availW := max(1, width-m.styles.ToolCallBox.GetHorizontalFrameSize()-2)
		wrappedLines := wrapText(formattedLine, availW)
		for _, wl := range wrappedLines {
			sb.WriteString(wl)
			sb.WriteString("\n")
		}
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

		cleanContent := strings.ReplaceAll(entry.Content, "\t", "    ")
		cleanContent = strings.ReplaceAll(cleanContent, "\r", "")

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

		entryHeader := lipgloss.JoinHorizontal(lipgloss.Top, pill, "  ", ts)

		var body string
		switch entry.Type {
		case transcript.TypeProposedAction:
			body = m.renderProposedAction(cleanContent, m.viewport.Width())

		case transcript.TypeActionResult:
			availW := max(1, m.viewport.Width()-6)
			wrapped := wrapText(cleanContent, availW)
			var formatted []string
			for _, wl := range wrapped {
				formatted = append(formatted, "  "+m.styles.ActionResultBar.Render("▌")+" "+m.styles.ActionResult.Render(wl))
			}
			body = strings.Join(formatted, "\n")

		default:
			content := cleanContent

			if strings.HasPrefix(content, "APPROVED") {
				badge := m.styles.ApprovedBadge.Render(" + APPROVED BY REVIEWER ")
				content = badge + "\n" + strings.TrimPrefix(content, "APPROVED")
			} else if strings.HasPrefix(content, "OBJECTION") {
				badge := m.styles.ObjectionBadge.Render(" ! OBJECTION BY REVIEWER ")
				content = badge + "\n" + strings.TrimPrefix(content, "OBJECTION")
			}

			if entry.Speaker == transcript.SpeakerYou {
				boxW := max(0, m.viewport.Width()-m.styles.UserCalloutBox.GetHorizontalFrameSize())
				wrapped := wrapText(content, boxW)
				body = m.styles.UserCalloutBox.Width(boxW).Render(strings.Join(wrapped, "\n"))
			} else {
				lines := strings.Split(content, "\n")
				var formatted []string
				for _, line := range lines {
					styledLine := formatMarkdownLine(line, m.styles)
					wrappedLines := wrapText(styledLine, max(1, m.viewport.Width()-8))
					for _, wl := range wrappedLines {
						bar := accentBar.Render("▌")
						formatted = append(formatted, "  "+bar+" "+wl)
					}
				}
				body = strings.Join(formatted, "\n")
			}
		}

		sb.WriteString(entryHeader)
		sb.WriteString("\n")
		sb.WriteString(body)
		if i < len(entries)-1 {
			sb.WriteString("\n")
			divW := max(4, m.viewport.Width()-4)
			sb.WriteString("  " + m.styles.EntryDivider.Render(strings.Repeat("·", divW)))
			sb.WriteString("\n")
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

	// ── Hero banner ──────────────────────────────────────────────
	sb.WriteString("\n")
	sb.WriteString(m.styles.WelcomeTitle.Render("  ◈  TRIAD STUDIO"))
	sb.WriteString("  ")
	sb.WriteString(m.styles.WelcomeSub.Render("Dual-Agent Coding Engine"))
	sb.WriteString("\n")
	sb.WriteString(m.styles.SidebarRule.Render("  " + strings.Repeat("─", max(10, vw-4))))
	sb.WriteString("\n\n")

	// ── Agent descriptions ────────────────────────────────────────
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

	// ── Capability hints ──────────────────────────────────────────
	sb.WriteString(m.styles.SidebarRule.Render("  " + strings.Repeat("─", max(10, vw-4))))
	sb.WriteString("\n")

	caps := []string{
		"Write, edit & refactor code across your entire codebase",
		"Run shell commands, builds, and test suites",
		"Debug, trace errors, and propose targeted fixes",
		"Plan multi-step features with Reviewer safety gates",
	}
	for _, c := range caps {
		sb.WriteString("  ")
		sb.WriteString(m.styles.MdBullet.Render(" ▸ "))
		sb.WriteString(m.styles.WelcomeSub.Render(c))
		sb.WriteString("\n")
	}

	sb.WriteString("\n")
	sb.WriteString("  ")
	sb.WriteString(m.styles.TitleKeycapKey.Render(" TIP "))
	sb.WriteString("  ")
	sb.WriteString(m.styles.WelcomeTip.Render("Describe your task below and press "))
	sb.WriteString(m.styles.TitleKeycapKey.Render(" Enter "))
	sb.WriteString(m.styles.WelcomeTip.Render(" to begin."))
	sb.WriteString("\n")

	return sb.String()
}

// formatMarkdownLine applies basic inline styling for bullets and inline code.
func formatMarkdownLine(line string, styles Styles) string {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") || strings.HasPrefix(trimmed, "+ ") {
		rest := trimmed[2:]
		return styles.MdBullet.Render(" ▸ ") + styles.EntryContent.Render(rest)
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

	var sb strings.Builder
	visibleW := 0
	targetW := maxLen - 3
	inAnsi := false
	var ansiBuf strings.Builder

	for _, r := range s {
		if r == '\x1b' {
			inAnsi = true
			ansiBuf.WriteRune(r)
			continue
		}
		if inAnsi {
			ansiBuf.WriteRune(r)
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inAnsi = false
				sb.WriteString(ansiBuf.String())
				ansiBuf.Reset()
			}
			continue
		}

		rw := lipgloss.Width(string(r))
		if visibleW+rw > targetW {
			break
		}
		sb.WriteRune(r)
		visibleW += rw
	}

	sb.WriteString("...")
	sb.WriteString("\x1b[0m")
	return sb.String()
}

// renderAutocompletePopup renders a filtered dropdown of matching slash commands.
func (m Model) renderAutocompletePopup(width int) string {
	if !m.autocompleteActive || len(m.autocompleteCmds) == 0 {
		return ""
	}

	frameHoriz := m.styles.AutocompleteBox.GetHorizontalFrameSize()
	innerWidth := max(10, width-frameHoriz)

	var sb strings.Builder
	header := m.styles.AutocompleteHeader.Render("▸ COMMAND SUGGESTIONS")
	sb.WriteString(header)
	sb.WriteString("\n")

	maxVisible := 8
	allCmds := m.autocompleteCmds
	startIdx := 0
	if len(allCmds) > maxVisible {
		startIdx = m.autocompleteIndex - maxVisible/2
		if startIdx < 0 {
			startIdx = 0
		}
		if startIdx+maxVisible > len(allCmds) {
			startIdx = len(allCmds) - maxVisible
		}
	}
	endIdx := startIdx + maxVisible
	if endIdx > len(allCmds) {
		endIdx = len(allCmds)
	}
	cmds := allCmds[startIdx:endIdx]

	for idx, cmd := range cmds {
		realIdx := startIdx + idx
		var line string
		nameStr := "/" + cmd.Name
		descStr := cmd.Description
		badgeStr := fmt.Sprintf("[%s]", cmd.Target)

		badge := m.styles.AutocompleteBadge.Render(badgeStr)
		badgeW := lipgloss.Width(badge)

		if realIdx == m.autocompleteIndex {
			prefix := "▸ "
			name := m.styles.AutocompleteItemSel.Render(nameStr)
			nameW := lipgloss.Width(nameStr) + 2
			descW := max(0, innerWidth-nameW-badgeW-3)
			desc := m.styles.AutocompleteDesc.Render(truncateLine(descStr, descW))

			line = lipgloss.JoinHorizontal(lipgloss.Top, prefix, name, " ", badge, " ", desc)
		} else {
			prefix := "  "
			name := m.styles.AutocompleteItem.Render(nameStr)
			nameW := lipgloss.Width(nameStr) + 2
			descW := max(0, innerWidth-nameW-badgeW-3)
			desc := m.styles.AutocompleteDesc.Render(truncateLine(descStr, descW))

			line = lipgloss.JoinHorizontal(lipgloss.Top, prefix, name, " ", badge, " ", desc)
		}
		sb.WriteString(truncateLine(line, innerWidth))
		if idx < len(cmds)-1 {
			sb.WriteString("\n")
		}
	}

	boxW := max(0, width-frameHoriz)
	return m.styles.AutocompleteBox.Width(boxW).Render(sb.String())
}

func wrapText(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}
	// Use Lipgloss to wrap text cleanly
	wrapped := lipgloss.NewStyle().Width(width).Render(text)
	return strings.Split(wrapped, "\n")
}
