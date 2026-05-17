package fs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/johnny1110/evva/internal/tools"
)

// WriteTool creates or overwrites a file.
type WriteTool struct {
	tracker *ReadTracker
}

// NewWrite creates a WriteTool that enforces the read-before-overwrite guard
// via the given tracker.
func NewWrite(tracker *ReadTracker) *WriteTool {
	return &WriteTool{tracker: tracker}
}

func (t *WriteTool) Name() string { return string(tools.WRITE_FILE) }

func (t *WriteTool) Description() string {
	return "Writes a file to the local filesystem. file_path must be absolute. " +
		"Use this for creating new files or fully overwriting an existing " +
		"one. For partial edits to an existing file, prefer edit_file — it " +
		"preserves surrounding content and is harder to misuse.\n\n" +
		"Overwriting an existing file requires you to have called " +
		"read_file on it first in this session — the tool refuses to " +
		"blindly clobber a file you haven't loaded into context. New " +
		"files (path doesn't exist) need no prior read. Missing parent " +
		"directories are created automatically.\n\n" +
		"Never create documentation files (*.md) or README files unless " +
		"the user explicitly asked for them. Only use emojis if the user " +
		"explicitly requested them."
}

func (t *WriteTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"required":["file_path","content"],
		"properties":{
			"file_path":{"type":"string","description":"Absolute path to the file to write (must be absolute, not relative)."},
			"content":{"type":"string","description":"Full text content to write to the file."}
		}
	}`)
}

type writeInput struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
}

func (t *WriteTool) Execute(ctx context.Context, input json.RawMessage) (tools.Result, error) {
	var in writeInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tools.Result{IsError: true, Content: "write: decode input: " + err.Error()}, nil
	}

	resolved, err := resolvePath(in.FilePath)
	if err != nil {
		return tools.Result{IsError: true, Content: "write: " + err.Error()}, nil
	}

	existedBefore := fileExists(resolved)

	// Read-before-overwrite guard. New files are exempt.
	if existedBefore && t.tracker != nil && !t.tracker.WasRead(resolved) {
		return tools.Result{
			IsError: true,
			Content: fmt.Sprintf(
				"write: you must use read_file on %s before overwriting it. "+
					"Read the file first so you don't blindly clobber existing "+
					"content; if you want a partial change use edit_file instead.",
				in.FilePath,
			),
		}, nil
	}

	// Capture prior content. We need this for two things on the
	// overwrite path: the proposed diff (Metadata for the UI) and the
	// "was M / now N" summary line on the model-facing result.
	var oldByteCount, oldLineCount int
	var priorContent string
	if existedBefore {
		prior, perr := os.ReadFile(resolved)
		if perr == nil {
			priorContent = string(prior)
			oldByteCount = len(prior)
			oldLineCount = countLines(priorContent)
		}
	}

	// Build the proposed diff for the final tools.Result.Metadata. New
	// files render every line as an add; overwrites use difflib for a
	// minimal unified diff.
	var diff *FileDiff
	if existedBefore {
		diff = buildOverwriteDiff(resolved, priorContent, in.Content)
	} else {
		diff = buildCreateDiff(resolved, in.Content)
	}

	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("write: could not create parent dirs: %s", err)}, nil
	}
	if err := os.WriteFile(resolved, []byte(in.Content), 0o644); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("write: could not write %s: %s", in.FilePath, err)}, nil
	}

	if t.tracker != nil {
		t.tracker.MarkRead(resolved)
	}

	newLineCount := countLines(in.Content)
	newByteCount := len(in.Content)

	if !existedBefore {
		return tools.Result{
			Content:  fmt.Sprintf("created %s (%d lines, %d bytes)", resolved, newLineCount, newByteCount),
			Metadata: diff,
		}, nil
	}
	return tools.Result{
		Content: fmt.Sprintf("overwrote %s (was %d lines / %d bytes, now %d lines / %d bytes)",
			resolved, oldLineCount, oldByteCount, newLineCount, newByteCount),
		Metadata: diff,
	}, nil
}

// countLines counts the lines in s the way users count them — a final "\n"
// is the terminator of the last line, not a marker for an extra empty line.
// Empty string → 0 lines.
func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}
