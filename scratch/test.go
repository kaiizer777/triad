package main

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

func main() {
	style := lipgloss.NewStyle().Width(20)

	// Test 1: Long word without spaces
	longWord := strings.Repeat("A", 40)
	res1 := style.Render(longWord)
	fmt.Printf("Test 1 (Long Word):\nWidth: %d\nLines: %d\nContent:\n%s\n\n", lipgloss.Width(res1), len(strings.Split(res1, "\n")), res1)

	// Test 2: Words with spaces
	longSentence := "This is a very long sentence that definitely has spaces."
	res2 := style.Render(longSentence)
	fmt.Printf("Test 2 (Long Sentence):\nWidth: %d\nLines: %d\nContent:\n%s\n\n", lipgloss.Width(res2), len(strings.Split(res2, "\n")), res2)
}
