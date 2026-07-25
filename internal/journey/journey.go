// Package journey implements commit journey visualization for Triad (work.md §Phase 10).
// It queries git log for Triad-tagged commits, distinguishes main-loop from twin-subagent
// commits, and renders the chronological history as an ASCII timeline in the TUI or an exportable HTML file.
package journey

import (
	"bytes"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/kaiizer777/triad/internal/gitcommit"
)

// CommitType distinguishes main-loop auto-commits from twin-subagent auto-commits.
type CommitType string

const (
	CommitTypeMain CommitType = "main"
	CommitTypeTwin CommitType = "twin"
)

// JourneyEntry represents a single commit in the Triad commit journey.
type JourneyEntry struct {
	Hash       string     // Short git commit hash (e.g. "a1b2c3d")
	AuthorDate time.Time  // Commit timestamp
	Subject    string     // Raw commit subject line
	Intent     string     // Extracted action intent
	EntryID    int        // Associated transcript entry ID (0 if not present)
	TwinID     string     // Twin subagent ID if this is a twin commit (e.g. "sub_1")
	Type       CommitType // CommitTypeMain or CommitTypeTwin
}

// GetJourneyEntries queries git log in workDir and returns all Triad commits
// sorted in chronological order (oldest to newest).
//
// If validEntryIDs is non-empty, only commits matching one of those entry IDs
// (or twin commits associated with the session) are included.
func GetJourneyEntries(workDir string, validEntryIDs map[int]bool) ([]JourneyEntry, error) {
	if !gitcommit.IsRepo(workDir) {
		return nil, nil
	}

	// Format: %h (short hash) <TAB> %aI (strict ISO 8601 / RFC3339) <TAB> %s (subject)
	cmd := exec.Command("git", "log", "--pretty=format:%h\t%aI\t%s") //nolint:gosec
	cmd.Dir = workDir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		outStr := out.String()
		if strings.Contains(outStr, "does not have any commits") ||
			strings.Contains(outStr, "unknown revision") ||
			strings.Contains(outStr, "bad default revision") {
			return nil, nil
		}
		return nil, fmt.Errorf("journey: git log failed in %q: %w (output: %s)", workDir, err, outStr)
	}

	raw := out.String()
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}

	var entries []JourneyEntry
	lines := strings.Split(raw, "\n")

	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}

		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 3 {
			continue
		}

		hash := parts[0]
		isoDate := parts[1]
		subject := parts[2]

		// Must begin with [triad] marker or contain [triad:twin
		if !strings.HasPrefix(subject, gitcommit.CommitSubjectPrefix) && !strings.Contains(subject, "[triad:twin") {
			continue
		}

		t, parseErr := time.Parse(time.RFC3339, isoDate)
		if parseErr != nil {
			// Fallback parsing if RFC3339 fails
			t = time.Now()
		}

		entryID, hasID := parseEntryID(subject)
		if len(validEntryIDs) > 0 && hasID && !validEntryIDs[entryID] {
			continue
		}

		cType := CommitTypeMain
		twinID := ""
		if idx := strings.Index(subject, "[triad:twin #"); idx != -1 {
			cType = CommitTypeTwin
			rest := subject[idx+len("[triad:twin #"):]
			if closeIdx := strings.IndexByte(rest, ']'); closeIdx != -1 {
				twinID = rest[:closeIdx]
			}
		}

		intent := extractIntent(subject)

		entries = append(entries, JourneyEntry{
			Hash:       hash,
			AuthorDate: t,
			Subject:    subject,
			Intent:     intent,
			EntryID:    entryID,
			TwinID:     twinID,
			Type:       cType,
		})
	}

	// git log returns newest first. Reverse slice so history is chronological (oldest first).
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}

	return entries, nil
}

