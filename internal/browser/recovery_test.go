package browser

import (
	"errors"
	"testing"
)

// ---------------------------------------------------------------------------
// Phase 3.1 — Selector failure detection tests
// ---------------------------------------------------------------------------

// TestDetectSelectorFailure_ZeroMatch confirms that errors containing
// "no element" or "resolved to no element" are classified as
// zero-match failures.
func TestDetectSelectorFailure_ZeroMatch(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"resolved_to_no_element", errors.New("selector resolved to no element")},
		{"no_element_generic", errors.New("could not find element with selector")},
		{"zero_elements", errors.New("0 elements matched")},
		{"no_element_mixed_case", errors.New("No Element found for selector")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := DetectSelectorFailure("browser_click", StrategyCSS, "#missing", c.err)
			if f == nil {
				t.Fatalf("DetectSelectorFailure(%q) = nil, want zero_match", c.err)
			}
			if f.Type != FailureZeroMatch {
				t.Errorf("failure type = %v, want %v", f.Type, FailureZeroMatch)
			}
			if f.Selector != "#missing" {
				t.Errorf("selector = %q, want %q", f.Selector, "#missing")
			}
			if f.Strategy != StrategyCSS {
				t.Errorf("strategy = %q, want %q", f.Strategy, StrategyCSS)
			}
		})
	}
}

// TestDetectSelectorFailure_AmbiguousMatch confirms that errors
// containing "strict mode violation" are classified as ambiguous-match
// failures — this is the Playwright strict-mode error when a locator
// matches multiple elements.
func TestDetectSelectorFailure_AmbiguousMatch(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"strict_mode_violation", errors.New("strict mode violation: selector resolved to 3 elements")},
		{"strict_with_count", errors.New("Error: strict mode violation: \"button\" resolved to 2 elements")},
		{"more_than_one", errors.New("more than one element found for selector")},
		{"strict_uppercase", errors.New("Strict mode violation: 5 elements matched")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := DetectSelectorFailure("browser_click", StrategyRole, "button:OK", c.err)
			if f == nil {
				t.Fatalf("DetectSelectorFailure(%q) = nil, want ambiguous_match", c.err)
			}
			if f.Type != FailureAmbiguousMatch {
				t.Errorf("failure type = %v, want %v", f.Type, FailureAmbiguousMatch)
			}
		})
	}
}

// TestDetectSelectorFailure_NonSelectorErrors confirms that errors
// which are NOT selector failures (e.g. action timeout, browser not
// launched) return nil — the caller should surface these as normal
// tool errors. Timeout errors with "waiting for" ARE treated as
// selector failures (the selector never resolved).
func TestDetectSelectorFailure_NonSelectorErrors(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		wantNil bool
	}{
		{"action_timeout_without_waiting_for", errors.New("browser_click: timeout after 30s"), true},
		{"timeout_waiting_for_element", errors.New("browser_click: timeout after 30s waiting for element to be visible"), false},
		{"browser_not_launched", errors.New("browser: manager is closed"), true},
		{"nil_error", nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := DetectSelectorFailure("browser_click", StrategyCSS, "#btn", c.err)
			if c.wantNil && f != nil {
				t.Errorf("DetectSelectorFailure(%q) = %v, want nil (non-selector error)", c.err, f.Type)
			}
			if !c.wantNil && f == nil {
				t.Errorf("DetectSelectorFailure(%q) = nil, want non-nil (selector failure)", c.err)
			}
		})
	}
}

// TestDetectSelectorFailure_PreservesMetadata confirms the failure
// struct carries the original selector, strategy, and tool name.
func TestDetectSelectorFailure_PreservesMetadata(t *testing.T) {
	err := errors.New("strict mode violation: 2 elements")
	f := DetectSelectorFailure("browser_type", StrategyLabel, "Email", err)
	if f == nil {
		t.Fatal("expected non-nil failure")
	}
	if f.ToolName != "browser_type" {
		t.Errorf("tool = %q, want %q", f.ToolName, "browser_type")
	}
	if f.Selector != "Email" {
		t.Errorf("selector = %q, want %q", f.Selector, "Email")
	}
	if f.Strategy != StrategyLabel {
		t.Errorf("strategy = %q, want %q", f.Strategy, StrategyLabel)
	}
	if f.Err != err {
		t.Errorf("err not preserved")
	}
}

