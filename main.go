package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/kaiizer777/triad/internal/agent"
	"github.com/kaiizer777/triad/internal/browser"
	"github.com/kaiizer777/triad/internal/commands"
	"github.com/kaiizer777/triad/internal/gitcommit"
	"github.com/kaiizer777/triad/internal/logger"
	"github.com/kaiizer777/triad/internal/memory"
	"github.com/kaiizer777/triad/internal/skills"
	"github.com/kaiizer777/triad/internal/tracelog"
	"github.com/kaiizer777/triad/internal/transcript"
	"github.com/kaiizer777/triad/internal/tui"
)

func main() {
	// --- Parse CLI flags ---
	sessionFlag := flag.String("session", "", "Path to a specific session file (.jsonl) to load/resume")
	resumeFlag := flag.Bool("resume", false, "Resume the most recent session from the sessions directory")
	browserFlag := flag.String("browser", "", `Browser mode override: "real" uses your installed Chrome with visible window and real logins (equivalent to browser_mode: real_chrome in config.yaml)`)
	flag.Parse()

	// --- Load config ---
	const configPath = "config.yaml"
	cfg, err := agent.LoadConfig(configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// --- Initialise file-based debug logger ---
	// Must happen before starting the TUI since bubbletea owns stdout/stderr.
	// All subsequent log output goes to triad.log in the working directory.
	if err := logger.InitWithOptions("triad.log", logger.Options{
		MaxBytes:   cfg.LogMaxBytes,
		MaxBackups: cfg.LogMaxBackups,
	}); err != nil {
		// Logger init failure is non-fatal: we just lose debug output.
		// Print to stderr because TUI hasn't started yet.
		fmt.Fprintf(os.Stderr, "Warning: could not initialise debug log: %v\n", err)
	}
	defer logger.Close()

	// Active provider is now the single source of truth for
	// base_url / api_key. The cfg.Coder / cfg.Reviewer blocks
	// were already populated from the active provider inside
	// LoadConfig, so we just verify the key is non-empty here
	// (env-var fallback is honored for legacy single-provider
	// setups, just like before).
	apiKey := cfg.Coder.APIKey
	if apiKey == "" {
		apiKey = os.Getenv("OPENCODE_API_KEY")
	}
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "Error: no API key configured for the active provider.")
		fmt.Fprintln(os.Stderr, "Set providers.<active_provider>.api_key in config.yaml, or")
		fmt.Fprintln(os.Stderr, "set the OPENCODE_API_KEY environment variable for the legacy single-provider setup.")
		os.Exit(1)
	}
	cfg.Coder.APIKey = apiKey
	cfg.Reviewer.APIKey = apiKey

	// Derive the command timeout from config.
	commandTimeout := time.Duration(cfg.CommandTimeoutSeconds) * time.Second
	if commandTimeout <= 0 {
		commandTimeout = agent.DefaultCommandTimeout
	}
	gitGCEnabled := cfg.GitGCAutoEnabled == nil || *cfg.GitGCAutoEnabled
	gitcommit.ConfigureGCHygiene(gitcommit.GCHygieneConfig{
		Enabled:        gitGCEnabled,
		CommitInterval: cfg.GitGCCommitInterval,
	})

	logger.L().Info("triad starting",
		"active_provider", cfg.ActiveProvider,
		"base_url", cfg.Coder.BaseURL,
		"model", cfg.Coder.Model,
		"reasoning_level", cfg.Coder.ReasoningLevel,
		"thinking_mode", cfg.Coder.ThinkingMode,
		"command_timeout", commandTimeout.String(),
		"git_gc_auto_enabled", gitGCEnabled,
		"git_gc_commit_interval", cfg.GitGCCommitInterval,
	)

	// --- Session setup & transcript loading ---
	var tr *transcript.Transcript
	var sessionPath string

	_ = os.MkdirAll("sessions", 0755)

	if *sessionFlag != "" {
		sessionPath = *sessionFlag
		if filepath.Ext(sessionPath) != ".jsonl" {
			log.Fatalf("session file must have .jsonl extension, got: %s", sessionPath)
		}
		if _, err := os.Stat(sessionPath); err == nil {
			loadedTr, err := transcript.LoadFromFile(sessionPath)
			if err != nil {
				log.Fatalf("Failed to load session file %s: %v", sessionPath, err)
			}
			tr = loadedTr
		} else {
			tr = transcript.NewTranscript(sessionPath)
		}
	} else if *resumeFlag {
		latestPath, err := transcript.FindLatestSession("sessions")
		if err != nil {
			sessionID := fmt.Sprintf("session_%s", time.Now().Format("20060102_150405"))
			sessionPath = filepath.Join("sessions", sessionID+".jsonl")
			tr = transcript.NewTranscript(sessionPath)
		} else {
			loadedTr, err := transcript.LoadFromFile(latestPath)
			if err != nil {
				log.Fatalf("Failed to resume latest session file %s: %v", latestPath, err)
			}
			tr = loadedTr
			sessionPath = latestPath
		}
	} else {
		sessionID := fmt.Sprintf("session_%s", time.Now().Format("20060102_150405"))
		sessionPath = filepath.Join("sessions", sessionID+".jsonl")
		tr = transcript.NewTranscript(sessionPath)
	}

	cleanup, err := transcript.CleanupSessions(
		"sessions",
		time.Duration(cfg.SessionRetentionDays)*24*time.Hour,
		sessionPath,
		tracelog.TracePathForSession(sessionPath),
	)
	if err != nil {
		logger.L().Warn("session retention cleanup failed", "error", err.Error())
	} else if cleanup.ArchivedCount() > 0 {
		logger.L().Info("archived expired session artifacts", "count", cleanup.ArchivedCount())
	}

	logger.L().Info("session ready", "path", sessionPath, "entries", len(tr.Entries()))
	tracePath := tracelog.TracePathForSession(sessionPath)

	// --- Flush transcript on OS signal (SIGINT / SIGTERM) ---
	// Each Append already writes immediately, but an explicit SaveToFile on signal
	// ensures the full in-memory state is safely written before process exit.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// --- Create agent client ---
	client := agent.NewClient(60 * time.Second)
	workDir, err := os.Getwd()
	if err != nil {
		log.Fatalf("Failed to get working directory: %v", err)
	}
	memoryMgr, err := memory.NewManager(workDir)
	if err == nil {
		memoryMgr.WithTracePath(tracePath)
	}
	if err != nil {
		logger.L().Warn("daily-log retention cleanup unavailable", "error", err.Error())
	} else {
		cleanup, cleanupErr := memoryMgr.CleanupDailyLogs(time.Duration(cfg.SessionRetentionDays) * 24 * time.Hour)
		if cleanupErr != nil {
			logger.L().Warn("daily-log retention cleanup failed", "error", cleanupErr.Error())
		} else if cleanup.ArchivedCount() > 0 {
			logger.L().Info("archived expired daily memory logs", "count", cleanup.ArchivedCount())
		}
		
		idxStat, _ := os.Stat(filepath.Join(workDir, "memory", "INDEX.md"))
		prefStat, _ := os.Stat(filepath.Join(workDir, "memory", "preferences.md"))
		var idxSize, prefSize int64
		if idxStat != nil { idxSize = idxStat.Size() }
		if prefStat != nil { prefSize = prefStat.Size() }
		
		_ = tracelog.Append(tracePath, tracelog.Entry{
			Entity:      "memory",
			EventType:   tracelog.EventMemoryLoaded,
			Description: fmt.Sprintf("Loaded memory seeds (INDEX.md: %d bytes, preferences.md: %d bytes)", idxSize, prefSize),
			Data: map[string]any{
				"index_bytes": idxSize,
				"pref_bytes":  prefSize,
			},
		})
	}

	// --- Ensure workDir is a git repository (docs/work2.md §2.2.1) ---
	// Auto-commit-on-every-edit depends on git. If the project isn't a
	// repo yet, initialise one and surface a clear one-time notice in
	// the log. The TUI will additionally write a System entry to the
	// first session so the human sees it inline.
	if err := gitcommit.EnsureRepo(workDir); err != nil {
		if _, ok := err.(gitcommit.ErrAlreadyRepo); ok {
			logger.L().Info("git repo already initialised", "workDir", workDir)
		} else {
			logger.L().Warn("git init failed; auto-commit will be disabled for this session",
				"workDir", workDir, "error", err.Error())
			fmt.Fprintf(os.Stderr, "Warning: could not initialise git repository: %v\n", err)
			fmt.Fprintln(os.Stderr, "Auto-commit on every edit will be skipped this session.")
		}
	}

	// --- Load slash command registry ---
	// If the commands/ directory is missing or unreadable, fall back to an
	// empty registry — slash commands are a quality-of-life feature, not a
	// hard dependency for the session to start.
	cmdReg, err := commands.Load("commands")
	if err != nil {
		logger.L().Warn("commands: failed to load registry, continuing with none", "error", err.Error())
		cmdReg = &commands.Registry{}
	}
	logger.L().Info("slash commands ready", "count", cmdReg.Count(), "names", cmdReg.Names())

	// --- Load skills registry (Workflow 5) ---
	// Same defensive pattern as commands/: if the skills/ directory is
	// missing or unreadable, fall back to an empty registry — skills
	// are an opt-in capability, not a hard dependency. The funnel
	// becomes a no-op when the registry is empty (Coder sees no
	// SELECTED_SECTIONS scaffold and works with the base prompt
	// alone).
	skillsReg, skillsErr := skills.Load("skills")
	if skillsErr != nil {
		logger.L().Warn("skills: failed to load registry, continuing with none", "error", skillsErr.Error())
		skillsReg = &skills.Registry{}
	}
	logger.L().Info("skills loaded", "count", skillsReg.Count(), "sections", skillsReg.Sections())

	// --- Create TUI Model ---
	// Browser tools are always registered in this build (the manager is
	// unconditional below), so we extend the Coder / Reviewer system
	// prompts with the browser-selector guidance (Workflow 4 Phase 1).
	// If a future variant ever wants to skip browser tools, gate this
	// on the same flag the manager is gated on.
	cfg.Coder.SystemPrompt = agent.CoderSystemPromptWithBrowser()
	cfg.Reviewer.SystemPrompt = agent.ReviewerSystemPromptWithBrowser()

	model := tui.NewModel(tr, cfg.Coder, cfg.Reviewer, client, workDir, commandTimeout, cmdReg, configPath, cfg)
	if memoryMgr != nil {
		model.SetMemory(memoryMgr)
	}
	model.SetSearchAPIKey(cfg.SearchAPIKey)
	model.SetSkillsRegistry(skillsReg)

	// --- Browser manager ---
	// Mode selection priority: --browser flag > config browser_mode > default (headless).
	// "real" / "real_chrome" → connect to the user's actual Chrome via CDP.
	// Anything else          → launch Playwright's own hidden Chromium.
	useRealChrome := cfg.BrowserMode == "real_chrome"
	if *browserFlag == "real" || *browserFlag == "real_chrome" {
		useRealChrome = true
	}

	var browserMgr *browser.Manager
	if useRealChrome {
		if !browser.IsRealChromeAvailable() {
			fmt.Fprintln(os.Stderr,
				"Browser mode: real_chrome requested but Chrome not found. "+
					"Set CHROME_PATH or install Chrome, then retry.")
			os.Exit(1)
		}
		port := cfg.ChromeCDPPort
		fmt.Fprintf(os.Stderr, "Browser: real Chrome (CDP on port %d) — your Chrome window will open.\n", port)
		browserMgr = browser.NewRealChromeManager(port)
	} else {
		if !browser.IsChromiumInstalled() {
			fmt.Fprintln(os.Stderr,
				"Browser tools enabled, but Chromium isn't installed — run `playwright install chromium` to use browser_* tools.")
		}
		browserMgr = browser.NewManager()
	}
	model.SetBrowser(browserMgr)
	defer func() {
		if err := browserMgr.Close(); err != nil {
			logger.L().Warn("browser: failed to close cleanly", "error", err.Error())
		}
	}()

	// --- Run Bubbletea program ---
	// Bubble Tea v2 is declarative: alt screen and mouse mode are set on the
	// View struct (see internal/tui/view.go), NOT as NewProgram options.
	// The v1 options (tea.WithAltScreen, tea.WithMouseCellMotion) were
	// removed in v2 — the runtime's diff-based renderer handles enter/exit
	// automatically by comparing the last view's AltScreen/MouseMode fields
	// to the next view's. So the only thing left to do is let p.Run() return
	// normally and NOT call os.Exit / log.Fatalf from here on out — that
	// would skip the deferred terminal teardown and leave the shell stuck
	// in the alt screen + raw input mode.
	p := tea.NewProgram(model)

	// Handle OS signals: ask the TUI to quit so it can flush the alt
	// screen exit through the graceful path. p.Kill() is the hard kill
	// (tea.go:1204 → shutdown(true)) and was the reason the terminal was
	// staying in raw mode after Ctrl+C / window close.
	go func() {
		<-sigCh
		logger.L().Info("received OS signal, shutting down")
		p.Quit()
	}()

	if _, err := p.Run(); err != nil {
		// Log and exit cleanly. log.Fatalf calls os.Exit(1) which bypasses
		// Bubble Tea's shutdown() entirely, leaving the terminal in alt
		// screen + raw mode. Write the manual escape sequences so even a
		// hard error doesn't strand the user in a broken terminal.
		logger.L().Error("TUI program exited with error", "error", err.Error())
		fmt.Fprint(os.Stderr, "\x1b[?1049l") // leave alt screen
		fmt.Fprint(os.Stderr, "\x1b[?1003l") // disable mouse tracking
		fmt.Fprint(os.Stderr, "\x1b[?2004l") // disable bracketed paste
		fmt.Fprintln(os.Stderr, "Error running TUI program:", err)
		os.Exit(1)
	}

	logger.L().Info("triad exited cleanly")
}