// RenderASCII builds a styled ASCII node-and-line timeline for display in the TUI viewport.
func RenderASCII(entries []JourneyEntry) string {
	if len(entries) == 0 {
		return "No Triad commit history recorded yet for this repository/session."
	}

	var (
		titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#A78BFA"))
		hashStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#67E8F9")).Bold(true)
		timeStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#5A7090")).Italic(true)
		descStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#E8EDF5"))
		lineStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#253348"))

		mainBadge = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#FFFFFF")).
				Background(lipgloss.Color("#2563EB")).
				Padding(0, 1)

		twinBadge = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#080C14")).
				Background(lipgloss.Color("#FCD34D")).
				Padding(0, 1)
	)

	var sb strings.Builder
	sb.WriteString(titleStyle.Render(fmt.Sprintf("Commit Journey (%d commits)", len(entries))))
	sb.WriteString("\n\n")

	for i, e := range entries {
		var badge string
		var nodeMarker string

		if e.Type == CommitTypeTwin {
			tag := "TWIN"
			if e.TwinID != "" {
				tag = "TWIN:#" + e.TwinID
			}
			badge = twinBadge.Render(tag)
			nodeMarker = lipgloss.NewStyle().Foreground(lipgloss.Color("#FCD34D")).Render("◆")
		} else {
			badge = mainBadge.Render("MAIN")
			nodeMarker = lipgloss.NewStyle().Foreground(lipgloss.Color("#3B82F6")).Render("●")
		}

		formattedTime := e.AuthorDate.Format("15:04:05")
		entryStr := ""
		if e.EntryID > 0 {
			entryStr = fmt.Sprintf(" entry #%d", e.EntryID)
		}

		line := fmt.Sprintf("  %s %s  %s  %s%s — %s",
			nodeMarker,
			badge,
			hashStyle.Render(e.Hash),
			timeStyle.Render(formattedTime),
			timeStyle.Render(entryStr),
			descStyle.Render(e.Intent),
		)
		sb.WriteString(line)
		sb.WriteString("\n")

		if i < len(entries)-1 {
			sb.WriteString(lineStyle.Render("  │"))
			sb.WriteString("\n")
		}
	}

	return sb.String()
}

