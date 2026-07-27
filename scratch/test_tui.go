package main

import (
	"fmt"

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

func main() {
	stateBadge := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#080C14")).Background(lipgloss.Color("#10B981")).Padding(0, 1).Render(" IDLE ")
	statsSummary := "0/1M (0.0%) $0.000"
	leftPart := stateBadge + "  " + lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#E2E8F0")).Render(statsSummary)
	leftW := lipgloss.Width(leftPart)
	fmt.Printf("leftW: %d\n", leftW)

	modelName := "mimo-v2.5-pro"
	modeStr := "ORCHESTRATOR"
	workDir := "c:\\Users\\bari2\\Desktop\\triad"

	sep := lipgloss.NewStyle().Foreground(lipgloss.Color("#475569")).Render(" | ")
	modelPill := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#60A5FA")).Render(modelName)
	modePill := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#E2E8F0")).Background(lipgloss.Color("#6366F1")).Padding(0, 1).Render(" " + modeStr + " ")

	prefixRight := modelPill + sep + modePill
	prefixRightW := lipgloss.Width(prefixRight)
	fmt.Printf("prefixRightW: %d\n", prefixRightW)

	// Simulate different container widths
	for _, containerW := range []int{100, 80, 70, 60} {
		availForDir := containerW - leftW - prefixRightW - 3
		fmt.Printf("containerW: %d, availForDir: %d\n", containerW, availForDir)
		var rightPart string
		if availForDir >= 6 {
			dirVal := truncatePath(workDir, availForDir)
			fmt.Printf("dirVal: %q (width: %d)\n", dirVal, lipgloss.Width(dirVal))
			rightPart = prefixRight + sep + lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#E2E8F0")).Render(dirVal)
		} else if containerW-leftW >= prefixRightW {
			rightPart = prefixRight
		} else if containerW-leftW >= lipgloss.Width(modelPill) {
			rightPart = modelPill
		} else {
			rightPart = ""
		}
		rightW := lipgloss.Width(rightPart)
		gapW := containerW - leftW - rightW
		if gapW < 1 {
			gapW = 1
		}
		fmt.Printf("rightW: %d, gapW: %d, total: %d\n", rightW, gapW, leftW+gapW+rightW)
	}
}