// TestDetectSelectorFailure_UnknownShapeDefaultsToNil confirms
// that an error with an unrecognized shape returns nil — only
// errors that explicitly mention selector-related keywords trigger
// recovery. Non-selector errors (e.g. "some unexpected playwright
// error") are surfaced as normal tool errors.
func TestDetectSelectorFailure_UnknownShapeDefaultsToNil(t *testing.T) {
	err := errors.New("some unexpected playwright error")
	f := DetectSelectorFailure("browser_click", StrategyCSS, "#x", err)
	if f != nil {
		t.Errorf("expected nil for unknown error shape, got %v", f.Type)
	}
}

// ---------------------------------------------------------------------------
// Text similarity tests
// ---------------------------------------------------------------------------

// TestComputeSimilarity covers the core similarity heuristic.
func TestComputeSimilarity(t *testing.T) {
	cases := []struct {
		name     string
		a, b     string
		minScore float64
		maxScore float64
	}{
		{"exact_match", "Sign In", "Sign In", 1.0, 1.0},
		{"case_insensitive", "sign in", "Sign In", 1.0, 1.0},
		{"substring_long", "Sign in to your account", "Sign in", 0.25, 0.4},
		{"substring_short", "Sign in", "Sign in to your account", 0.25, 0.4},
		{"partial_word_overlap", "Submit Form", "Submit", 0.5, 1.0},
		{"no_overlap", "Sign In", "Cancel", 0.0, 0.1},
		{"empty_a", "", "Sign In", 0.0, 0.0},
		{"empty_b", "Sign In", "", 0.0, 0.0},
		{"both_empty", "", "", 0.0, 0.0},
		{"similar_words", "Sign In Button", "Sign In Link", 0.5, 0.8},
		{"completely_different", "Submit", "Cancel", 0.0, 0.1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			score := computeSimilarity(c.a, c.b)
			if score < c.minScore || score > c.maxScore {
				t.Errorf("computeSimilarity(%q, %q) = %f, want [%f, %f]",
					c.a, c.b, score, c.minScore, c.maxScore)
			}
		})
	}
}

// TestComputeSimilarity_SubstringMatch specifically tests that one
// string being a substring of the other returns a reasonable score.
func TestComputeSimilarity_SubstringMatch(t *testing.T) {
	score := computeSimilarity("Sign in", "Please sign in to continue")
	if score < 0.15 {
		t.Errorf("substring match score = %f, want >= 0.3", score)
	}
}

// TestSplitWords covers the word-splitting helper.
func TestSplitWords(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"simple", "hello world", []string{"hello", "world"}},
		{"with_punctuation", "sign-in button!", []string{"sign", "in", "button"}},
		{"numbers", "item 42", []string{"item", "42"}},
		{"empty", "", nil},
		{"only_spaces", "   ", nil},
		{"mixed", "hello-world_foo", []string{"hello", "world", "foo"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := splitWords(c.in)
			if len(got) != len(c.want) {
				t.Fatalf("splitWords(%q) = %v, want %v", c.in, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("splitWords(%q)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
				}
			}
		})
	}
}

// TestExtractTargetText confirms the right text is extracted from
// each selector/strategy combination.
func TestExtractTargetText(t *testing.T) {
	cases := []struct {
		name     string
		selector string
		strategy SelectStrategy
		want     string
	}{
		{"role", "button:Sign in", StrategyRole, "Sign in"},
		{"role_no_colon", "buttonSignin", StrategyRole, ""},
		{"text", "Sign in", StrategyText, "Sign in"},
		{"text_exact", "exact:Sign in", StrategyText, "Sign in"},
		{"label", "Email", StrategyLabel, "Email"},
		{"placeholder", "Enter password", StrategyPlaceholder, "Enter password"},
		{"title", "Submit form", StrategyTitle, "Submit form"},
		{"alt", "Company logo", StrategyAlt, "Company logo"},
		{"css_id", "#submit-btn", StrategyCSS, "submit btn"},
		{"css_class", ".login-button", StrategyCSS, "login button"},
		{"css_default", "#my-element", "", "my element"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := extractTargetText(c.selector, c.strategy)
			if got != c.want {
				t.Errorf("extractTargetText(%q, %q) = %q, want %q",
					c.selector, c.strategy, got, c.want)
			}
		})
	}
}

