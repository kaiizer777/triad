package main

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

func truncatePath(path string, maxLen int) string {
	if maxLen <= 3 {
		return "..."
	}
	if lipgloss.Width(path) <= maxLen {
		return path
	}
	runes := []rune(path)
	for len(runes) > 0 && lipgloss.Width("..."+string(runes)) > maxLen {
		runes = runes[1:]
	}
	return "..." + string(runes)
}

func truncateLine(s string, maxW int) string {
	if lipgloss.Width(s) <= maxW {
		return s
	}
	return lipgloss.String(s).Truncate(maxW, "", lipgloss.Tail).String()
}

func main() {
	stateBadge := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#080C14")).Background(lipgloss.Color("#10B981")).Padding(0, 1).Render(" IDLE ")
	statsSummary := "0/1M (0.0%) $0.000"
	leftPart := stateBadge + "  " + lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#E2E8F0")).Render(statsSummary)
	leftW := lipgloss.Width(leftPart)

	modelName := "mimo-v2.5-pro"
	modeStr := "ORCHESTRATOR"
	workDir := "c:\\Users\\bari2\\Desktop\\triad"

	sep := lipgloss.NewStyle().Foreground(lipgloss.Color("#475569")).Render(" | ")
	modelPill := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#60A5FA")).Render(modelName)
	modePill := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#E2E8F0")).Background(lipgloss.Color("#6366F1")).Padding(0, 1).Render(" " + modeStr + " ")

	prefixRight := modelPill + sep + modePill
	prefixRightW := lipgloss.Width(prefixRight)

	containerW := 80
	availForDir := containerW - leftW - prefixRightW - 3
	var rightPart string
	if availForDir >= 6 {
		dirVal := truncatePath(workDir, availForDir)
		rightPart = prefixRight + sep + lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#E2E8F0")).Render(dirVal)
	}

	rightW := lipgloss.Width(rightPart)
	gapW := containerW - leftW - rightW
	if gapW < 1 {
		gapW = 1
	}

	row := lipgloss.JoinHorizontal(
		lipgloss.Top,
		leftPart,
		strings.Repeat(" ", gapW),
		rightPart,
	)

	fmt.Printf("Row len before truncate: %d\n", lipgloss.Width(row))
	truncated := truncateLine(row, containerW)
	fmt.Printf("Row len after truncate: %d\n", lipgloss.Width(truncated))
	
	sb := lipgloss.NewStyle().
		Padding(0, 1).
		Foreground(lipgloss.Color("#E2E8F0")).
		Background(lipgloss.Color("#080C14")).
		Width(containerW).
		Render(truncated)
		
	fmt.Printf("Final:\n%s\n", sb)
}
