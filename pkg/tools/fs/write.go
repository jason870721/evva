package fs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/johnny1110/evva/pkg/tools"
)

type WriteTool struct {
	tracker *ReadTracker
	workdir string
}

func NewWrite(tracker *ReadTracker, workdir string) *WriteTool {
	return &WriteTool{tracker: tracker, workdir: workdir}
}

func (t *WriteTool) Name() string { return string(tools.WRITE_FILE) }

func (t *WriteTool) Description() string {
	return `Writes a file to the local filesystem.

Usage:
- This tool will overwrite the existing file if there is one at the provided path.
- If this is an existing file, you MUST use the ` + "`read`" + ` tool first to read the file's contents. This tool will fail if you did not read the file first, or if the file's bytes changed on disk since the read. A truncated or offset read is fine.
- Prefer the Edit tool for modifying existing files — it only sends the diff. Only use this tool to create new files or for complete rewrites.
- NEVER create documentation files (*.md) or README files unless explicitly requested by the User.
- Only use emojis if the user explicitly requests it. Avoid writing emojis to files unless asked.
- Missing parent directories are created automatically. New files do not require a prior read.
- If the file already exists with a UTF-16 encoding (Windows / Notepad default with BOM), the new content is re-encoded as UTF-16 so the file's encoding is preserved. Line endings in the content you provide are written verbatim — write_file does NOT re-introduce CRLF.`
}

func (t *WriteTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"required":["file_path","content"],
		"properties":{
			"file_path":{"type":"string","description":"The absolute path to the file to write (must be absolute, not relative)."},
			"content":{"type":"string","description":"The content to write to the file."}
		}
	}`)
}

type writeInput struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
}

func (t *WriteTool) Execute(ctx context.Context, logger *slog.Logger, input json.RawMessage) (tools.Result, error) {
	var in writeInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tools.Result{IsError: true, Content: "write: missing required params: need file_path and content"}, nil
	}
	if in.FilePath == "" {
		return tools.Result{IsError: true, Content: "write: missing required param: file_path"}, nil
	}
	if in.Content == "" && !jsonFieldPresent(input, "content") {
		return tools.Result{IsError: true, Content: "write: missing required param: content"}, nil
	}
	logger.Debug("write.dispatch", "path", in.FilePath, "bytes", len(in.Content))

	resolved, err := resolvePath(in.FilePath, t.workdir)
	if err != nil {
		return tools.Result{IsError: true, Content: "write: " + err.Error()}, nil
	}

	priorInfo, statErr := os.Stat(resolved)
	existedBefore := statErr == nil && !priorInfo.IsDir()
	if statErr == nil && priorInfo.IsDir() {
		return tools.Result{IsError: true, Content: fmt.Sprintf("write: %s is a directory, not a regular file.", in.FilePath)}, nil
	}

	// Read prior content before the CanWrite check: it feeds the content-
	// hash staleness fallback (so a spurious mtime bump doesn't force a
	// re-read), preserves the original encoding on overwrite, and supplies
	// the before-image for the diff.
	//
	// Encoding detection: existing UTF-16 LE files (Notepad / Windows
	// default) must be re-encoded as UTF-16 on overwrite or the file
	// becomes unreadable in its original consumer. New files default
	// to UTF-8. Prior content is CRLF-normalized via readFileWithEncoding.
	enc := encUTF8
	var priorContent string
	if existedBefore {
		mem, merr := readFileWithEncoding(resolved)
		if merr == nil {
			enc = mem.enc
			priorContent = mem.content
		}
	}

	if existedBefore && t.tracker != nil {
		if ok, reason := t.tracker.CanWrite(resolved, priorInfo.ModTime(), HashContent(priorContent)); !ok {
			return tools.Result{
				IsError: true,
				Content: fmt.Sprintf("write: %s — path: %s", reason, in.FilePath),
			}, nil
		}
	}

	var diff *FileDiff
	if existedBefore {
		diff = buildOverwriteDiff(resolved, priorContent, in.Content)
	} else {
		diff = buildCreateDiff(resolved, in.Content)
	}

	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("write: could not create parent dirs: %s", err)}, nil
	}
	// TOCTOU guard: if the file changed on disk between the read above and
	// this overwrite, refuse so we don't clobber a concurrent modification.
	if existedBefore && fileChangedSince(resolved, priorInfo.ModTime()) {
		return tools.Result{
			IsError: true,
			Content: fmt.Sprintf("write: %s was modified after it was read (mtime advanced mid-write). Re-read it before overwriting. — path: %s", resolved, in.FilePath),
		}, nil
	}
	// Write is a full replacement: the model sent explicit line
	// endings and meant them. Do NOT restore CRLF even if the original
	// file used CRLF. Encoding (UTF-16 / UTF-8) IS preserved so the
	// file's consumer still recognizes it. Matches ref FileWriteTool
	// (writeTextContent(..., enc, 'LF')).
	if err := writeFileWithEncoding(resolved, in.Content, enc, false); err != nil {
		logger.Warn("write.fail", "path", in.FilePath, "err", err)
		return tools.Result{IsError: true, Content: fmt.Sprintf("write: could not write %s: %s", in.FilePath, err)}, nil
	}

	if t.tracker != nil {
		if newInfo, statErr := os.Stat(resolved); statErr == nil {
			t.tracker.Record(resolved, newInfo.ModTime(), false, HashContent(in.Content))
		} else {
			t.tracker.Forget(resolved)
		}
	}

	newLineCount := countLines(in.Content)
	newByteCount := len(in.Content)
	oldLineCount := countLines(priorContent)
	oldByteCount := len(priorContent)

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

func jsonFieldPresent(raw json.RawMessage, key string) bool {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return false
	}
	_, ok := m[key]
	return ok
}
