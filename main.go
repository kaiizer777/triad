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
	"github.com/kaiizer777/triad/internal/transcript"
	"github.com/kaiizer777/triad/internal/tui"
)

func main() {
	// --- Parse CLI flags ---
	sessionFlag := flag.String("session", "", "Path to a specific session file (.jsonl) to load/resume")
	resumeFlag := flag.Bool("resume", false, "Resume the most recent session from the sessions directory")
	flag.Parse()

	// --- Initialise file-based debug logger ---
	// Must happen before starting the TUI since bubbletea owns stdout/stderr.
	// All subsequent log output goes to triad.log in the working directory.
	if err := logger.Init("triad.log"); err != nil {
		// Logger init failure is non-fatal: we just lose debug output.
		// Print to stderr because TUI hasn't started yet.
		fmt.Fprintf(os.Stderr, "Warning: could not initialise debug log: %v\n", err)
	}
	defer logger.Close()

	// --- Load config ---
	cfg, err := agent.LoadConfig("config.yaml")
	if err != nil {
		logger.L().Error("failed to load config", "error", err.Error())
		log.Fatalf("Failed to load config: %v", err)
	}

	apiKey := cfg.Coder.APIKey
	if apiKey == "" {
		apiKey = os.Getenv("OPENCODE_API_KEY")
	}
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "Error: OPENCODE_API_KEY is not set and not in config.yaml.")
		fmt.Fprintln(os.Stderr, "Set the environment variable or add api_key to config.yaml.")
		os.Exit(1)
	}
	// Ensure the API key is available in both agent configs.
	cfg.Coder.APIKey = apiKey
	cfg.Reviewer.APIKey = apiKey

	// Derive the command timeout from config.
	commandTimeout := time.Duration(cfg.CommandTimeoutSeconds) * time.Second
	if commandTimeout <= 0 {
		commandTimeout = agent.DefaultCommandTimeout
	}

	logger.L().Info("triad starting",
		"base_url", cfg.Coder.BaseURL,
		"model", cfg.Coder.Model,
		"command_timeout", commandTimeout.String(),
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

	logger.L().Info("session ready", "path", sessionPath, "entries", len(tr.Entries()))

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

	// --- Create TUI Model ---
	model := tui.NewModel(tr, cfg.Coder, cfg.Reviewer, client, workDir, commandTimeout, cmdReg)

	// --- Browser manager (docs/work2.md §4.2) ---
	// The TUI owns the long-lived Playwright process. Browser launch
	// is lazy — the first browser_navigate call will trigger it —
	// so creating the manager here is cheap and never fails. If
	// the Chromium binary isn't installed, that surfaces as a clear
	// error on the first browser_* tool call rather than crashing
	// at startup.
	//
	// We also do a one-line startup probe (rough edge #8 in the
	// final project summary) so the user gets a friendly notice
	// at session start, not on their first browser_navigate call.
	// The probe is a best-effort filesystem check; it does not
	// launch Chromium and adds no startup latency.
	if !browser.IsChromiumInstalled() {
		fmt.Fprintln(os.Stderr,
			"Browser tools enabled, but Chromium isn't installed — run `playwright install chromium` to use browser_* tools.")
	}
	browserMgr := browser.NewManager()
	model.SetBrowser(browserMgr)
	defer func() {
		if err := browserMgr.Close(); err != nil {
			logger.L().Warn("browser: failed to close cleanly", "error", err.Error())
		}
	}()

	// --- Run Bubbletea program ---
	p := tea.NewProgram(model)

	// Handle OS signals: kill the TUI program cleanly.
	// Append already writes every entry atomically and immediately, so no
	// additional SaveToFile is needed here. SaveToFile (full truncate-rewrite)
	// would race with any concurrent Append still in flight.
	go func() {
		<-sigCh
		logger.L().Info("received OS signal, shutting down")
		p.Kill()
	}()

	if _, err := p.Run(); err != nil {
		logger.L().Error("TUI program exited with error", "error", err.Error())
		log.Fatalf("Error running TUI program: %v", err)
	}

	logger.L().Info("triad exited cleanly")
}
