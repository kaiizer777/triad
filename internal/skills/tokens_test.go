package skills

import (
	"strings"
	"testing"
)

func TestEstimateTokens(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"empty", "", 0},
		{"whitespace only", "   \n\t\n  ", 0},
		{"short word", "hello", 2}, // 5 chars, ceil(5/4)=2
		{"single word exact 4", "word", 1},
		{"medium body", "The quick brown fox jumps over the lazy dog.", 11}, // 44 chars, ceil(44/4)=11
		{"multi-line collapses whitespace",
			"line1\n\nline2   with   spaces\nline3",
			// collapsed = "line1 line2 with spaces line3" = 30 chars, ceil(30/4)=8
			8,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := EstimateTokens(c.in)
			if got != c.want {
				t.Errorf("EstimateTokens(%q) = %d, want %d", c.in, got, c.want)
			}
		})
	}
}

func TestEstimateTokens_Deterministic(t *testing.T) {
	// Determinism: same input → same output across calls. The
	// /trace view would be confusing if a skill's per-turn cost
	// changed between two runs of the same task.
	body := "FRONTEND MAIN BODY: use functional components.\n"
	first := EstimateTokens(body)
	for i := 0; i < 100; i++ {
		if got := EstimateTokens(body); got != first {
			t.Fatalf("non-deterministic: got %d on call %d, want %d", got, i, first)
		}
	}
}

func TestEstimateInjectedTokens(t *testing.T) {
	body := "FRONTEND MAIN BODY: use functional components.\n"
	// strings.Fields strips the trailing newline; collapsed body
	// is "FRONTEND MAIN BODY: use functional components." = 47
	// chars → ceil(47/4) = 12 tokens + 30 delimiter overhead = 42.
	got := EstimateInjectedTokens("frontend", TierMain, body)
	if got != 42 {
		t.Errorf("EstimateInjectedTokens: got %d, want 42", got)
	}

	// Empty body: skip the delimiter, return 0.
	if got := EstimateInjectedTokens("frontend", TierMain, ""); got != 0 {
		t.Errorf("empty body: got %d, want 0", got)
	}
	// Empty tier: still 0 (the funnel skips emitting the
	// delimiter when the chosen tier's body is empty, so the
	// cost is the body's cost, which is also 0).
	if got := EstimateInjectedTokens("frontend", "", ""); got != 0 {
		t.Errorf("empty tier + empty body: got %d, want 0", got)
	}
}

func TestEstimateInjectedTokens_IncludesDelimiter(t *testing.T) {
	// The delimiter is part of what the model sees, so it must
	// be in the cost. The body itself is small (a few chars);
	// the cost should still be ≥ delimiter overhead.
	small := "x"
	got := EstimateInjectedTokens("foo", TierMini, small)
	bodyCost := EstimateTokens(small)
	if got <= bodyCost {
		t.Errorf("injected cost (%d) should exceed body cost (%d) to account for the delimiter",
			got, bodyCost)
	}
	// Spot-check: a 1-char body costs 1 token + 30 = 31.
	if got != 31 {
		t.Errorf("small body cost: got %d, want 31", got)
	}
}

// TestEstimateTokens_RealisticMain is a sanity check against a
// realistic Main skill body size (a few hundred words, like the
// real skills in `skills/` will be). We're not pinning the exact
// number — different encoders disagree at this scale — but we
// want a value in the right ballpark (hundreds of tokens, not
// dozens or tens of thousands). A regression that returns "12"
// for a 1KB body would be caught here.
func TestEstimateTokens_RealisticMain(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 200; i++ {
		sb.WriteString("This is a sentence that approximates a real skill body. ")
	}
	body := sb.String()
	got := EstimateTokens(body)
	// 200 sentences × ~64 chars = ~12800 chars → ~3200 tokens.
	// Allow a wide window for tokenizer variance.
	if got < 2000 || got > 5000 {
		t.Errorf("realistic body token estimate out of range: got %d, want 2000-5000", got)
	}
}
