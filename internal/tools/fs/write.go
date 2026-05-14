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
	return "Writes a file to the local filesystem. Use this for creating new " +
		"files or fully overwriting an existing one. For partial edits to " +
		"an existing file, prefer edit_file — it preserves surrounding " +
		"content and is harder to misuse.\n\n" +
		"Overwriting an existing file requires you to have called " +
		"read_file on it first in this session — the tool refuses to " +
		"blindly clobber a file you haven't loaded into context. New " +
		"files (path doesn't exist) need no prior read. Missing parent " +
		"directories are created automatically.\n\n" +
		"On a new-file create the result is a unified diff against " +
		"/dev/null (so the TUI can render it in git-diff style); on an " +
		"overwrite the result is a byte-count confirmation."
}

func (t *WriteTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"required":["file_path","content"],
		"properties":{
			"file_path":{"type":"string","description":"Absolute or relative path to the file to write."},
			"content":{"type":"string","description":"Full text content to write to the file."}
		}
	}`)
}

type writeInput struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
	Encoding string `json:"encoding"`
}

func (t *WriteTool) Execute(_ context.Context, input json.RawMessage) (tools.Result, error) {
	var in writeInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tools.Result{IsError: true, Content: "write: decode input: " + err.Error()}, nil
	}
	if in.FilePath == "" {
		return tools.Result{IsError: true, Content: "write: file_path is required"}, nil
	}

	resolved, err := resolvePath(in.FilePath)
	if err != nil {
		return tools.Result{IsError: true, Content: "error: " + err.Error()}, nil
	}

	existedBefore := fileExists(resolved)

	// check read when overwrite case.
	if existedBefore && t.tracker != nil && !t.tracker.WasRead(resolved) {
		return tools.Result{
			IsError: true,
			Content: fmt.Sprintf(
				"error: you must use read_file on %s before overwriting it. "+
					"Read the file first so you don't blindly clobber existing "+
					"content; if you want a partial change use edit_file instead.",
				in.FilePath,
			),
		}, nil
	}

	// Create parent directories.
	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("error: could not create parent dirs: %s", err)}, nil
	}

	if err := os.WriteFile(resolved, []byte(in.Content), 0o644); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("error: could not write %s: %s", in.FilePath, err)}, nil
	}

	// Mark as read so subsequent edits succeed.
	if t.tracker != nil {
		t.tracker.MarkRead(resolved)
	}

	if !existedBefore {
		return tools.Result{Content: renderNewFileDiff(in.FilePath, in.Content)}, nil
	}
	return tools.Result{Content: fmt.Sprintf("wrote %d bytes to %s", len(in.Content), in.FilePath)}, nil
}

// renderNewFileDiff synthesizes a unified diff for a new file.
func renderNewFileDiff(path, content string) string {
	if content == "" {
		return fmt.Sprintf("--- /dev/null\n+++ b/%s\n", path)
	}
	lines := strings.Split(content, "\n")
	hasTrailingNewline := strings.HasSuffix(content, "\n")
	if hasTrailingNewline && len(lines) > 0 {
		lines = lines[:len(lines)-1]
	}
	n := len(lines)
	header := fmt.Sprintf("--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n", path, n)
	var body strings.Builder
	for _, line := range lines {
		body.WriteString("+" + line + "\n")
	}
	if !hasTrailingNewline {
		body.WriteString("\\ No newline at end of file\n")
	}
	return header + body.String()
}
