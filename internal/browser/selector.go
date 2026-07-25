package browser

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/mxschmitt/playwright-go"
)

// SelectStrategy is the explicit hint Coder passes to browser_click /
// browser_type / browser_get_text to say which Playwright Locator
// factory to use. Default (empty string) is StrategyCSS for backward
// compatibility with tasks that already work — but the Coder system
// prompt steers Coder toward StrategyRole / StrategyText / etc.
//
// Work 4 Phase 1.3: a stable, semantic selector is the single biggest
// lever for browser-tool reliability, so we make Coder state its
// intent explicitly rather than parsing everything as a raw CSS
// string. The fields intentionally mirror the order in the system
// prompt so reviewers can cross-reference them.
type SelectStrategy string

const (
	// StrategyCSS — raw CSS / standard Playwright selector. Last resort.
	StrategyCSS SelectStrategy = "css"
	// StrategyRole — selector is "role:name" (e.g. "button:Sign in").
	// Maps to page.GetByRole(role, {Name: name}).
	StrategyRole SelectStrategy = "role"
	// StrategyText — selector is visible text. Maps to page.GetByText.
	// Optional "exact:" prefix requests an exact match.
	StrategyText SelectStrategy = "text"
	// StrategyLabel — selector is the <label> text / aria-label.
	// Maps to page.GetByLabel.
	StrategyLabel SelectStrategy = "label"
	// StrategyPlaceholder — selector is placeholder text. Maps to
	// page.GetByPlaceholder.
	StrategyPlaceholder SelectStrategy = "placeholder"
	// StrategyTestID — selector is data-testid. Maps to page.GetByTestId.
	StrategyTestID SelectStrategy = "testid"
	// StrategyTitle — selector is the title attribute. Maps to
	// page.GetByTitle.
	StrategyTitle SelectStrategy = "title"
	// StrategyAlt — selector is alt text. Maps to page.GetByAltText.
	StrategyAlt SelectStrategy = "alt"
)

// validStrategy reports whether s is one of the known strategies.
// Empty string is treated as StrategyCSS (the default) and is valid.
func validStrategy(s SelectStrategy) bool {
	switch s {
	case "", StrategyCSS, StrategyRole, StrategyText, StrategyLabel,
		StrategyPlaceholder, StrategyTestID, StrategyTitle, StrategyAlt:
		return true
	}
	return false
}

// Positional-selector detection (Work 4 Phase 1.4). We flag selectors
// that are layout-coupled (parent-relative child positions) so
// Reviewer can object — these break the moment the page layout
// changes. Mere CSS selectors like "#submit-btn" or "input[name=email]"
// are fine.
//
// A selector is considered positional if it contains ANY of:
//
//   - nth-child(N)
//   - nth-of-type(N)
//   - nth-last-child(N)
//   - nth-last-of-type(N)
//   - first-child / last-child / only-child
//   - first-of-type / last-of-type / only-of-type
//
// AND those positional pieces are not part of a structural context
// that already anchors semantically (we don't try to be smart here —
// if any positional pseudo-class is present, the selector is flagged).
//
// Note: ":first-child" appearing inside a compound selector counts;
// the rule is "don't ship layout-coupled selectors at all", not
// "ship slightly less layout-coupled selectors".
var positionalPseudo = regexp.MustCompile(`(?i):(nth-child|nth-of-type|nth-last-child|nth-last-of-type|first-child|last-child|only-child|first-of-type|last-of-type|only-of-type)\b`)

// IsPositionalSelector reports whether the raw CSS selector looks
// layout-coupled. This is a quality signal surfaced to Reviewer
// (Phase 1.6) — it does NOT block execution on its own, because
// there are still legitimate cases (e.g. a setup helper that wants
// the literal third row). It returns true for selectors that look
// positional, so the caller can decide.
func IsPositionalSelector(selector string) bool {
	if strings.TrimSpace(selector) == "" {
		return false
	}
	return positionalPseudo.MatchString(selector)
}

