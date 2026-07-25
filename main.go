package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/kaiizer777/triad/internal/agent"
	"github.com/kaiizer777/triad/internal/transcript"
	"github.com/kaiizer777/triad/internal/tui"
)

func main() {
	// --- Load config ---
	cfg, err := agent.LoadConfig("config.yaml")
	if err != nil {
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

	// --- Bind transcript to a session file ---
	sessionID := fmt.Sprintf("session_%s", time.Now().Format("20060102_150405"))
	_ = os.MkdirAll("sessions", 0755)
	sessionPath := filepath.Join("sessions", sessionID+".jsonl")
	tr := transcript.NewTranscript(sessionPath)

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

	// --- Create TUI Model ---
	model := tui.NewModel(tr, cfg.Coder, cfg.Reviewer, client, workDir)

	// --- Run Bubbletea program ---
	p := tea.NewProgram(model)

	// Handle OS signals in the background: flush transcript, then quit cleanly.
	go func() {
		<-sigCh
		_ = tr.SaveToFile(sessionPath)
		p.Kill()
	}()

	if _, err := p.Run(); err != nil {
		log.Fatalf("Error running TUI program: %v", err)
	}

	// Final flush on clean Ctrl+C / ESC exit through bubbletea's own quit path.
	_ = tr.SaveToFile(sessionPath)
}
