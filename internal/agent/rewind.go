package agent

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/ui"
)

// Checkpoints implements ui.Controller: the current session's rewind
// checkpoints, newest first. ChatRestoreOK is false once a full compaction has
// happened since a checkpoint was taken — the conversation cut-point then
// indexes a since-rewritten history, so only code-restore is safe to offer
// (rewind PRD §5.2; micro-compaction preserves indices and stays fine).
func (a *Agent) Checkpoints() []ui.CheckpointInfo {
	if a.IsSubagent() || a.checkpoints == nil {
		return nil
	}
	recs := a.checkpoints.List()
	curEpoch := a.session.GetFullCompactCount()
	out := make([]ui.CheckpointInfo, 0, len(recs))
	for _, r := range recs {
		out = append(out, ui.CheckpointInfo{
			ID:            strconv.Itoa(r.Seq),
			PromptPreview: r.PromptPreview,
			CreatedAt:     r.CreatedAt.UnixNano(),
			FileCount:     r.FileCount(),
			ChatRestoreOK: r.FullCompactCount == curEpoch,
		})
	}
	return out
}

// RestoreCheckpoint implements ui.Controller. See the interface doc for the
// mode semantics. Re-checks the running guard defensively — the TUI only opens
// /rewind at idle, but a code restore overwrites the working tree and a chat
// restore swaps the live history, so neither may race an in-flight Run.
func (a *Agent) RestoreCheckpoint(id, mode string) (string, error) {
	if a.IsSubagent() {
		return "", fmt.Errorf("agent: only the root agent can rewind")
	}
	if a.checkpoints == nil {
		return "", fmt.Errorf("agent: checkpointing is disabled")
	}
	if a.running.Load() {
		return "", ErrRunInProgress
	}

	wantCode := mode == "code" || mode == "both"
	wantChat := mode == "chat" || mode == "both"
	if !wantCode && !wantChat {
		return "", fmt.Errorf("agent: unknown rewind mode %q (want \"code\", \"chat\", or \"both\")", mode)
	}

	seq, err := strconv.Atoi(id)
	if err != nil {
		return "", fmt.Errorf("agent: invalid checkpoint id %q", id)
	}
	rec, err := a.checkpoints.Load(seq)
	if err != nil {
		return "", fmt.Errorf("agent: load checkpoint %s: %w", id, err)
	}

	var parts []string
	if wantCode {
		res := a.checkpoints.RestoreCode(rec)
		seg := fmt.Sprintf("restored %d file(s)", res.Restored)
		if res.Deleted > 0 {
			seg += fmt.Sprintf(", deleted %d", res.Deleted)
		}
		if len(res.Errors) > 0 {
			seg += fmt.Sprintf(", %d error(s)", len(res.Errors))
			for _, e := range res.Errors {
				a.logger.Warn("checkpoint.restore.file", "err", e)
			}
		}
		parts = append(parts, seg)
	}

	if wantChat {
		// Gate: a full compaction since the checkpoint rewrote Messages into a
		// brief, so the stored cut-point no longer indexes a real boundary.
		if rec.FullCompactCount != a.session.GetFullCompactCount() {
			if !wantCode {
				return "", fmt.Errorf("agent: conversation rewind unavailable for this checkpoint — the session was compacted after it was taken")
			}
			parts = append(parts, "conversation rewind skipped (compacted since checkpoint)")
		} else {
			n := a.rewindConversation(rec.CutLen)
			parts = append(parts, fmt.Sprintf("rewound conversation to %d message(s)", n))
		}
	}

	summary := strings.Join(parts, "; ")
	a.logger.Info("checkpoint.restore", "seq", seq, "mode", mode, "summary", summary)
	return summary, nil
}

// rewindConversation truncates the live session history to cutLen and persists
// the new state so a later /resume lands on the rewound transcript. Returns the
// resulting message count. Caller must hold no Run in flight.
func (a *Agent) rewindConversation(cutLen int) int {
	msgs := a.session.GetMessages()
	if cutLen < 0 {
		cutLen = 0
	}
	if cutLen > len(msgs) {
		cutLen = len(msgs)
	}
	truncated := make([]llm.Message, cutLen)
	copy(truncated, msgs[:cutLen])
	a.session.Messages = truncated
	a.persistSession()
	return cutLen
}