// RenderHTML generates a standalone HTML document containing a responsive, modern timeline of the commit journey.
func RenderHTML(entries []JourneyEntry) string {
	mainCount := 0
	twinCount := 0
	for _, e := range entries {
		if e.Type == CommitTypeTwin {
			twinCount++
		} else {
			mainCount++
		}
	}

	var sb strings.Builder
	sb.WriteString(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Triad Commit Journey</title>
  <style>
    :root {
      --bg-color: #080c14;
      --card-bg: #0d1526;
      --border-color: #1e2d45;
      --text-main: #e8edf5;
      --text-muted: #8899b4;
      --accent-main: #3b82f6;
      --accent-twin: #fcd34d;
      --accent-twin-bg: #261a08;
      --accent-hash: #67e8f9;
      --font-family: 'Inter', system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
    }
    * { box-sizing: border-box; margin: 0; padding: 0; }
    body {
      background-color: var(--bg-color);
      color: var(--text-main);
      font-family: var(--font-family);
      line-height: 1.6;
      padding: 2rem 1rem;
      max-width: 900px;
      margin: 0 auto;
    }
    header {
      margin-bottom: 2.5rem;
      border-bottom: 1px solid var(--border-color);
      padding-bottom: 1.5rem;
    }
    h1 {
      font-size: 2rem;
      font-weight: 700;
      color: #ffffff;
      margin-bottom: 0.5rem;
      display: flex;
      align-items: center;
      gap: 0.75rem;
    }
    h1 span.logo {
      background: linear-gradient(135deg, #8b5cf6, #3b82f6);
      color: #fff;
      padding: 0.2rem 0.6rem;
      border-radius: 6px;
      font-size: 1.2rem;
    }
    .stats-bar {
      display: flex;
      gap: 1.5rem;
      margin-top: 1rem;
      font-size: 0.9rem;
    }
    .stat-item {
      background-color: var(--card-bg);
      border: 1px solid var(--border-color);
      padding: 0.5rem 1rem;
      border-radius: 8px;
      display: flex;
      align-items: center;
      gap: 0.5rem;
    }
    .stat-value { font-weight: 700; color: #ffffff; }
    .timeline {
      position: relative;
      padding-left: 2.5rem;
    }
    .timeline::before {
      content: '';
      position: absolute;
      left: 1rem;
      top: 0;
      bottom: 0;
      width: 2px;
      background-color: var(--border-color);
    }
    .timeline-item {
      position: relative;
      margin-bottom: 1.5rem;
    }
    .timeline-item:last-child { margin-bottom: 0; }
    .node {
      position: absolute;
      left: -2.5rem;
      top: 0.3rem;
      width: 1.2rem;
      height: 1.2rem;
      border-radius: 50%;
      background-color: var(--bg-color);
      border: 3px solid var(--accent-main);
      box-shadow: 0 0 10px rgba(59, 130, 246, 0.4);
      z-index: 1;
    }
    .timeline-item.twin .node {
      border-color: var(--accent-twin);
      box-shadow: 0 0 10px rgba(252, 211, 77, 0.4);
    }
    .card {
      background-color: var(--card-bg);
      border: 1px solid var(--border-color);
      border-radius: 10px;
      padding: 1rem 1.25rem;
      transition: transform 0.15s ease, border-color 0.15s ease;
    }
    .card:hover {
      border-color: #3b82f6;
      transform: translateY(-1px);
    }
    .card-header {
      display: flex;
      justify-content: space-between;
      align-items: center;
      margin-bottom: 0.5rem;
      flex-wrap: wrap;
      gap: 0.5rem;
    }
    .badge {
      font-size: 0.75rem;
      font-weight: 700;
      padding: 0.2rem 0.6rem;
      border-radius: 4px;
      text-transform: uppercase;
      letter-spacing: 0.5px;
    }
    .badge-main { background-color: #2563eb; color: #ffffff; }
    .badge-twin { background-color: var(--accent-twin); color: #080c14; }
    .hash {
      font-family: monospace;
      color: var(--accent-hash);
      font-weight: 600;
      font-size: 0.9rem;
      background: #111827;
      padding: 0.1rem 0.4rem;
      border-radius: 4px;
    }
    .time-info {
      font-size: 0.85rem;
      color: var(--text-muted);
    }
    .intent {
      font-size: 1rem;
      color: var(--text-main);
      word-break: break-word;
    }
    .empty-state {
      text-align: center;
      padding: 3rem 1rem;
      background-color: var(--card-bg);
      border: 1px dashed var(--border-color);
      border-radius: 12px;
      color: var(--text-muted);
    }
  </style>
</head>
<body>
  <header>
    <h1><span class="logo">TRIAD</span> Commit Journey</h1>
    <div class="stats-bar">
      <div class="stat-item">Total Commits: <span class="stat-value">`)
	sb.WriteString(strconv.Itoa(len(entries)))
	sb.WriteString(`</span></div>
      <div class="stat-item">Main Loop: <span class="stat-value">`)
	sb.WriteString(strconv.Itoa(mainCount))
	sb.WriteString(`</span></div>
      <div class="stat-item">Twin Subagents: <span class="stat-value">`)
	sb.WriteString(strconv.Itoa(twinCount))
	sb.WriteString(`</span></div>
    </div>
  </header>
  <main>
`)

	if len(entries) == 0 {
		sb.WriteString(`    <div class="empty-state">
      <h3>No commits found</h3>
      <p>No Triad auto-commits have been created in this repository yet.</p>
    </div>
`)
	} else {
		sb.WriteString(`    <div class="timeline">
`)
		for _, e := range entries {
			isTwin := e.Type == CommitTypeTwin
			itemClass := "timeline-item"
			badgeClass := "badge-main"
			badgeLabel := "MAIN LOOP"

			if isTwin {
				itemClass = "timeline-item twin"
				badgeClass = "badge-twin"
				if e.TwinID != "" {
					badgeLabel = "TWIN SUBAGENT #" + html.EscapeString(e.TwinID)
				} else {
					badgeLabel = "TWIN SUBAGENT"
				}
			}

			sb.WriteString(fmt.Sprintf(`      <div class="%s">
        <div class="node"></div>
        <div class="card">
          <div class="card-header">
            <div>
              <span class="badge %s">%s</span>
              <span class="hash">%s</span>
            </div>
            <div class="time-info">%s</div>
          </div>
          <div class="intent">%s</div>
        </div>
      </div>
`,
				itemClass,
				badgeClass,
				badgeLabel,
				html.EscapeString(e.Hash),
				html.EscapeString(e.AuthorDate.Format("2006-01-02 15:04:05")),
				html.EscapeString(e.Intent),
			))
		}
		sb.WriteString(`    </div>
`)
	}

	sb.WriteString(`  </main>
</body>
</html>
`)
	return sb.String()
}

// ExportHTML generates the HTML report and writes it to a file in workDir.
func ExportHTML(workDir, filename string, entries []JourneyEntry) (string, error) {
	if filename == "" {
		filename = "journey_report.html"
	}
	outPath := filepath.Join(workDir, filename)
	htmlContent := RenderHTML(entries)

	if err := os.WriteFile(outPath, []byte(htmlContent), 0644); err != nil {
		return "", fmt.Errorf("journey: failed to write HTML export to %q: %w", outPath, err)
	}

	return outPath, nil
}

// Helpers

func parseEntryID(subject string) (int, bool) {
	idx := strings.Index(subject, "entry #")
	if idx == -1 {
		return 0, false
	}
	rest := subject[idx+len("entry #"):]
	var id int
	n, err := fmt.Sscanf(rest, "%d", &id)
	if err != nil || n != 1 {
		return 0, false
	}
	return id, true
}

func extractIntent(subject string) string {
	// Subject format: "[triad] entry #12: <intent>" or "[triad:twin #id] tool: arg"
	colonIdx := strings.Index(subject, ": ")
	if colonIdx != -1 {
		return strings.TrimSpace(subject[colonIdx+2:])
	}
	return subject
}
