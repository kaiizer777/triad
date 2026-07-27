package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/kaiizer777/triad/internal/agent"
	"github.com/kaiizer777/triad/internal/journey"
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
	// The picker takes priority over the autocomplete popup
	// (typing /foo while the picker is open is suppressed anyway,
	// but visually the picker should win when both could render).
	if m.picker != nil {
		popup = m.renderPickerPopup(rightCardInnerWidth)
		if popup != "" {
			popupHeight = lipgloss.Height(popup)
		}
	} else if m.autocompleteActive && len(m.autocompleteCmds) > 0 {
		popup = m.renderAutocompletePopup(rightCardInnerWidth)
		if popup != "" {
			popupHeight = lipgloss.Height(popup)
		}
	}

	inputBar := m.renderInputBar(rightCardInnerWidth)

	// Fixed bottom rows that must ALWAYS be visible in right card:
	// Input Bar (measured dynamically) + optional Status Bar if no sidebar + optional Autocomplete Popup height when active.
	bottomRows := lipgloss.Height(inputBar)
	if sidebarWidth == 0 {
		bottomRows += 1
	}
	vpContentHeight := max(1, rightCardInnerHeight-bottomRows-popupHeight)
	vpContentWidth := max(1, rightCardInnerWidth-m.styles.ViewportContainer.GetHorizontalFrameSize())

	m.viewport.SetWidth(vpContentWidth)
	m.viewport.SetHeight(vpContentHeight)

	vpView := clipLines(m.viewport.View(), vpContentHeight)
	vpContainer := m.styles.ViewportContainer.
		Width(vpContentWidth).
		Height(vpContentHeight).
		Render(vpView)

	// Clip ONLY the scrollable viewport portion — never the pinned bottom dock.
	scrollableArea := clipLines(vpContainer, vpContentHeight)

	// Skill editor swap: when m.skillEditor is non-nil, the
	// right panel's scrollable area becomes the inline
	// textarea instead of the conversation viewport. The
	// input bar still docks at the bottom (so Esc / Ctrl-S
	// work via the same keystroke pipeline), and the
	// textarea is sized to fit the available height.
	//
	// Work.md §3.3 specifies "inline TUI editing pane (not
	// shelling out to an external editor)"; the in-place
	// swap is the simplest expression of that constraint.
	if m.skillEditor != nil {
		editorStr := renderSkillEditor(m.skillEditor, vpContentWidth)
		// Pad / clip to vpContentHeight so the bottom dock
		// doesn't shift when the editor opens. We use
		// clipLines on the rendered string to enforce the
		// exact line count.
		scrollableArea = clipLines(editorStr, vpContentHeight)
	}

	// Pin bottom: (statusBar if no sidebar) + (popup if active) + input are always rendered last.
	var bottomDock string
	if sidebarWidth == 0 {
		statusBar := m.renderStatusBar(rightCardInnerWidth)
		if popup != "" {
			bottomDock = lipgloss.JoinVertical(
				lipgloss.Left,
				statusBar,
				popup,
				inputBar,
			)
		} else {
			bottomDock = lipgloss.JoinVertical(
				lipgloss.Left,
				statusBar,
				inputBar,
			)
		}
	} else {
		if popup != "" {
			bottomDock = lipgloss.JoinVertical(
				lipgloss.Left,
				popup,
				inputBar,
			)
		} else {
			bottomDock = inputBar
		}
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
	// Bubble Tea v2 is declarative: alt screen and mouse mode live on the
	// View, not as NewProgram options. The runtime's diff-based renderer
	// (cursed_renderer.go: shouldUpdateAltScreen) compares this view's
	// AltScreen to the previous frame's and writes the enter/exit sequences
	// accordingly — including on the final frame after a graceful Quit.
	v := tea.NewView(formattedContent)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeNone
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
	statsSummary := m.renderStatsSummary()
	sessFile := truncatePath(m.transcript.FilePath(), max(5, centerStyleW-lipgloss.Width(stateStr)-lipgloss.Width(statsSummary)-6))
	centerContent := " " + sessFile + "  " + stateStr + "  " + statsSummary + " "
	center := m.styles.TitleCenter.Width(centerStyleW).Render(truncateLine(centerContent, centerStyleW))

	res := lipgloss.JoinHorizontal(lipgloss.Top, left, center, right)
	return clipLines(res, 1)
}

