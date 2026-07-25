// Package twinsubagent — this file contains the §6.6 clarify-phase shim.
//
// RunClarifyPhase runs the shared Phase 3 clarify step in the twin pair's
// own isolated transcript. Because the twin pair is headless (spawned
// autonomously — no interactive stdin to wait on), it cannot pause and ask
// the human for clarification the way the main loop does. Instead, if the
// task is ambiguous, the twin:
//
//  1. Appends the formatted clarify block (the questions) as a [System] entry
//     into its own isolated transcript so a post-mortem reader can see what
//     was ambiguous.
//  2. Immediately appends the "proceeding with best-guess" proceed note as a
//     second [System] entry — the same note the main loop appends when the
//     human types /proceed.
//
// This satisfies the work.md §6.6 requirement ("Wire the Phase 3 clarify step
// in immediately after the twin pair receives Orchestrator's one-message
// handoff, before their own loop starts") while staying consistent with the
// Phase 0 principle that autonomous agents must not stall waiting for input
// they cannot receive.
//
// Clear tasks produce no transcript entries — the twin pair starts its loop
// immediately without any clarify overhead.
package twinsubagent

import (
	"time"

	"github.com/kaiizer777/triad/internal/clarify"
	"github.com/kaiizer777/triad/internal/transcript"
)

// RunClarifyPhase assesses task for ambiguity and, when ambiguous, appends
// the clarify block and an immediate proceed note to tr.
//
// nowFn is used for entry timestamps; pass nil to use time.Now (nil-safe).
//
// Returns the clarify.Batch that was assessed, whether or not it needed
// clarification, so callers can inspect it in tests.
func RunClarifyPhase(task string, tr *transcript.Transcript, nowFn func() time.Time) clarify.Batch {
	if nowFn == nil {
		nowFn = time.Now
	}
	batch := clarify.AssessAmbiguity(task)
	if !batch.NeedsClarification {
		return batch
	}

	// Append the formatted questions so the transcript records what was unclear.
	_ = tr.Append(transcript.Entry{
		Speaker:   transcript.SpeakerSystem,
		Type:      transcript.TypeMessage,
		Content:   clarify.FormatClarifyBlock(batch),
		Timestamp: nowFn(),
	})

	// Immediately append a proceed note — the twin pair is headless and cannot
	// wait for a human reply. This mirrors what the main loop does when the
	// human types /proceed.
	_ = tr.Append(transcript.Entry{
		Speaker:   transcript.SpeakerSystem,
		Type:      transcript.TypeMessage,
		Content:   clarify.FormatProceedNote(batch),
		Timestamp: nowFn(),
	})

	return batch
}
