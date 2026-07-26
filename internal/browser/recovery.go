package browser

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/mxschmitt/playwright-go"
)

// ---------------------------------------------------------------------------
// Phase 3.1 — Selector failure detection (two distinct failure types)
// ---------------------------------------------------------------------------

// SelectorFailureType distinguishes the two ways a selector can fail:
//   - ZeroMatch:    the selector found nothing on the page
//   - AmbiguousMatch: the selector matched more than one element
//     (Playwright's strict-mode violation)
type SelectorFailureType int

const (
	// FailureNone means no failure was detected — the selector worked.
	FailureNone SelectorFailureType = iota
	// FailureZeroMatch means the selector matched zero elements.
	FailureZeroMatch
	// FailureAmbiguousMatch means the selector matched multiple elements
	// (Playwright's strict-mode violation).
	FailureAmbiguousMatch
)

// String returns a human-readable label for the failure type.
func (t SelectorFailureType) String() string {
	switch t {
	case FailureNone:
		return "none"
	case FailureZeroMatch:
		return "zero_match"
	case FailureAmbiguousMatch:
		return "ambiguous_match"
	default:
		return "unknown"
	}
}

// SelectorFailure holds the parsed result of a failed selector operation.
// It carries the failure type, the original selector/strategy, the raw
// Playwright error, and the tool name — everything the recovery logic
// needs to attempt a fix.
type SelectorFailure struct {
	Type     SelectorFailureType
	Selector string
	Strategy SelectStrategy
	ToolName string
	Err      error
}

// DetectSelectorFailure inspects an error returned by a Playwright
// locator operation and classifies it as either a zero-match or
// ambiguous-match failure. Non-selector errors (e.g. browser not
// launched, timeout waiting for visibility) return FailureNone —
// those are not recoverable by changing the selector.
//
// Playwright-go's error messages for these two cases are well-known:
//   - Zero match: "strict mode violation" is NOT in the message;
//     the error typically says "selector resolved to no element" or
//     just a generic timeout
//   - Ambiguous match: "strict mode violation" IS in the message,
//     along with "N elements" or "more than one element"
//
// We key on the presence of "strict mode violation" to distinguish
// the two, falling back to "no element" / "0 elements" for
// zero-match on platforms where the wording varies.
func DetectSelectorFailure(toolName string, strategy SelectStrategy, selector string, err error) *SelectorFailure {
	if err == nil {
		return nil
	}

	msg := err.Error()
	lower := strings.ToLower(msg)

	// Ambiguous match — Playwright's strict-mode violation.
	// The message contains "strict mode violation" and typically
	// lists how many elements matched.
	if strings.Contains(lower, "strict mode violation") ||
		strings.Contains(lower, "more than one element") {
		return &SelectorFailure{
			Type:     FailureAmbiguousMatch,
			Selector: selector,
			Strategy: strategy,
			ToolName: toolName,
			Err:      err,
		}
	}

	// Zero match — the selector found nothing.
	// Playwright says "selector resolved to no element" or
	// "0 elements" or sometimes just a timeout error.
	if strings.Contains(lower, "no element") ||
		strings.Contains(lower, "0 elements") ||
		strings.Contains(lower, "resolved to no element") ||
		strings.Contains(lower, "could not find") ||
		strings.Contains(lower, "could not locate") {
		return &SelectorFailure{
			Type:     FailureZeroMatch,
			Selector: selector,
			Strategy: strategy,
			ToolName: toolName,
			Err:      err,
		}
	}

	// For timeout errors, distinguish between selector-not-found
	// timeouts (element never appeared) and action timeouts (element
	// found but action took too long). The former is a recoverable
	// selector failure; the latter is not.
	if strings.Contains(lower, "timeout") {
		// "waiting for" in the error message means Playwright was
		// waiting for the selector to resolve — it never did. This
		// is effectively a zero-match.
		if strings.Contains(lower, "waiting for") {
			return &SelectorFailure{
				Type:     FailureZeroMatch,
				Selector: selector,
				Strategy: strategy,
				ToolName: toolName,
				Err:      err,
			}
		}
		// Other timeouts (e.g. action timeout after element found)
		// are not selector failures.
		return nil
	}

	// Unknown error shape — return nil. We used to default these to
	// zero-match, but errors like "browser: manager is closed" are
	// infrastructure failures, not selector failures. Only errors that
	// explicitly mention selector-related keywords trigger recovery.
	return nil
}

