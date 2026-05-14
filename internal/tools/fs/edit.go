package fs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/johnny1110/evva/internal/tools"
)

// EditTool performs exact string replacements in existing files.
type EditTool struct {
	tracker *ReadTracker
}

// NewEdit creates an EditTool that enforces the read-before-edit guard
// via the given tracker.
func NewEdit(tracker *ReadTracker) *EditTool {
	return &EditTool{tracker: tracker}
}

func (t *EditTool) Name() string { return string(tools.EDIT_FILE) }

func (t *EditTool) Description() string {
	return "Performs an exact string replacement in an existing file.\n\n" +
		"Workflow: (1) call read_file on the target file first — required, " +
		"the tool refuses to edit a file you haven't loaded into context; " +
		"(2) copy the exact text to replace as old_string (byte-for-byte, " +
		"tabs vs spaces matter, and DO NOT include the `<n>\\t` line-number " +
		"prefix from read_file output); (3) supply the replacement as " +
		"new_string.\n\n" +
		"By default old_string must occur exactly once in the file — if it " +
		"appears 0 times the tool errors and you should re-read the file; " +
		"if it appears multiple times the tool errors and you should " +
		"include more surrounding context to make it unique. Pass " +
		"replace_all=true to replace every occurrence at once (useful for " +
		"renaming a variable across the whole file).\n\n" +
		"old_string and new_string MUST differ. To create a new file or " +
		"fully overwrite one, use write_file instead. On success the tool " +
		"returns a unified diff so the TUI can render the change."
}

func (t *EditTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"required":["file_path","old_string","new_string"],
		"properties":{
			"file_path":{"type":"string","description":"Absolute or relative path to the file to edit."},
			"old_string":{"type":"string","description":"Exact text to find. Must match byte-for-byte including whitespace and newlines. Include enough surrounding context to make it unique unless replace_all is true."},
			"new_string":{"type":"string","description":"Replacement text. Must differ from old_string."},
			"replace_all":{"type":"boolean","description":"Replace every occurrence of old_string. Defaults to false (require a unique match)."}
		}
	}`)
}

type editInput struct {
	FilePath   string `json:"file_path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all"`
	Encoding   string `json:"encoding"`
}

func (t *EditTool) Execute(_ context.Context, input json.RawMessage) (tools.Result, error) {
	var in editInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tools.Result{IsError: true, Content: "edit: decode input: " + err.Error()}, nil
	}
	if in.FilePath == "" {
		return tools.Result{IsError: true, Content: "edit: file_path is required"}, nil
	}

	resolved, err := resolvePath(in.FilePath)
	if err != nil {
		return tools.Result{IsError: true, Content: "error: " + err.Error()}, nil
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("error: file not found: %s", in.FilePath)}, nil
	}
	if info.IsDir() {
		return tools.Result{IsError: true, Content: fmt.Sprintf("error: not a regular file: %s", in.FilePath)}, nil
	}

	if t.tracker != nil && !t.tracker.WasRead(resolved) {
		return tools.Result{
			IsError: true,
			Content: fmt.Sprintf(
				"error: you must use read_file on %s before editing it. "+
					"Read the file first so your old_string matches the current "+
					"content exactly.",
				in.FilePath,
			),
		}, nil
	}

	if in.OldString == in.NewString {
		return tools.Result{
			IsError: true,
			Content: "error: old_string and new_string are identical — no edit to apply.",
		}, nil
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("error: could not read %s: %s", in.FilePath, err)}, nil
	}

	contents := string(data)
	count := strings.Count(contents, in.OldString)
	if count == 0 {
		return tools.Result{
			IsError: true,
			Content: fmt.Sprintf(
				"error: old_string not found in %s. "+
					"The text you provided does not appear in the file. "+
					"Re-read the file and copy the exact text — including "+
					"whitespace — that you want to replace.",
				in.FilePath,
			),
		}, nil
	}
	if count > 1 && !in.ReplaceAll {
		return tools.Result{
			IsError: true,
			Content: fmt.Sprintf(
				"error: old_string matches %d locations in %s. "+
					"Either include more surrounding context in old_string to "+
					"make it unique, or set replace_all=true to replace every "+
					"occurrence.",
				count, in.FilePath,
			),
		}, nil
	}

	var updated string
	if in.ReplaceAll {
		updated = strings.ReplaceAll(contents, in.OldString, in.NewString)
	} else {
		updated = strings.Replace(contents, in.OldString, in.NewString, 1)
	}

	if err := os.WriteFile(resolved, []byte(updated), 0o644); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("error: could not write %s: %s", in.FilePath, err)}, nil
	}

	// Mark as read so subsequent edits don't require another explicit read.
	if t.tracker != nil {
		t.tracker.MarkRead(resolved)
	}

	diff := buildDiff(in.FilePath, in.OldString, in.NewString)
	if in.ReplaceAll && count > 1 {
		diff = fmt.Sprintf("# replaced %d occurrences\n%s", count, diff)
	}
	return tools.Result{Content: diff}, nil
}

// buildDiff returns a minimal unified diff showing old→new replacement.
func buildDiff(path, oldStr, newStr string) string {
	oldLines := splitLines(oldStr)
	newLines := splitLines(newStr)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("--- a/%s\n+++ b/%s\n", path, path))
	b.WriteString(fmt.Sprintf("@@ -1,%d +1,%d @@\n", max(len(oldLines), 1), max(len(newLines), 1)))
	for _, l := range oldLines {
		b.WriteString("-" + l + "\n")
	}
	for _, l := range newLines {
		b.WriteString("+" + l + "\n")
	}
	return b.String()
}

// splitLines splits s into lines, preserving empty trailing lines like
// strings.Split(s, "\n") does, but without the final empty element when
// s ends with \n.
func splitLines(s string) []string {
	if s == "" {
		return []string{""}
	}
	lines := strings.Split(s, "\n")
	if strings.HasSuffix(s, "\n") && len(lines) > 0 {
		lines = lines[:len(lines)-1]
	}
	return lines
}
