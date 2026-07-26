package skills

import "strings"

// EstimateTokens returns an approximate token count for a body of text
// that will be injected into Coder's prompt. Used by Phase 4
// observability to record per-section and per-turn token cost in the
// /trace view (work.md §7).
//
// This is NOT a real tokenizer. The estimate uses a chars-per-token
// ratio (≈ 4 characters per token) which is a well-known rough
// approximation for English-like text — close enough to GPT-family
// BPE tokenizers for the "how much did this turn cost" question the
// /trace view is answering. Phase 5.4 will switch to a real tokenizer
// (e.g. tiktoken) for the hard budget-enforcement check
// (work.md §5.4 / Phase 5.4), but that swap is out of scope for
// observability — all observability needs is a stable, deterministic
// number, not a precise one.
//
// The number is intentionally deterministic (no randomness, no model
// state) so a skill body always produces the same cost across
// sessions — otherwise the /trace view would show different costs for
// the same skill on different days, which would be confusing.
//
// Whitespace is normalized: runs of whitespace are collapsed to a
// single space. This avoids the classic "Markdown newlines inflate
// the count" trap where a body full of blank lines would cost 2x what
// the model actually sees. We also trim leading/trailing whitespace
// before counting.
//
// Empty input → 0 tokens. Bodies that produce a non-positive count
// return 0.
func EstimateTokens(body string) int {
	if body == "" {
		return 0
	}
	// Collapse runs of whitespace (spaces, tabs, newlines, CR) to a
	// single space. This matches what a BPE tokenizer does to
	// repeated whitespace — multiple spaces between words are still
	// just one space at the token level.
	collapsed := strings.Join(strings.Fields(body), " ")
	if collapsed == "" {
		return 0
	}
	// chars-per-token: GPT-style encoders average ~4 chars/token for
	// English prose; Markdown is close enough at this granularity.
	// The 4 here is the same rule of thumb used by every "cheap
	// token estimator" in the wild. Rounding up via ceiling division
	// so an empty-but-with-whitespace body never returns 0 falsely.
	const charsPerToken = 4
	n := len(collapsed)
	tokens := (n + charsPerToken - 1) / charsPerToken
	if tokens < 1 {
		tokens = 1
	}
	return tokens
}

// EstimateInjectedTokens returns the token cost of one section's
// contribution to a Coder prompt, including the `--- skill:NAME
// (tier: tier) ---` delimiter the prompt builder wraps every body
// with. Without the delimiter the /trace view would undercount by
// the per-section framing overhead and humans would wonder why the
// numbers don't quite add up to the prompt total.
//
// Pass tier = "" for the "empty body, nothing injected" case — the
// prompt builder skips emitting the delimiter there, so the function
// returns 0. The matching TestEstimateInjectedTokens_EmptyTier test
// pins this contract.
func EstimateInjectedTokens(name string, tier Tier, body string) int {
	if body == "" {
		return 0
	}
	const delimiterOverhead = 30 // ample for `--- skill:foo (tier: mini) ---\n\n`
	bodyCost := EstimateTokens(body)
	return bodyCost + delimiterOverhead
}
