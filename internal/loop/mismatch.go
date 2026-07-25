package loop

import (
	"strings"
)

// CheckModeMismatch evaluates whether a forced mode (ModeGeneral or ModeTriad)
// looks mismatched for the given task description. Returns a gentle, non-blocking
// System note if a mismatch is flagged, or an empty string if no mismatch is detected
// or if mode is ModeOrchestrator.
func CheckModeMismatch(mode Mode, task string) string {
	if mode == ModeOrchestrator {
		return ""
	}

	trimmed := strings.TrimSpace(task)
	if trimmed == "" {
		return ""
	}

	lower := strings.ToLower(trimmed)
	words := strings.Fields(lower)

	switch mode {
	case ModeTriad:
		if isTrivialTask(lower, words) {
			return "[System]: Note — you're in Triad mode; this looks trivial, /mode general would skip the review overhead."
		}
	case ModeGeneral:
		if isComplexTask(lower, words) {
			return "[System]: Note — you're in General mode; this task looks complex/sensitive, /mode triad would provide Reviewer oversight."
		}
	}

	return ""
}

// isTrivialTask returns true if the task appears simple or informational.
func isTrivialTask(lower string, words []string) bool {
	// If task is clearly complex, it's not trivial.
	if containsComplexKeywords(lower) {
		return false
	}

	// Short word count or character length
	if len(words) <= 6 || len(lower) <= 40 {
		return true
	}

	// Simple informational / greeting keywords
	trivialKeywords := []string{
		"hi", "hello", "hey", "thanks", "thank you",
		"what is", "how do i", "explain", "who is",
		"typo", "fix typo", "ping", "test", "show", "tell me",
	}

	for _, kw := range trivialKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}

	return false
}

// isComplexTask returns true if the task appears complex, sensitive, or multi-file.
func isComplexTask(lower string, words []string) bool {
	// Long description
	if len(words) > 20 || len(lower) > 120 {
		return true
	}

	// Complex/sensitive keywords
	if containsComplexKeywords(lower) {
		return true
	}

	// Contains multiple file extensions or path patterns
	extCount := 0
	extensions := []string{".go", ".js", ".ts", ".py", ".md", ".json", ".yaml", ".yml", ".html", ".css"}
	for _, ext := range extensions {
		if strings.Contains(lower, ext) {
			extCount += strings.Count(lower, ext)
		}
	}
	if extCount >= 2 {
		return true
	}

	// Path separators or directory mentions
	if strings.Contains(lower, "/") || strings.Contains(lower, "\\") || strings.Contains(lower, "internal/") || strings.Contains(lower, "pkg/") {
		return true
	}

	return false
}

// containsComplexKeywords checks for sensitive, architectural, or multi-file keywords.
func containsComplexKeywords(lower string) bool {
	complexKeywords := []string{
		"refactor", "overhaul", "auth", "payment", "delete", "remove",
		"database", "migration", "security", "permission", "architecture",
		"multiple files", "across all files", "all tests", "breaking change",
		"redesign", "re-architect", "credential", "secret",
	}

	for _, kw := range complexKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}
