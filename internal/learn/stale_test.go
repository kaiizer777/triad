package learn

import (
	"strings"
	"testing"
)

func TestParseTopicEntries(t *testing.T) {
	content := `# Architecture Topic Memory

This is a totally dateless entry placed here.

- Multi-Agent Approval Loop: All tool calls must be explicitly approved...
- Storage: Use flat files.
- [2026-01-01] Old format with date.
Some plain text that gets absorbed.
### [2026-07-28] REVIEWER OBJECTION: raw body required for HMAC verification
<!-- confidence: high | verified: 2026-07-28 | source: learn-promote:a1b2c3 -->
Webhook signature verification must run against the raw request body, not
the parsed/re-serialized JSON — re-serialization changes byte order and
breaks HMAC comparison.
`
	entries := parseTopicEntries(content)
	if len(entries) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(entries))
	}

	if !entries[0].Date.IsZero() || entries[0].HasHeader {
		t.Errorf("expected entry 0 to have unknown date, got %v", entries[0].Date)
	}
	if !strings.Contains(entries[0].OriginalText, "totally dateless entry") {
		t.Errorf("expected entry 0 to be the dateless text, got %q", entries[0].OriginalText)
	}

	if entries[3].Date.Format("2006-01-02") != "2026-01-01" {
		t.Errorf("expected entry 3 to have date 2026-01-01, got %v", entries[3].Date)
	}
	if !strings.Contains(entries[3].OriginalText, "Some plain text that gets absorbed") {
		t.Errorf("expected entry 3 to have absorbed text, got %q", entries[3].OriginalText)
	}

	if entries[4].Date.Format("2006-01-02") != "2026-07-28" {
		t.Errorf("expected entry 4 to have date 2026-07-28, got %v", entries[4].Date)
	}
	expectedBodyFragment := "breaks HMAC comparison."
	if !strings.Contains(entries[4].OriginalText, expectedBodyFragment) {
		t.Errorf("expected entry 4 to contain the full multi-line body, but it was split or missing. Got:\n%s", entries[4].OriginalText)
	}
}