// splitRoleName parses "role:name" into role and name. The role
// follows ARIA conventions: button, link, textbox, checkbox, etc.
// The name is everything after the first ':'. If the name is empty
// (just "button:"), returns an error. If no ':' is present, returns
// an error — the strategy="role" path always expects both halves.
func splitRoleName(selector string) (role, name string, err error) {
	idx := strings.Index(selector, ":")
	if idx < 0 {
		return "", "", fmt.Errorf("strategy=role: selector %q is missing ':' between role and accessible name (expected form: \"button:Sign in\")", selector)
	}
	role = strings.TrimSpace(selector[:idx])
	name = strings.TrimSpace(selector[idx+1:])
	if role == "" {
		return "", "", fmt.Errorf("strategy=role: selector %q has empty role before ':'", selector)
	}
	if name == "" {
		return "", "", fmt.Errorf("strategy=role: selector %q has empty accessible name after ':'", selector)
	}
	return role, name, nil
}

// splitTextExact parses an optional "exact:" prefix on a text
// selector. Default is substring (the Playwright default).
func splitTextExact(selector string) (text string, exact bool) {
	const prefix = "exact:"
	if strings.HasPrefix(selector, prefix) {
		return strings.TrimPrefix(selector, prefix), true
	}
	return selector, false
}

// LocatorForStrategy returns the Playwright Locator corresponding to
// the given strategy + selector. This is the single place that maps
// Coder's intent to a Playwright Locator; we keep it here so the
// per-tool executors stay thin and the strategy semantics live in
// one file with tests.
//
// `toolName` is only used to make error messages nicer.
func LocatorForStrategy(page playwright.Page, strategy SelectStrategy, selector, toolName string) (playwright.Locator, error) {
	if selector == "" {
		return nil, fmt.Errorf("%s: selector is empty", toolName)
	}
	switch strategy {
	case "", StrategyCSS:
		return page.Locator(selector), nil

	case StrategyRole:
		role, name, err := splitRoleName(selector)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", toolName, err)
		}
		// PageGetByRoleOptions.Name is `any` (Playwright accepts string,
		// regex, or a struct for matching). We pass the raw string —
		// substring match is the documented default.
		opts := playwright.PageGetByRoleOptions{Name: name}
		return page.GetByRole(playwright.AriaRole(role), opts), nil

	case StrategyText:
		text, exact := splitTextExact(selector)
		if text == "" {
			return nil, fmt.Errorf("%s: strategy=text selector is empty after stripping 'exact:' prefix", toolName)
		}
		opts := playwright.PageGetByTextOptions{}
		if exact {
			opts.Exact = boolPtr(true)
		}
		return page.GetByText(text, opts), nil

	case StrategyLabel:
		if selector == "" {
			return nil, fmt.Errorf("%s: strategy=label selector is empty", toolName)
		}
		return page.GetByLabel(selector), nil

	case StrategyPlaceholder:
		if selector == "" {
			return nil, fmt.Errorf("%s: strategy=placeholder selector is empty", toolName)
		}
		return page.GetByPlaceholder(selector), nil

	case StrategyTestID:
		if selector == "" {
			return nil, fmt.Errorf("%s: strategy=testid selector is empty", toolName)
		}
		return page.GetByTestId(selector), nil

	case StrategyTitle:
		if selector == "" {
			return nil, fmt.Errorf("%s: strategy=title selector is empty", toolName)
		}
		return page.GetByTitle(selector), nil

	case StrategyAlt:
		if selector == "" {
			return nil, fmt.Errorf("%s: strategy=alt selector is empty", toolName)
		}
		return page.GetByAltText(selector), nil

	default:
		return nil, fmt.Errorf("%s: unknown strategy %q (valid: css, role, text, label, placeholder, testid, title, alt)", toolName, strategy)
	}
}

// boolPtr is a tiny helper so callers can pass &true to a Playwright
// options struct that takes *bool. Kept private — this is for the
// selector strategy plumbing only.
func boolPtr(b bool) *bool {
	return &b
}
