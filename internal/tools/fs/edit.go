package fs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
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
	return "Performs exact string replacements in a file. file_path must be absolute.\n\n" +
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
		"fully overwrite one, use write_file instead.\n\n" +
		"Only use emojis if the user explicitly requested them."
}

func (t *EditTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"required":["file_path","old_string","new_string"],
		"properties":{
			"file_path":{"type":"string","description":"Absolute path to the file to edit (must be absolute, not relative)."},
			"old_string":{"type":"string","description":"Exact text to find. Must match byte-for-byte including whitespace and newlines. Include enough surrounding context to make it unique unless replace_all is true."},
			"new_string":{"type":"string","description":"Replacement text. Must differ from old_string."},
			"replace_all":{"type":"boolean","default":false,"description":"Replace every occurrence of old_string. Defaults to false (require a unique match)."}
		}
	}`)
}

type editInput struct {
	FilePath   string `json:"file_path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all"`
}

func (t *EditTool) Execute(ctx context.Context, input json.RawMessage) (tools.Result, error) {
	var in editInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tools.Result{IsError: true, Content: "edit: decode input: " + err.Error()}, nil
	}

	resolved, err := resolvePath(in.FilePath)
	if err != nil {
		return tools.Result{IsError: true, Content: "edit: " + err.Error()}, nil
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("edit: file not found: %s", in.FilePath)}, nil
	}
	if info.IsDir() {
		return tools.Result{IsError: true, Content: fmt.Sprintf("edit: not a regular file: %s", in.FilePath)}, nil
	}

	if t.tracker != nil && !t.tracker.WasRead(resolved) {
		return tools.Result{
			IsError: true,
			Content: fmt.Sprintf(
				"edit: you must use read_file on %s before editing it. "+
					"Read the file first so your old_string matches the current "+
					"content exactly.",
				in.FilePath,
			),
		}, nil
	}

	if in.OldString == in.NewString {
		return tools.Result{
			IsError: true,
			Content: "edit: old_string and new_string are identical — no edit to apply.",
		}, nil
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("edit: could not read %s: %s", in.FilePath, err)}, nil
	}

	before := string(data)
	count := strings.Count(before, in.OldString)
	if count == 0 {
		return tools.Result{
			IsError: true,
			Content: fmt.Sprintf(
				"edit: old_string not found in %s. "+
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
				"edit: old_string matches %d locations in %s. "+
					"Either include more surrounding context in old_string to "+
					"make it unique, or set replace_all=true to replace every "+
					"occurrence.",
				count, in.FilePath,
			),
		}, nil
	}

	// Collect each replacement's starting byte offset in the original
	// file so we can compute its 1-based line number for the hunk header
	// and the model-facing summary. Order matters — earlier hunks shift
	// later ones on the new side, but the byte offsets on the OLD side
	// remain stable since we scan against `before`.
	offsets := findAllOffsets(before, in.OldString)
	if !in.ReplaceAll {
		offsets = offsets[:1]
	}

	var after string
	if in.ReplaceAll {
		after = strings.ReplaceAll(before, in.OldString, in.NewString)
	} else {
		after = strings.Replace(before, in.OldString, in.NewString, 1)
	}

	// Resolve byte offsets to 1-based line numbers once — both the diff
	// builder and the model-facing summary want them.
	oldLineNums := make([]int, len(offsets))
	for i, off := range offsets {
		oldLineNums[i] = lineNumberOf(before, off)
	}

	diff := buildEditDiff(resolved, before, after, in.OldString, in.NewString, oldLineNums)

	if err := os.WriteFile(resolved, []byte(after), 0o644); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("edit: could not write %s: %s", in.FilePath, err)}, nil
	}

	if t.tracker != nil {
		t.tracker.MarkRead(resolved)
	}

	return tools.Result{
		Content:  editSummary(resolved, oldLineNums),
		Metadata: diff,
	}, nil
}

// findAllOffsets returns every starting byte index of needle in haystack
// (non-overlapping, left-to-right).
func findAllOffsets(haystack, needle string) []int {
	out := make([]int, 0, 1)
	start := 0
	for {
		i := strings.Index(haystack[start:], needle)
		if i < 0 {
			return out
		}
		out = append(out, start+i)
		start += i + len(needle)
	}
}

// editSummary formats the model-facing one-liner: how many replacements
// landed and at which 1-based old-side line numbers. The diff itself
// stays in Metadata for the UI.
func editSummary(path string, lineNums []int) string {
	if len(lineNums) == 0 {
		return fmt.Sprintf("edited %s", path)
	}
	parts := make([]string, len(lineNums))
	for i, n := range lineNums {
		parts[i] = strconv.Itoa(n)
	}
	return fmt.Sprintf("edited %s (%d replacement(s) at line(s) %s)",
		path, len(lineNums), strings.Join(parts, ", "))
}

// buildEditDiff assembles a FileDiff for one or more replacements. Each
// occurrence becomes one DiffHunk with up to ContextLines context above
// and below. Line numbers in hunks refer to the original (old) file on
// the remove side and the post-edit file on the add side.
func buildEditDiff(path, before, after, oldStr, newStr string, oldLineNums []int) *FileDiff {
	oldLines := splitLinesPreservingEnd(before)
	newLines := splitLinesPreservingEnd(after)

	changedOld := lineSpan(oldStr)
	changedNew := lineSpan(newStr)

	hunks := make([]DiffHunk, 0, len(oldLineNums))
	delta := 0
	for _, oldLineNum := range oldLineNums {
		newLineNum := oldLineNum + delta
		hunks = append(hunks, buildEditHunk(oldLines, newLines, oldLineNum, newLineNum, changedOld, changedNew))
		delta += changedNew - changedOld
	}

	return &FileDiff{Path: path, Op: OpEdit, Hunks: hunks}
}

// lineSpan returns the number of lines a substring occupies — i.e. how
// many lines old_string or new_string covers when substituted in place.
// Counts the lines as splitLinesPreservingEnd would yield, with a minimum
// of 1 so that "foo" (no newline) registers as 1 line, not 0.
func lineSpan(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	if n < 1 {
		return 1
	}
	return n
}
