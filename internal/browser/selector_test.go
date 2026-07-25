package browser

import (
	"strings"
	"testing"
)

// TestValidStrategy documents the closed set of valid strategy
// strings. Adding a new strategy requires: (a) adding it here,
// (b) wiring it in LocatorForStrategy, (c) listing it in the
// tool schema descriptions, and (d) registering it in the system
// prompt fallback chain.
func TestValidStrategy(t *testing.T) {
	cases := []struct {
		name     string
		strategy SelectStrategy
		want     bool
	}{
		{"empty_defaults_to_css", "", true},
		{"css", StrategyCSS, true},
		{"role", StrategyRole, true},
		{"text", StrategyText, true},
		{"label", StrategyLabel, true},
		{"placeholder", StrategyPlaceholder, true},
		{"testid", StrategyTestID, true},
		{"title", StrategyTitle, true},
		{"alt", StrategyAlt, true},
		{"unknown", "xpath", false},
		{"typo", "roel", false},
		{"case_sensitive", "ROLE", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := validStrategy(c.strategy); got != c.want {
				t.Errorf("validStrategy(%q) = %v, want %v", c.strategy, got, c.want)
			}
		})
	}
}

// TestIsPositionalSelector covers the detection regex. Phase 1.4
// uses this to reject purely positional CSS selectors at tool
// validation time.
func TestIsPositionalSelector(t *testing.T) {
	cases := []struct {
		name     string
		selector string
		want     bool
	}{
		// Positional — should be flagged.
		{"nth_child", "li:nth-child(2)", true},
		{"nth_of_type", "button:nth-of-type(3)", true},
		{"nth_last_child", "li:nth-last-child(1)", true},
		{"first_child", "a:first-child", true},
		{"last_child", "a:last-child", true},
		{"only_child", "a:only-child", true},
		{"first_of_type", "p:first-of-type", true},
		{"positional_chain", "nav > ul > li:nth-child(3) > a", true},
		{"positional_uppercase", "li:NTH-CHILD(2)", true},
		{"positional_with_class", "div.row:nth-of-type(2) > input", true},

		// Non-positional — should be allowed.
		{"empty", "", false},
		{"id_selector", "#submit-btn", false},
		{"class_selector", "button.submit", false},
		{"attr_selector", "input[name=email]", false},
		{"testid", "[data-testid=login]", false},
		{"combined", "form#login input.email", false},
		{"child_combinator", "nav > ul > li > a", false},
		{"pseudo_not_positional", "a:hover", false}, // :hover is interaction, not positional
		{"pseudo_disabled", "input:disabled", false},
		{"pseudo_checked", "input:checked", false},
		{"pseudo_focus", "input:focus", false},
		{"whitespace_only", "   ", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsPositionalSelector(c.selector); got != c.want {
				t.Errorf("IsPositionalSelector(%q) = %v, want %v", c.selector, got, c.want)
			}
		})
	}
}

// TestSplitRoleName covers the "role:name" parsing. The colon
// separator is necessary because the role itself is a single
// word — there's no other meaningful parsing of "button:Sign in"
// that doesn't introduce a colon.
func TestSplitRoleName(t *testing.T) {
	cases := []struct {
		name      string
		selector  string
		wantRole  string
		wantName  string
		wantErr   bool
		errSubstr string
	}{
		{"ok", "button:Sign in", "button", "Sign in", false, ""},
		{"ok_with_class", "link:Documentation", "link", "Documentation", false, ""},
		{"ok_trims", "  button  :  Sign in  ", "button", "Sign in", false, ""},
		{"ok_name_with_colon", "button:Submit: now", "button", "Submit: now", false, ""}, // first ':' wins
		{"missing_colon", "buttonSignin", "", "", true, "missing ':'"},
		{"empty_role", ":Sign in", "", "", true, "empty role"},
		{"empty_name", "button:", "", "", true, "empty accessible name"},
		{"whitespace_only_role", "   :Sign in", "", "", true, "empty role"},
		{"whitespace_only_name", "button:   ", "", "", true, "empty accessible name"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			role, name, err := splitRoleName(c.selector)
			if c.wantErr {
				if err == nil {
					t.Fatalf("splitRoleName(%q) = (%q, %q, nil), want error", c.selector, role, name)
				}
				if c.errSubstr != "" && !strings.Contains(err.Error(), c.errSubstr) {
					t.Errorf("splitRoleName(%q) error = %q, want substring %q", c.selector, err.Error(), c.errSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("splitRoleName(%q) = unexpected error: %v", c.selector, err)
			}
			if role != c.wantRole {
				t.Errorf("splitRoleName(%q) role = %q, want %q", c.selector, role, c.wantRole)
			}
			if name != c.wantName {
				t.Errorf("splitRoleName(%q) name = %q, want %q", c.selector, name, c.wantName)
			}
		})
	}
}

// TestSplitTextExact covers the "exact:" prefix parsing.
func TestSplitTextExact(t *testing.T) {
	cases := []struct {
		name       string
		selector   string
		wantText   string
		wantExact  bool
	}{
		{"substring", "Sign in", "Sign in", false},
		{"exact_prefix", "exact:Sign in", "Sign in", true},
		{"exact_empty", "exact:", "", true},
		{"substring_with_exact_word", "Submit exact thing", "Submit exact thing", false},
		{"uppercase_exact", "EXACT:Sign in", "EXACT:Sign in", false}, // case-sensitive
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			text, exact := splitTextExact(c.selector)
			if text != c.wantText || exact != c.wantExact {
				t.Errorf("splitTextExact(%q) = (%q, %v), want (%q, %v)", c.selector, text, exact, c.wantText, c.wantExact)
			}
		})
	}
}