// ---------------------------------------------------------------------------
// Phase 3.2 — Deterministic recovery (no model call)
// ---------------------------------------------------------------------------

// RecoveryResult holds the outcome of a deterministic recovery attempt.
type RecoveryResult struct {
	// Recovered is true when the recovery found and successfully
	// executed a corrected action.
	Recovered bool
	// Result is the tool's output string when Recovered is true.
	Result string
	// Candidate holds a corrected selector that the caller can
	// propose to Reviewer when the recovery found a likely match
	// but the caller wants Reviewer confirmation before executing.
	// Empty when Recovered is true (already executed) or when no
	// candidate was found.
	Candidate string
	// CandidateStrategy is the strategy to use with Candidate.
	CandidateStrategy SelectStrategy
	// Err is non-nil when the recovery found a candidate and tried
	// to execute it, but the execution itself failed.
	Err error
}

// SelectorRecoveryError is a sentinel error returned by the browser
// tool executor when a selector fails and recovery is possible. The
// loop inspects this error to decide whether to invoke LLM-assisted
// recovery (Phase 3.3) or propose a deterministic candidate to
// Reviewer.
//
// This type implements the error interface so it can flow through
// the normal error path, but the loop uses errors.As to detect it
// specifically.
type SelectorRecoveryError struct {
	Failure     SelectorFailure
	Candidate   string         // non-empty when deterministic recovery found a match
	Strategy    SelectStrategy // strategy for Candidate
	Phase       string         // "deterministic" or "model"
	OriginalErr error          // the original Playwright error
}

func (e *SelectorRecoveryError) Error() string {
	if e.Candidate != "" {
		return fmt.Sprintf("selector %q [%s] failed (%s); recovery suggests %q [%s] (phase: %s): %v",
			e.Failure.Selector, e.Failure.Strategy, e.Failure.Type,
			e.Candidate, e.Strategy, e.Phase, e.OriginalErr)
	}
	return fmt.Sprintf("selector %q [%s] failed (%s); %s recovery needed: %v",
		e.Failure.Selector, e.Failure.Strategy, e.Failure.Type,
		e.Phase, e.OriginalErr)
}