func formatCompactTokens(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 1000000 {
		val := float64(n) / 1000.0
		if val == float64(int(val)) {
			return fmt.Sprintf("%dk", int(val))
		}
		return fmt.Sprintf("%.1fk", val)
	}
	val := float64(n) / 1000000.0
	if val == float64(int(val)) {
		return fmt.Sprintf("%dM", int(val))
	}
	return fmt.Sprintf("%.1fM", val)
}

func (m Model) renderStatsSummary() string {
	winSize := m.coder.ContextWindow
	if winSize <= 0 {
		winSize = agent.DefaultContextWindow
	}

	usedTokens := m.stats.LastPromptTokens
	fillPct := 0.0
	if usedTokens > 0 && winSize > 0 {
		fillPct = (float64(usedTokens) / float64(winSize)) * 100.0
	}

	cost := (float64(m.stats.Coder.PromptTokens) * m.coder.InputCostPerToken) +
		(float64(m.stats.Coder.CompletionTokens) * m.coder.OutputCostPerToken) +
		(float64(m.stats.Reviewer.PromptTokens) * m.reviewer.InputCostPerToken) +
		(float64(m.stats.Reviewer.CompletionTokens) * m.reviewer.OutputCostPerToken)

	contextUsageStr := fmt.Sprintf("%s/%s", formatCompactTokens(usedTokens), formatCompactTokens(winSize))
	pctStr := fmt.Sprintf("(%.1f%%)", fillPct)
	costStr := fmt.Sprintf("$%.3f", cost)

	return fmt.Sprintf("%s %s %s", contextUsageStr, pctStr, costStr)
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

	if m.showJourney {
		return m.renderJourneySidebar(width, height, innerWidth, innerHeight, rule)
	}

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

	// Mode badge
	modeLabel := m.styles.SidebarLabel.Render(" Mode   ")
	modeVal := strings.ToUpper(string(m.currentMode))
	if modeVal == "" {
		modeVal = "ORCHESTRATOR"
	}
	sb.WriteString(modeLabel)
	sb.WriteString(m.styles.SidebarBadgeThink.Render(" " + modeVal + " "))
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
	// Only show the dual-agent section when the mode actually uses
	// a Reviewer (triad). Orchestrator and General modes are
	// single-agent paths where the Reviewer pill would be misleading.
	if innerHeight >= 11 && m.currentMode == loop.ModeTriad {
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
	} else if innerHeight >= 11 {
		// Single-agent modes: show only the Coder.
		sb.WriteString("\n")
		sb.WriteString(m.styles.SidebarHeader.Render("▸ AGENT ENGINE"))
		sb.WriteString("\n")
		sb.WriteString(rule)
		sb.WriteString("\n")

		coderPillW := lipgloss.Width(m.styles.CoderPill.Render(" CODER ")) + 1
		coderVal := truncatePath(m.coder.Model, max(3, innerWidth-coderPillW))
		sb.WriteString(m.styles.CoderPill.Render(" CODER "))
		sb.WriteString(" ")
		sb.WriteString(m.styles.SidebarValue.Render(coderVal))
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

	// ── SYSTEM LOGS (Scrollable Viewport in Remaining Area of Left Card) ────
	header := m.styles.SidebarHeader.Render("▸ SYSTEM LOGS")
	sysHeaderLines := []string{
		header,
		rule,
	}

	topLines := strings.Split(sb.String(), "\n")
	if len(topLines) > 0 && topLines[len(topLines)-1] == "" {
		topLines = topLines[:len(topLines)-1]
	}

	availSysHeight := max(3, innerHeight-len(topLines)-len(sysHeaderLines)-1)
	m.sysViewport.SetWidth(innerWidth)
	m.sysViewport.SetHeight(availSysHeight)
	m.sysViewport.SetContent(m.renderSystemLogs())

	sysView := clipLines(m.sysViewport.View(), availSysHeight)
	sysLines := strings.Split(sysView, "\n")

	var finalLines []string
	finalLines = append(finalLines, topLines...)
	finalLines = append(finalLines, "")
	finalLines = append(finalLines, sysHeaderLines...)
	finalLines = append(finalLines, sysLines...)

	if len(finalLines) > innerHeight {
		finalLines = finalLines[:innerHeight]
	} else {
		for len(finalLines) < innerHeight {
			finalLines = append(finalLines, "")
		}
	}
	padded := strings.Join(finalLines, "\n")

	return m.styles.SidebarContainer.
		Width(width).
		Height(height).
		Render(padded)
}

// renderSystemLogs formats all system entries (SpeakerSystem) for the sidebar viewport.
func (m Model) renderSystemLogs() string {
	allEntries := m.transcript.Entries()
	var sysEntries []transcript.Entry
	for _, entry := range allEntries {
		if entry.Speaker == transcript.SpeakerSystem {
			sysEntries = append(sysEntries, entry)
		}
	}

	if len(sysEntries) == 0 {
		return m.styles.WelcomeSub.Render("  No system events recorded.")
	}

	var sb strings.Builder
	vw := max(10, m.sysViewport.Width())

	for i, entry := range sysEntries {
		ts := m.styles.Timestamp.Render(entry.Timestamp.Format("15:04"))
		pill := m.styles.SystemPill.Render(" SYS ")
		header := lipgloss.JoinHorizontal(lipgloss.Top, pill, " ", ts)
		sb.WriteString(header)
		sb.WriteString("\n")

		cleanContent := strings.ReplaceAll(entry.Content, "\t", "    ")
		cleanContent = strings.ReplaceAll(cleanContent, "\r", "")

		lines := strings.Split(cleanContent, "\n")
		for _, line := range lines {
			wrapped := wrapText(line, max(1, vw-4))
			for _, wl := range wrapped {
				bar := m.styles.TitleKeycapKey.Render("▌")
				sb.WriteString(" " + bar + " " + m.styles.EntryContent.Render(wl))
				sb.WriteString("\n")
			}
		}

		if i < len(sysEntries)-1 {
			divW := max(4, vw-2)
			sb.WriteString(" " + m.styles.EntryDivider.Render(strings.Repeat("·", divW)))
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

// renderJourneySidebar renders the Commit Journey view filling the entire left card.
func (m Model) renderJourneySidebar(width, height, innerWidth, innerHeight int, rule string) string {
	var sb strings.Builder

	sb.WriteString(m.styles.SidebarHeader.Render("▸ COMMIT JOURNEY"))
	sb.WriteString("\n")
	sb.WriteString(rule)
	jEntries := m.journeyEntries

	mainCount := 0
	twinCount := 0
	for _, e := range jEntries {
		if e.Type == journey.CommitTypeTwin {
			twinCount++
		} else {
			mainCount++
		}
	}

	statsStr := fmt.Sprintf("%d Commits (%d Main · %d Twin)", len(jEntries), mainCount, twinCount)
	sb.WriteString(m.styles.SidebarValue.Render(truncateLine(statsStr, innerWidth)))
	sb.WriteString("\n")
	sb.WriteString(rule)

	headerText := sb.String()
	headerLines := strings.Split(strings.TrimRight(headerText, "\n"), "\n")

	footerHint := m.styles.SidebarSubHeader.Render("▸ [/journey toggle]")
	footerLines := []string{rule, footerHint}

	availVpHeight := max(3, innerHeight-len(headerLines)-len(footerLines))

	m.journeyViewport.SetWidth(innerWidth)
	m.journeyViewport.SetHeight(availVpHeight)
	m.journeyViewport.SetContent(journey.RenderSidebarTimeline(jEntries, innerWidth))

	vpView := clipLines(m.journeyViewport.View(), availVpHeight)
	sysLines := strings.Split(vpView, "\n")

	var finalLines []string
	finalLines = append(finalLines, headerLines...)
	finalLines = append(finalLines, sysLines...)
	finalLines = append(finalLines, footerLines...)

	if len(finalLines) > innerHeight {
		finalLines = finalLines[:innerHeight]
	} else {
		for len(finalLines) < innerHeight {
			finalLines = append(finalLines, "")
		}
	}

	padded := strings.Join(finalLines, "\n")
	return m.styles.SidebarContainer.
		Width(width).
		Height(height).
		Render(padded)
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

	var hint string
	if m.sessionState == loop.StateActive {
		hint = lipgloss.JoinHorizontal(lipgloss.Top,
			m.styles.TitleKeycapKey.Render("Enter"),
			m.styles.TitleKeycapLabel.Render(" Interject"),
		)
	} else {
		hint = lipgloss.JoinHorizontal(lipgloss.Top,
			m.styles.TitleKeycapKey.Render("Enter"),
			m.styles.TitleKeycapLabel.Render(" Submit"),
		)
	}

	containerW := max(0, width-m.styles.InputContainer.GetHorizontalFrameSize())
	pillW := lipgloss.Width(pill)
	hintW := lipgloss.Width(hint)

	// Reserve 6 chars safety buffer to prevent soft-wrapping under any window size
	inputW := max(10, containerW-pillW-hintW-6)
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

// renderProposedPlan formats a structured plan snapshot as a compact checklist
// card. The transcript stores snapshots as JSON, so malformed or legacy plan
// entries deliberately fall back to readable text instead of breaking View.
func (m Model) renderProposedPlan(content string, width int) string {
	plan, err := transcript.DecodePlan(content)
	if err != nil {
		return m.renderProposedAction(content, width)
	}

	boxW := max(0, width-m.styles.ToolCallBox.GetHorizontalFrameSize())
	innerW := max(1, width-m.styles.ToolCallBox.GetHorizontalFrameSize()-2)

	header := " ▸ PLAN "
	if plan.Revision > 1 {
		header = fmt.Sprintf(" ▸ PLAN (revised from initial · #%d) ", plan.Revision)
	}

	done := 0
	for _, item := range plan.Items {
		if item.Status == transcript.PlanItemDone {
			done++
		}
	}

	var sb strings.Builder
	sb.WriteString(m.styles.ToolCallHeader.Render(header))
	sb.WriteString("\n")
	sb.WriteString(m.styles.SidebarRule.Render(strings.Repeat("─", innerW)))
	sb.WriteString("\n")
	sb.WriteString(m.styles.ToolCallFunc.Render(fmt.Sprintf(" %d/%d done ", done, len(plan.Items))))

	for _, item := range plan.Items {
		icon := "▢"
		switch item.Status {
		case transcript.PlanItemInProgress:
			icon = "▷"
		case transcript.PlanItemDone:
			icon = "✓"
		}

		text := strings.TrimSpace(item.Text)
		if text == "" {
			text = "Untitled item"
		}
		for _, line := range wrapText(fmt.Sprintf(" %s %d. %s", icon, item.ID, text), innerW) {
			sb.WriteString("\n")
			sb.WriteString(m.styles.ToolCallVal.Render(line))
		}
	}

	return m.styles.ToolCallBox.Width(boxW).Render(sb.String())
}

// renderTranscript formats all non-system transcript entries for the main chat viewport.
func (m Model) renderTranscript() string {
	allEntries := m.transcript.Entries()
	var entries []transcript.Entry
	for _, entry := range allEntries {
		// Plan snapshots are written by the system so they remain a
		// trustworthy record, but they are user-facing cards and must
		// remain visible in the main transcript. Other system events
		// continue to live only in the sidebar log.
		if entry.Speaker != transcript.SpeakerSystem || entry.Type == transcript.TypeProposedPlan {
			entries = append(entries, entry)
		}
	}

	if len(entries) == 0 {
		return m.renderWelcomeScreen()
	}

	var sb strings.Builder
	for i, entry := range entries {
		ts := m.styles.Timestamp.Render(entry.Timestamp.Format("15:04"))

		cleanContent := strings.ReplaceAll(entry.Content, "\t", "    ")
		cleanContent = strings.ReplaceAll(cleanContent, "\r", "")

		var pill string

		switch entry.Speaker {
		case transcript.SpeakerYou:
			pill = m.styles.YouPill.Render(" YOU ")
		case transcript.SpeakerCoder:
			pill = m.styles.CoderPill.Render(" CODER ")
		case transcript.SpeakerReviewer:
			pill = m.styles.ReviewerPill.Render(" REVIEWER ")
		case transcript.SpeakerSystem:
			pill = m.styles.SystemPill.Render(" SYSTEM ")
		default:
			pill = m.styles.SystemPill.Render(fmt.Sprintf(" %s ", entry.Speaker))
		}

		entryHeader := lipgloss.JoinHorizontal(lipgloss.Top, pill, "  ", ts)

		var body string
		switch entry.Type {
		case transcript.TypeProposedAction:
			body = m.renderProposedAction(cleanContent, m.viewport.Width())

		case transcript.TypeProposedPlan:
			body = m.renderProposedPlan(cleanContent, m.viewport.Width())

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

			var boxStyle lipgloss.Style
			switch entry.Speaker {
			case transcript.SpeakerYou:
				boxStyle = m.styles.UserCalloutBox
			case transcript.SpeakerCoder:
				boxStyle = m.styles.CoderCalloutBox
			case transcript.SpeakerReviewer:
				boxStyle = m.styles.ReviewerCalloutBox
			default:
				boxStyle = m.styles.UserCalloutBox
			}

			boxW := max(0, m.viewport.Width()-boxStyle.GetHorizontalFrameSize())
			lines := strings.Split(content, "\n")
			var formattedLines []string
			for _, line := range lines {
				styledLine := formatMarkdownLine(line, m.styles)
				wrappedLines := wrapText(styledLine, max(1, boxW))
				formattedLines = append(formattedLines, wrappedLines...)
			}
			body = boxStyle.Width(boxW).Render(strings.Join(formattedLines, "\n"))
		}

		sb.WriteString(entryHeader)
		sb.WriteString("\n")
		sb.WriteString(body)
		if i < len(entries)-1 {
			sb.WriteString("\n\n")
			divW := max(4, m.viewport.Width()-4)
			sb.WriteString("  " + m.styles.EntryDivider.Render(strings.Repeat("·", divW)))
			sb.WriteString("\n\n")
		} else {
			sb.WriteString("\n\n\n")
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

	divLine := m.styles.SidebarRule.Render(strings.Repeat("━", max(10, vw-2)))

	// ── Hero banner ──────────────────────────────────────────────
	sb.WriteString("\n")
	sb.WriteString(divLine)
	sb.WriteString("\n")
	sb.WriteString(m.styles.WelcomeTitle.Render("  ◈  TRIAD STUDIO  "))
	sb.WriteString(m.styles.WelcomeSub.Render("Dual-Agent Coding Engine"))
	sb.WriteString("\n")
	sb.WriteString(divLine)
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

	caps := []struct{ icon, text string }{
		{"◆", "Write, edit & refactor code across your entire codebase"},
		{"◆", "Run shell commands, builds, and test suites"},
		{"◆", "Debug, trace errors, and propose targeted fixes"},
		{"◆", "Plan multi-step features with Reviewer safety gates"},
	}
	for _, c := range caps {
		sb.WriteString("  ")
		sb.WriteString(m.styles.MdBullet.Render(" " + c.icon + " "))
		sb.WriteString(m.styles.WelcomeSub.Render(c.text))
		sb.WriteString("\n")
	}

	sb.WriteString("\n")
	sb.WriteString(m.styles.SidebarRule.Render("  " + strings.Repeat("─", max(10, vw-4))))
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
	if strings.Contains(line, "\x1b[") {
		return line
	}
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