// TestRejectPositionalCSSOnlyOnCSSStrategy confirms the check is
// scoped to the CSS strategy path. role/text/label/etc. are
// inherently semantic and don't have positional pseudo-classes, so
// they shouldn't be rejected on this basis — and even if a model
// somehow passes a positional-looking string under strategy="role",
// we should let Playwright itself handle it (and surface Playwright's
// normal error in the result).
func TestRejectPositionalCSSOnlyOnCSSStrategy(t *testing.T) {
	cases := []struct {
		name     string
		strategy SelectStrategy
		selector string
		wantErr  bool
	}{
		{"css_positional", StrategyCSS, "li:nth-child(2)", true},
		{"css_default_positional", "", "li:nth-child(2)", true},
		{"css_stable_id", StrategyCSS, "#submit-btn", false},
		{"css_class", StrategyCSS, "button.submit", false},
		{"role_any", StrategyRole, "button:Sign in", false},
		{"text_any", StrategyText, "Sign in", false},
		{"label_any", StrategyLabel, "Email", false},
		// Even a positional-looking string under strategy="role" is
		// let through — the strategy path doesn't parse it as CSS.
		{"role_passes_positional_silently", StrategyRole, "li:nth-child(2)", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := rejectPositionalCSS("browser_click", c.strategy, c.selector)
			if c.wantErr && err == nil {
				t.Errorf("rejectPositionalCSS(%q, %q) = nil, want error", c.strategy, c.selector)
			}
			if !c.wantErr && err != nil {
				t.Errorf("rejectPositionalCSS(%q, %q) = %v, want nil", c.strategy, c.selector, err)
			}
			if c.wantErr && err != nil {
				// Confirm the error message is informative — Reviewer
				// and Coder both rely on it to know what to fix.
				low := strings.ToLower(err.Error())
				if !strings.Contains(low, "positional") {
					t.Errorf("error should mention 'positional', got: %v", err)
				}
				if !strings.Contains(low, "replace") {
					t.Errorf("error should mention 'replace', got: %v", err)
				}
			}
		})
	}
}

// TestExecuteClickRejectsPositionalAtValidation is the Phase 1.6
// test: a positional selector should be rejected up front (before
// ANY browser call), with a clear, actionable error message. This
// is the "not silently executed" guarantee.
func TestExecuteClickRejectsPositionalAtValidation(t *testing.T) {
	mgr := NewManager()
	t.Cleanup(func() { _ = mgr.Close() })

	positional := []string{
		`{"selector":"li:nth-child(2) > a","strategy":"css"}`,
		`{"selector":"button:nth-of-type(3)","strategy":"css"}`,
		`{"selector":"li:nth-child(2) > a"}`, // default strategy
	}
	for _, raw := range positional {
		t.Run(raw, func(t *testing.T) {
			_, err := mgr.ExecuteClick(raw)
			if err == nil {
				t.Fatalf("ExecuteClick(%s) = nil error, want positional rejection", raw)
			}
			low := strings.ToLower(err.Error())
			if !strings.Contains(low, "positional") {
				t.Errorf("error should mention 'positional', got: %v", err)
			}
		})
	}

	// Stable CSS selectors should NOT be rejected.
	t.Run("stable_css_passes_validation", func(t *testing.T) {
		// We don't expect this to actually click anything — the test
		// browser isn't running here — but we DO expect the call to
		// fail with a Chromium launch error, not a positional
		// rejection. The error path that proves "validation passed" is
		// "browser not launched" or "connection refused", not
		// "positional".
		_, err := mgr.ExecuteClick(`{"selector":"#submit-btn","strategy":"css"}`)
		if err == nil {
			// If it succeeded, great — but on most CI environments the
			// browser binary is missing, so we expect a launch error.
			return
		}
		if strings.Contains(strings.ToLower(err.Error()), "positional") {
			t.Errorf("stable CSS selector should not be flagged as positional; got: %v", err)
		}
	})
}