// AttemptDeterministicRecovery tries to fix a selector failure using
// plain DOM inspection — no model call. The recovery differs by failure
// type:
//
//   - ZeroMatch: re-queries the page for elements with similar
//     text/role/aria-label to what was originally requested.
//   - AmbiguousMatch: attempts to narrow the match using visible
//     text content (Playwright's .filter({hasText}) pattern).
//
// Returns nil when the failure type is not recoverable or when no
// suitable candidate was found.
func (m *Manager) AttemptDeterministicRecovery(failure *SelectorFailure) *RecoveryResult {
	if failure == nil {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensureLaunched(); err != nil {
		return nil
	}

	switch failure.Type {
	case FailureZeroMatch:
		return m.recoverZeroMatch(failure)
	case FailureAmbiguousMatch:
		return m.recoverAmbiguousMatch(failure)
	default:
		return nil
	}
}

// recoverZeroMatch implements the zero-match recovery path.
// It queries the page for elements with text/role/aria-label similar
// to what Coder originally requested, and if a good-enough match is
// found, attempts the original action against it.
func (m *Manager) recoverZeroMatch(failure *SelectorFailure) *RecoveryResult {
	candidates := m.findSimilarElements(failure.Selector, failure.Strategy)
	if len(candidates) == 0 {
		return nil
	}

	// Use the best candidate. We pick the first one because
	// findSimilarElements returns them sorted by relevance
	// (exact match first, then substring, then word overlap).
	best := candidates[0]

	// Try executing the original action with the candidate.
	result, err := m.executeRecoveredAction(failure, best.Text, best.Strategy)
	if err != nil {
		// The candidate didn't work either. Return it as a suggestion
		// for Reviewer rather than giving up entirely.
		return &RecoveryResult{
			Recovered:         false,
			Candidate:         best.Text,
			CandidateStrategy: best.Strategy,
			Err:               err,
		}
	}

	return &RecoveryResult{
		Recovered: true,
		Result:    result,
	}
}

// recoverAmbiguousMatch implements the ambiguous-match recovery path.
// When a selector matched multiple elements (strict-mode violation),
// this attempts to narrow by finding an element with exact visible
// text matching the selector's intent.
//
// IMPORTANT: This method must be called with m.mu already held.
func (m *Manager) recoverAmbiguousMatch(failure *SelectorFailure) *RecoveryResult {
	// Strategy 1: Try to find a single element with exact text match
	// among the ambiguous set. This mirrors Playwright's
	// .filter({hasText: exact}) disambiguation pattern.
	exactMatch := m.findExactTextMatch(failure.Selector, failure.Strategy)
	if exactMatch != "" {
		result, err := m.executeRecoveredAction(failure, exactMatch, StrategyText)
		if err == nil {
			return &RecoveryResult{
				Recovered: true,
				Result:    result,
			}
		}
		// Exact text match didn't work as a selector — fall through.
	}

	// Strategy 2: If the original was a role-based selector, try
	// narrowing by finding a surrounding context (nearest heading
	// or parent section) that uniquely identifies one of the
	// matches, then apply it via Locator.Filter({HasText: ...}).
	// This is the documented Playwright disambiguation pattern and
	// produces a real, working narrowed selector rather than the
	// same ambiguous one.
	if failure.Strategy == StrategyRole {
		narrowed := m.narrowRoleSelector(failure.Selector)
		if narrowed != nil && narrowed.HasText != "" {
			role, name, err := splitRoleName(failure.Selector)
			if err == nil {
				opts := playwright.PageGetByRoleOptions{Name: name}
				loc := m.page.GetByRole(playwright.AriaRole(role), opts)
				filtered := loc.Filter(playwright.LocatorFilterOptions{
					HasText: narrowed.HasText,
				})
				result, execErr := m.executeActionOnLocator(failure, filtered, narrowed)
				if execErr == nil {
					return &RecoveryResult{
						Recovered: true,
						Result:    result,
					}
				}
				// Filter didn't resolve to a unique element — fall
				// through to LLM-assisted recovery.
			}
		}
	}

	return nil
}

// executeActionOnLocator runs the original tool action against a
// pre-built Playwright Locator. Used by recoverAmbiguousMatch's
// role-narrowing path, where the corrected target is a filtered
// Locator (Locator.Filter(HasText: ...)) rather than a re-parsable
// selector+strategy string.
//
// IMPORTANT: This method must be called with m.mu already held.
func (m *Manager) executeActionOnLocator(failure *SelectorFailure, loc playwright.Locator, narrowed *narrowedRoleTarget) (string, error) {
	switch failure.ToolName {
	case "browser_click":
		if err := loc.Click(playwright.LocatorClickOptions{
			Timeout: playwright.Float(float64(DefaultTimeout.Milliseconds())),
		}); err != nil {
			return "", fmt.Errorf("browser_click: failed to click narrowed [%s] %q (filter=%q): %w",
				failure.Strategy, failure.Selector, narrowed.HasText, err)
		}
		return fmt.Sprintf("browser_click: clicked [%s] %q narrowed by %s=%q",
			failure.Strategy, failure.Selector, narrowed.Source, narrowed.HasText), nil

	case "browser_get_text":
		text, err := loc.InnerText(playwright.LocatorInnerTextOptions{
			Timeout: playwright.Float(float64(DefaultTimeout.Milliseconds())),
		})
		if err != nil {
			return "", fmt.Errorf("browser_get_text: failed to read narrowed [%s] %q (filter=%q): %w",
				failure.Strategy, failure.Selector, narrowed.HasText, err)
		}
		return fmt.Sprintf("browser_get_text: [%s] %q (narrowed by %s=%q) =\n%s",
			failure.Strategy, failure.Selector, narrowed.Source,
			narrowed.HasText, truncateResult(text, MaxResultBytes)), nil

	default:
		return "", fmt.Errorf("recovery: unsupported tool %q for narrowed-locator recovery", failure.ToolName)
	}
}

// ---------------------------------------------------------------------------
// DOM inspection helpers
// ---------------------------------------------------------------------------

// similarCandidate holds a potential recovery target found by scanning
// the page DOM.
type similarCandidate struct {
	Text     string         // visible text or accessible name
	Strategy SelectStrategy // which strategy would target this element
}

// findSimilarElements queries the page DOM for elements with
// text/role/aria-label similar to the failed selector. Returns
// candidates sorted by relevance (exact match first).
func (m *Manager) findSimilarElements(selector string, strategy SelectStrategy) []similarCandidate {
	// Extract the meaningful text from the selector depending on strategy.
	targetText := extractTargetText(selector, strategy)
	if targetText == "" {
		return nil
	}

	// Use JavaScript to scan the page DOM. This is cheaper than
	// making multiple Playwright locator calls — one JS evaluate
	// gives us all the information we need.
	js := `() => {
		const results = [];
		const allElements = document.querySelectorAll('*');
		for (const el of allElements) {
			if (el.offsetParent === null && el.tagName !== 'BODY') continue;
			const text = (el.innerText || '').trim();
			const label = el.getAttribute('aria-label') || '';
			const role = el.getAttribute('role') || el.tagName.toLowerCase();
			const title = el.getAttribute('title') || '';
			const placeholder = el.getAttribute('placeholder') || '';
			const alt = el.getAttribute('alt') || '';
			if (text || label || title || placeholder || alt) {
				results.push({
					text: text.substring(0, 200),
					label: label.substring(0, 200),
					role: role,
					title: title.substring(0, 200),
					placeholder: placeholder.substring(0, 200),
					alt: alt.substring(0, 200),
					tag: el.tagName.toLowerCase(),
				});
			}
		}
		return JSON.stringify(results);
	}`

	raw, err := m.page.Evaluate(js)
	if err != nil {
		return nil
	}

	rawStr, ok := raw.(string)
	if !ok {
		return nil
	}

	var elements []struct {
		Text        string `json:"text"`
		Label       string `json:"label"`
		Role        string `json:"role"`
		Title       string `json:"title"`
		Placeholder string `json:"placeholder"`
		Alt         string `json:"alt"`
		Tag         string `json:"tag"`
	}
	if err := json.Unmarshal([]byte(rawStr), &elements); err != nil {
		return nil
	}

	var candidates []similarCandidate
	for _, el := range elements {
		// Check each attribute for similarity to the target text.
		checks := []struct {
			value    string
			strategy SelectStrategy
		}{
			{el.Text, StrategyText},
			{el.Label, StrategyLabel},
			{el.Title, StrategyTitle},
			{el.Placeholder, StrategyPlaceholder},
			{el.Alt, StrategyAlt},
		}

		for _, check := range checks {
			if check.value == "" {
				continue
			}
			similarity := computeSimilarity(targetText, check.value)
			if similarity >= 0.6 { // threshold: 60% word overlap or substring
				candidates = append(candidates, similarCandidate{
					Text:     check.value,
					Strategy: check.strategy,
				})
			}
		}

		// Also check role-based match (for strategy=role).
		if strategy == StrategyRole && el.Role != "" {
			_, name := splitRoleNameFast(selector)
			if name != "" {
				similarity := computeSimilarity(name, el.Text)
				if similarity >= 0.6 {
					candidates = append(candidates, similarCandidate{
						Text:     fmt.Sprintf("%s:%s", el.Role, el.Text),
						Strategy: StrategyRole,
					})
				}
			}
		}
	}

	// Deduplicate and sort by relevance.
	return deduplicateCandidates(candidates, targetText)
}

// findExactTextMatch tries to find a single element with exact visible
// text that matches the selector's intent. Returns empty string if
// zero or multiple elements match (no disambiguation possible).
func (m *Manager) findExactTextMatch(selector string, strategy SelectStrategy) string {
	targetText := extractTargetText(selector, strategy)
	if targetText == "" {
		return ""
	}

	// Count how many visible elements contain this exact text.
	js := fmt.Sprintf(`() => {
		const target = %q;
		const allElements = document.querySelectorAll('*');
		let exactMatches = [];
		for (const el of allElements) {
			if (el.offsetParent === null && el.tagName !== 'BODY') continue;
			const text = (el.innerText || '').trim();
			if (text === target) {
				exactMatches.push({
					tag: el.tagName.toLowerCase(),
					text: text,
					role: el.getAttribute('role') || '',
				});
			}
		}
		return JSON.stringify(exactMatches);
	}`, targetText)

	raw, err := m.page.Evaluate(js)
	if err != nil {
		return ""
	}

	rawStr, ok := raw.(string)
	if !ok {
		return ""
	}

	var matches []struct {
		Tag  string `json:"tag"`
		Text string `json:"text"`
		Role string `json:"role"`
	}
	if err := json.Unmarshal([]byte(rawStr), &matches); err != nil {
		return ""
	}

	// Only disambiguate if there's exactly one exact match.
	if len(matches) == 1 {
		return matches[0].Text
	}

	return ""
}

// narrowedRoleTarget is the output of narrowRoleSelector. It carries
// the role selector to use together with a HasText filter to apply
// via Playwright's Locator.Filter — the documented pattern for
// disambiguating a role+name locator that matches multiple elements.
//
// Example: if "button:OK" matches three buttons, but only one of
// them is inside a section whose visible text is "Dialog", then
// narrowRoleSelector returns:
//
//   {Selector: "button:OK", HasText: "Dialog"}
//
// which the caller applies as
//   page.GetByRole("button", {name:"OK"}).Filter({HasText:"Dialog"})
type narrowedRoleTarget struct {
	Selector string // the role:name selector, unchanged
	HasText  string // a substring that uniquely identifies one of the matches
	// Source describes where HasText came from ("parent section",
	// "nearest heading", etc.). Useful for the human reading the
	// transcript; not used by the caller.
	Source string
}

// narrowRoleSelector attempts to narrow a role-based selector that
// matched multiple elements by finding a surrounding context
// (parent section text, nearest heading) that uniquely identifies
// one of the matches. The returned HasText is applied via
// Locator.Filter — the standard Playwright disambiguation pattern.
//
// Returns nil if no narrowing is possible (e.g. all matches live in
// the same parent with no distinguishing heading).
func (m *Manager) narrowRoleSelector(selector string) *narrowedRoleTarget {
	role, name := splitRoleNameFast(selector)
	if role == "" || name == "" {
		return nil
	}

	// Use JavaScript to find all elements with this role and gather
	// their surrounding context (parent section text + nearest heading).
	js := fmt.Sprintf(`() => {
		const role = %q;
		const name = %q;
		const candidates = [];
		// Query by role attribute or implicit ARIA role.
		const byRole = document.querySelectorAll('[role="' + role + '"]');
		// Also check semantic elements with implicit roles.
		const semanticTags = {
			'button': 'button', 'a': 'link', 'input': 'textbox',
			'select': 'combobox', 'textarea': 'textbox',
			'h1': 'heading', 'h2': 'heading', 'h3': 'heading',
			'h4': 'heading', 'h5': 'heading', 'h6': 'heading',
		};
		const allElements = [...byRole];
		for (const [tag, implicitRole] of Object.entries(semanticTags)) {
			if (implicitRole === role) {
				allElements.push(...document.querySelectorAll(tag));
			}
		}
		for (const el of allElements) {
			if (el.offsetParent === null) continue;
			const text = (el.innerText || '').trim();
			const label = el.getAttribute('aria-label') || '';
			const accessibleName = label || text;
			if (!accessibleName) continue;
			// Only consider elements whose accessible name contains
			// the requested name (substring match, matching the
			// GetByRole default).
			if (!(label === name || text === name ||
				  (label && label.indexOf(name) >= 0) ||
				  (text && text.indexOf(name) >= 0))) {
				continue;
			}
			// Find the nearest heading (h1..h6) above this element
			// — it usually gives a tight, human-meaningful context.
			let heading = '';
			let cur = el.parentElement;
			while (cur && !heading) {
				const prev = (cur.previousElementSibling ||
					(cur.parentElement ? cur.parentElement : null));
				// Walk previous siblings looking for a heading.
				let walker = cur.previousElementSibling;
				while (walker && !heading) {
					if (/^H[1-6]$/.test(walker.tagName)) {
						heading = (walker.innerText || '').trim();
						break;
					}
					walker = walker.previousElementSibling;
				}
				cur = cur.parentElement;
			}
			// Fallback: section-level text (truncated).
			const parentText = el.parentElement ?
				(el.parentElement.innerText || '').trim().substring(0, 100) : '';
			candidates.push({
				text: text.substring(0, 200),
				label: label.substring(0, 200),
				heading: heading.substring(0, 100),
				parentText: parentText,
			});
		}
		return JSON.stringify(candidates);
	}`, role, name)

	raw, err := m.page.Evaluate(js)
	if err != nil {
		return nil
	}

	rawStr, ok := raw.(string)
	if !ok {
		return nil
	}

	var candidates []struct {
		Text       string `json:"text"`
		Label      string `json:"label"`
		Heading    string `json:"heading"`
		ParentText string `json:"parentText"`
	}
	if err := json.Unmarshal([]byte(rawStr), &candidates); err != nil {
		return nil
	}

	// We need at least 2 candidates to disambiguate.
	if len(candidates) < 2 {
		return nil
	}

	// Strategy A: find a heading that is unique to ONE of the
	// matching elements. "button:OK" inside <h2>Dialog</h2> → use
	// "Dialog" as the disambiguator. This is the tightest, most
	// human-meaningful narrowing and is what reviewers / humans
	// reading the transcript most easily recognise.
	headingCounts := make(map[string]int)
	for _, c := range candidates {
		if c.Heading != "" {
			headingCounts[c.Heading]++
		}
	}
	for _, c := range candidates {
		if c.Heading != "" && headingCounts[c.Heading] == 1 {
			return &narrowedRoleTarget{
				Selector: selector,
				HasText:  c.Heading,
				Source:   "nearest heading",
			}
		}
	}

	// Strategy B: fall back to parentText (the section the element
	// lives in). Less tight than a heading but still meaningful —
	// it captures the containing card / dialog / form.
	parentCounts := make(map[string]int)
	for _, c := range candidates {
		if c.ParentText != "" {
			parentCounts[c.ParentText]++
		}
	}
	for _, c := range candidates {
		if c.ParentText != "" && parentCounts[c.ParentText] == 1 {
			return &narrowedRoleTarget{
				Selector: selector,
				HasText:  c.ParentText,
				Source:   "parent section",
			}
		}
	}

	// No unique context found — all matches live in identical
	// surrounding text. Caller should fall through to LLM-assisted
	// recovery.
	return nil
}

// executeRecoveredAction runs the original tool action against a
// recovered selector. It's a helper that avoids duplicating the
// click/type/get_text logic.
//
// IMPORTANT: This method must be called with m.mu already held.
// It calls the internal unlocked execution methods to avoid
// deadlock.
func (m *Manager) executeRecoveredAction(failure *SelectorFailure, recoveredSelector string, recoveredStrategy SelectStrategy) (string, error) {
	// Build the JSON arguments for the recovered action.
	args := map[string]interface{}{
		"selector": recoveredSelector,
		"strategy": string(recoveredStrategy),
	}

	// For type actions, we need to preserve the text field.
	if failure.ToolName == "browser_type" {
		// Extract the original text from the failure context.
		// We can't easily get this from the SelectorFailure alone,
		// so we return an error indicating the caller should handle
		// type recovery differently.
		return "", fmt.Errorf("type recovery requires original text — handled by caller")
	}

	rawArgs, err := json.Marshal(args)
	if err != nil {
		return "", fmt.Errorf("recovery: failed to marshal args: %w", err)
	}

	// Use internal unlocked methods since m.mu is already held.
	switch failure.ToolName {
	case "browser_click":
		return m.executeClickUnlocked(string(rawArgs))
	case "browser_get_text":
		return m.executeGetTextUnlocked(string(rawArgs))
	default:
		return "", fmt.Errorf("recovery: unsupported tool %q for recovery", failure.ToolName)
	}
}

// executeClickUnlocked is the internal version of ExecuteClick that
// does NOT acquire m.mu. It must be called with m.mu already held.
func (m *Manager) executeClickUnlocked(rawArgs string) (string, error) {
	var args ClickArgs
	if err := unmarshalArgs("browser_click", rawArgs, &args); err != nil {
		return "", err
	}
	if err := validateSelector("browser_click", args.Selector); err != nil {
		return "", err
	}
	if !validStrategy(args.Strategy) {
		return "", fmt.Errorf("browser_click: unknown strategy %q", args.Strategy)
	}
	if err := rejectPositionalCSS("browser_click", args.Strategy, args.Selector); err != nil {
		return "", err
	}

	loc, err := LocatorForStrategy(m.page, args.Strategy, args.Selector, "browser_click")
	if err != nil {
		return "", err
	}
	if err := loc.Click(playwright.LocatorClickOptions{
		Timeout: playwright.Float(float64(DefaultTimeout.Milliseconds())),
	}); err != nil {
		return "", fmt.Errorf("browser_click: failed to click [%s] %q: %w", args.Strategy, args.Selector, err)
	}
	return fmt.Sprintf("browser_click: clicked [%s] %q", args.Strategy, args.Selector), nil
}

// executeGetTextUnlocked is the internal version of ExecuteGetText that
// does NOT acquire m.mu. It must be called with m.mu already held.
func (m *Manager) executeGetTextUnlocked(rawArgs string) (string, error) {
	var args GetTextArgs
	if err := unmarshalArgs("browser_get_text", rawArgs, &args); err != nil {
		return "", err
	}
	selector := args.Selector
	if strings.TrimSpace(selector) == "" {
		selector = "body"
	} else if err := validateSelector("browser_get_text", selector); err != nil {
		return "", err
	}
	if !validStrategy(args.Strategy) {
		return "", fmt.Errorf("browser_get_text: unknown strategy %q", args.Strategy)
	}
	if err := rejectPositionalCSS("browser_get_text", args.Strategy, selector); err != nil {
		return "", err
	}

	loc, err := LocatorForStrategy(m.page, args.Strategy, selector, "browser_get_text")
	if err != nil {
		return "", err
	}
	text, err := loc.InnerText(playwright.LocatorInnerTextOptions{
		Timeout: playwright.Float(float64(DefaultTimeout.Milliseconds())),
	})
	if err != nil {
		return "", fmt.Errorf("browser_get_text: failed to read [%s] %q: %w", args.Strategy, selector, err)
	}
	return fmt.Sprintf("browser_get_text: [%s] %q =\n%s", args.Strategy, selector, truncateResult(text, MaxResultBytes)), nil
}

// ---------------------------------------------------------------------------
// Phase 3.3 — LLM-assisted recovery support
// ---------------------------------------------------------------------------

// PageContextForRecovery extracts a concise summary of the current
// page's interactive elements, text content, and structure. This is
// passed to the LLM as context when asking it to correct a failed
// selector.
//
// The output is deliberately short (capped at ~2KB) to keep the
// LLM prompt small — we only need enough context for the model to
// suggest a better selector, not a full page dump.
func (m *Manager) PageContextForRecovery() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensureLaunched(); err != nil {
		return "(page not available)"
	}

	js := `() => {
		const context = {
			url: window.location.href,
			title: document.title,
			interactive: [],
			headings: [],
			visibleText: '',
		};

		// Collect interactive elements (buttons, links, inputs).
		const interactives = document.querySelectorAll(
			'button, a[href], input, select, textarea, [role="button"], [role="link"], [role="textbox"]'
		);
		for (const el of interactives) {
			if (el.offsetParent === null && el.tagName !== 'BODY') continue;
			const text = (el.innerText || '').trim().substring(0, 100);
			const label = el.getAttribute('aria-label') || '';
			const role = el.getAttribute('role') || el.tagName.toLowerCase();
			const placeholder = el.getAttribute('placeholder') || '';
			const testid = el.getAttribute('data-testid') || '';
			const name = label || text || placeholder;
			if (name) {
				context.interactive.push({
					role: role,
					name: name.substring(0, 100),
					tag: el.tagName.toLowerCase(),
					testid: testid,
				});
			}
		}

		// Collect headings for page structure.
		const headings = document.querySelectorAll('h1, h2, h3');
		for (const h of headings) {
			const text = (h.innerText || '').trim();
			if (text) {
				context.headings.push(text.substring(0, 100));
			}
		}

		// Collect visible text (body, truncated).
		const bodyText = (document.body.innerText || '').trim();
		context.visibleText = bodyText.substring(0, 1000);

		// Cap the total output to ~2KB.
		let result = JSON.stringify(context);
		if (result.length > 2048) {
			result = result.substring(0, 2048) + '"}';
		}
		return result;
	}`

	raw, err := m.page.Evaluate(js)
	if err != nil {
		return fmt.Sprintf("(failed to extract page context: %v)", err)
	}

	rawStr, ok := raw.(string)
	if !ok {
		return "(page context extraction returned unexpected type)"
	}

	return rawStr
}