// TestSplitRoleNameFast covers the fast role-name splitter used
// internally by recovery logic.
func TestSplitRoleNameFast(t *testing.T) {
	cases := []struct {
		selector string
		wantRole string
		wantName string
	}{
		{"button:Sign in", "button", "Sign in"},
		{"link:Documentation", "link", "Documentation"},
		{"buttonSignin", "", ""},
		{"", "", ""},
		{"button:", "button", ""},
		{":Sign in", "", "Sign in"},
	}
	for _, c := range cases {
		t.Run(c.selector, func(t *testing.T) {
			role, name := splitRoleNameFast(c.selector)
			if role != c.wantRole || name != c.wantName {
				t.Errorf("splitRoleNameFast(%q) = (%q, %q), want (%q, %q)",
					c.selector, role, name, c.wantRole, c.wantName)
			}
		})
	}
}

// TestDeduplicateCandidates confirms that duplicate candidates are
// removed and sorted by relevance.
func TestDeduplicateCandidates(t *testing.T) {
	input := []similarCandidate{
		{Text: "Cancel", Strategy: StrategyText},
		{Text: "Sign In", Strategy: StrategyText},
		{Text: "Cancel", Strategy: StrategyText}, // duplicate
		{Text: "Sign In", Strategy: StrategyRole}, // different strategy, not a duplicate
	}
	result := deduplicateCandidates(input, "Sign In")
	if len(result) != 3 {
		t.Fatalf("expected 3 unique candidates, got %d", len(result))
	}
	// "Sign In" (text) should be first — exact match to target.
	if result[0].Text != "Sign In" || result[0].Strategy != StrategyText {
		t.Errorf("first candidate should be exact match 'Sign In'/text, got %q/%q",
			result[0].Text, result[0].Strategy)
	}
}

// TestRecoveryResult_Fields confirms the RecoveryResult struct
// correctly represents both "recovered" and "needs-review" states.
func TestRecoveryResult_Fields(t *testing.T) {
	t.Run("recovered", func(t *testing.T) {
		r := &RecoveryResult{
			Recovered: true,
			Result:    "browser_click: clicked [text] \"Sign In\"",
		}
		if !r.Recovered {
			t.Error("Recovered should be true")
		}
		if r.Candidate != "" {
			t.Error("Candidate should be empty when Recovered is true")
		}
	})

	t.Run("needs_review", func(t *testing.T) {
		r := &RecoveryResult{
			Recovered:         false,
			Candidate:         "Sign In",
			CandidateStrategy: StrategyText,
		}
		if r.Recovered {
			t.Error("Recovered should be false")
		}
		if r.Candidate != "Sign In" {
			t.Errorf("Candidate = %q, want %q", r.Candidate, "Sign In")
		}
	})
}

// TestSelectorFailureType_String covers the String() method.
func TestSelectorFailureType_String(t *testing.T) {
	cases := []struct {
		t    SelectorFailureType
		want string
	}{
		{FailureNone, "none"},
		{FailureZeroMatch, "zero_match"},
		{FailureAmbiguousMatch, "ambiguous_match"},
		{SelectorFailureType(99), "unknown"},
	}
	for _, c := range cases {
		t.Run(c.want, func(t *testing.T) {
			if got := c.t.String(); got != c.want {
				t.Errorf("String() = %q, want %q", got, c.want)
			}
		})
	}
}

// TestDetectSelectorFailure_PlaywrightRealErrors uses realistic
// Playwright error messages (not contrived ones) to confirm the
// detection works against actual errors from the playwright-go driver.
func TestDetectSelectorFailure_PlaywrightRealErrors(t *testing.T) {
	// Real Playwright-go error for zero match.
	realZeroMatch := errors.New(
		"browser_click: failed to click [role] \"button:Nonexistent\": " +
			"Could not locate element: \"button:Nonexistent\"")
	f := DetectSelectorFailure("browser_click", StrategyRole, "button:Nonexistent", realZeroMatch)
	if f == nil {
		t.Fatal("expected non-nil for real zero-match error")
	}
	if f.Type != FailureZeroMatch {
		t.Errorf("type = %v, want %v", f.Type, FailureZeroMatch)
	}

	// Real Playwright-go error for strict mode violation.
	realAmbiguous := errors.New(
		"browser_click: failed to click [role] \"button:OK\": " +
			"Error: strict mode violation: \"button\" resolved to 2 elements")
	f = DetectSelectorFailure("browser_click", StrategyRole, "button:OK", realAmbiguous)
	if f == nil {
		t.Fatal("expected non-nil for real ambiguous error")
	}
	if f.Type != FailureAmbiguousMatch {
		t.Errorf("type = %v, want %v", f.Type, FailureAmbiguousMatch)
	}
}