// TestExecuteTypeRejectsPositional mirrors ExecuteClick.
func TestExecuteTypeRejectsPositional(t *testing.T) {
	mgr := NewManager()
	t.Cleanup(func() { _ = mgr.Close() })

	_, err := mgr.ExecuteType(`{"selector":"form > div:nth-of-type(2) > input","text":"x","strategy":"css"}`)
	if err == nil {
		t.Fatal("ExecuteType with positional selector should have failed")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "positional") {
		t.Errorf("error should mention 'positional', got: %v", err)
	}
}

// TestExecuteGetTextRejectsPositional mirrors ExecuteClick.
func TestExecuteGetTextRejectsPositional(t *testing.T) {
	mgr := NewManager()
	t.Cleanup(func() { _ = mgr.Close() })

	_, err := mgr.ExecuteGetText(`{"selector":"div:nth-child(3) > p","strategy":"css"}`)
	if err == nil {
		t.Fatal("ExecuteGetText with positional selector should have failed")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "positional") {
		t.Errorf("error should mention 'positional', got: %v", err)
	}
}

// TestBrowserArgsStrategyRoundTrip confirms the JSON unmarshal
// path correctly surfaces the optional `strategy` field on each
// args struct. This is the contract the LLM-facing tool schema
// describes — if we silently dropped the field, every strategy
// would land as empty (default CSS) and Phase 1.3 wouldn't work.
func TestBrowserArgsStrategyRoundTrip(t *testing.T) {
	t.Run("click", func(t *testing.T) {
		var a ClickArgs
		if err := unmarshalArgs("browser_click", `{"selector":"button:Sign in","strategy":"role"}`, &a); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if a.Selector != "button:Sign in" {
			t.Errorf("selector = %q, want %q", a.Selector, "button:Sign in")
		}
		if a.Strategy != StrategyRole {
			t.Errorf("strategy = %q, want %q", a.Strategy, StrategyRole)
		}
	})
	t.Run("type", func(t *testing.T) {
		var a TypeArgs
		if err := unmarshalArgs("browser_type", `{"selector":"Email","text":"x","strategy":"label"}`, &a); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if a.Strategy != StrategyLabel {
			t.Errorf("strategy = %q, want %q", a.Strategy, StrategyLabel)
		}
	})
	t.Run("get_text", func(t *testing.T) {
		var a GetTextArgs
		if err := unmarshalArgs("browser_get_text", `{"selector":"Success","strategy":"text"}`, &a); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if a.Strategy != StrategyText {
			t.Errorf("strategy = %q, want %q", a.Strategy, StrategyText)
		}
	})
	// Strategy omitted → defaults to empty (which is treated as CSS).
	t.Run("strategy_omitted", func(t *testing.T) {
		var a ClickArgs
		if err := unmarshalArgs("browser_click", `{"selector":"#submit"}`, &a); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if a.Strategy != "" {
			t.Errorf("strategy = %q, want empty (default to CSS)", a.Strategy)
		}
	})
}

// TestRejectUnknownStrategy confirms an unknown strategy string is
// rejected with a clear error that lists the valid values.
func TestRejectUnknownStrategy(t *testing.T) {
	mgr := NewManager()
	t.Cleanup(func() { _ = mgr.Close() })

	for _, fn := range []struct {
		name string
		call func() (string, error)
	}{
		{"click", func() (string, error) { return mgr.ExecuteClick(`{"selector":"x","strategy":"xpath"}`) }},
		{"type", func() (string, error) { return mgr.ExecuteType(`{"selector":"x","text":"y","strategy":"regex"}`) }},
		{"get_text", func() (string, error) { return mgr.ExecuteGetText(`{"selector":"x","strategy":"regex"}`) }},
	} {
		t.Run(fn.name, func(t *testing.T) {
			_, err := fn.call()
			if err == nil {
				t.Fatalf("%s with unknown strategy should fail", fn.name)
			}
			if !strings.Contains(strings.ToLower(err.Error()), "unknown strategy") {
				t.Errorf("error should mention 'unknown strategy', got: %v", err)
			}
		})
	}
}