// ExecuteRecoveredAction executes a corrected action with the given
// selector and strategy. This is called after LLM-assisted recovery
// produces a corrected selector that has been approved by Reviewer.
func (m *Manager) ExecuteRecoveredAction(toolName, selector string, strategy SelectStrategy, originalArgs string) (string, error) {
	// Build new args preserving the original's non-selector fields.
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(originalArgs), &args); err != nil {
		args = make(map[string]interface{})
	}

	args["selector"] = selector
	if strategy != "" {
		args["strategy"] = string(strategy)
	}

	rawArgs, err := json.Marshal(args)
	if err != nil {
		return "", fmt.Errorf("recovery: failed to marshal corrected args: %w", err)
	}

	return m.ExecuteTool("", toolName, string(rawArgs))
}

// ---------------------------------------------------------------------------
// Text similarity helpers
// ---------------------------------------------------------------------------

// extractTargetText extracts the meaningful text from a selector
// based on the strategy. For role selectors, it extracts the name
// part after the colon. For text selectors, it strips the "exact:"
// prefix. For CSS selectors, it returns the selector as-is.
func extractTargetText(selector string, strategy SelectStrategy) string {
	switch strategy {
	case StrategyRole:
		_, name := splitRoleNameFast(selector)
		return name
	case StrategyText:
		text, _ := splitTextExact(selector)
		return text
	case StrategyLabel, StrategyPlaceholder, StrategyTitle, StrategyAlt:
		return selector
	case StrategyCSS, "":
		// For CSS selectors, try to extract meaningful text from
		// common patterns like "#submit-btn", ".login-button", etc.
		// Strip leading # or . and replace - with space.
		clean := strings.TrimLeft(selector, "#.")
		clean = strings.ReplaceAll(clean, "-", " ")
		clean = strings.ReplaceAll(clean, "_", " ")
		return clean
	default:
		return selector
	}
}

// computeSimilarity returns a score between 0.0 and 1.0 indicating
// how similar two strings are. Uses a combination of substring
// matching and word overlap for a fast, reasonable heuristic.
//
//   - 1.0: exact match
//   - >= 0.6: one is a substring of the other (with sufficient coverage),
//     or 60%+ word overlap
//   - 0.0: completely different or either input is empty
func computeSimilarity(a, b string) float64 {
	aLower := strings.ToLower(strings.TrimSpace(a))
	bLower := strings.ToLower(strings.TrimSpace(b))

	if aLower == bLower {
		if aLower == "" {
			return 0.0
		}
		return 1.0
	}

	if aLower == "" || bLower == "" {
		return 0.0
	}

	// Substring match — one contains the other. Score based on how
	// much of the longer string is covered by the shorter one. We
	// require at least 40% coverage to count as a substring match,
	// so a 7-char "Sign in" inside a 26-char "Sign in to your
	// account" doesn't get an inflated score.
	if strings.Contains(aLower, bLower) || strings.Contains(bLower, aLower) {
		shorter := len(aLower)
		longer := len(bLower)
		if longer < shorter {
			shorter, longer = longer, shorter
		}
		ratio := float64(shorter) / float64(longer)
		if ratio >= 0.4 {
			return ratio
		}
	}

	// Word overlap — split into words and compute Jaccard-ish similarity.
	wordsA := splitWords(aLower)
	wordsB := splitWords(bLower)

	if len(wordsA) == 0 || len(wordsB) == 0 {
		return 0.0
	}

	setA := make(map[string]bool, len(wordsA))
	for _, w := range wordsA {
		setA[w] = true
	}

	overlap := 0
	for _, w := range wordsB {
		if setA[w] {
			overlap++
		}
	}

	union := len(setA)
	for _, w := range wordsB {
		if !setA[w] {
			union++
		}
	}

	if union == 0 {
		return 0.0
	}

	return float64(overlap) / float64(union)
}

// splitWords splits a string into words, stripping non-alphanumeric
// characters. Fast, no regex.
func splitWords(s string) []string {
	var words []string
	var current []rune
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			current = append(current, r)
		} else if len(current) > 0 {
			words = append(words, string(current))
			current = nil
		}
	}
	if len(current) > 0 {
		words = append(words, string(current))
	}
	return words
}

// splitRoleNameFast is a faster version of splitRoleName that doesn't
// return errors — used internally by recovery logic where we've
// already validated the selector.
func splitRoleNameFast(selector string) (role, name string) {
	idx := strings.Index(selector, ":")
	if idx < 0 {
		return "", ""
	}
	role = strings.TrimSpace(selector[:idx])
	name = strings.TrimSpace(selector[idx+1:])
	return role, name
}

// deduplicateCandidates removes duplicate candidates and sorts them
// by relevance to the target text. Exact matches come first, then
// substring matches, then word-overlap matches.
func deduplicateCandidates(candidates []similarCandidate, targetText string) []similarCandidate {
	seen := make(map[string]bool)
	var unique []similarCandidate
	for _, c := range candidates {
		key := c.Text + "|" + string(c.Strategy)
		if seen[key] {
			continue
		}
		seen[key] = true
		unique = append(unique, c)
	}

	// Sort by similarity score (descending).
	targetLower := strings.ToLower(targetText)
	for i := 0; i < len(unique); i++ {
		for j := i + 1; j < len(unique); j++ {
			simI := computeSimilarity(targetLower, strings.ToLower(unique[i].Text))
			simJ := computeSimilarity(targetLower, strings.ToLower(unique[j].Text))
			if simJ > simI {
				unique[i], unique[j] = unique[j], unique[i]
			}
		}
	}

	return unique
}
