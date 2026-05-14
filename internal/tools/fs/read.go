package fs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/johnny1110/evva/internal/tools"
)

// ReadTool reads a file and returns it in cat -n format.
type ReadTool struct {
	tracker *ReadTracker
}

// NewRead creates a ReadTool that records reads in the given tracker.
func NewRead(tracker *ReadTracker) *ReadTool {
	return &ReadTool{tracker: tracker}
}

func (t *ReadTool) Name() string { return string(tools.READ_FILE) }

func (t *ReadTool) Description() string {
	return "Reads a file from the local filesystem. Output is cat -n format: " +
		"each line is prefixed with its 1-based line number and a tab " +
		"(e.g. `   42\\thello`). A header `[File: <path> (N lines)]` " +
		"precedes the body and notes the slice when offset/limit are used.\n\n" +
		"Use `offset` (1-based) and `limit` to read a slice of a large " +
		"file without spending tokens on the rest. Reading marks the file " +
		"as loaded into the session — edit_file and write_file (overwrite) " +
		"refuse to touch a file you haven't read first.\n\n" +
		"When you later call edit_file, DO NOT include the `<line>\\t` " +
		"prefix in old_string — strip it. Only the raw line content is " +
		"what's actually in the file."
}

func (t *ReadTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"required":["file_path"],
		"properties":{
			"file_path":{"type":"string","description":"Absolute or relative path to the file to read."},
			"offset":{"type":"integer","description":"1-based line number to start reading from. Defaults to 1."},
			"limit":{"type":"integer","exclusiveMinimum":0,"description":"Maximum number of lines to return. Defaults to all lines."}
		}
	}`)
}

type readInput struct {
	FilePath string `json:"file_path"`
	Encoding string `json:"encoding"`
	Offset   int    `json:"offset"`
	Limit    int    `json:"limit"`
}

func (t *ReadTool) Execute(_ context.Context, input json.RawMessage) (tools.Result, error) {
	var in readInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tools.Result{IsError: true, Content: "read: decode input: " + err.Error()}, nil
	}
	if in.FilePath == "" {
		return tools.Result{IsError: true, Content: "read: file_path is required"}, nil
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

	encoding := in.Encoding
	if encoding == "" {
		encoding = "utf-8"
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("error: could not read %s: %s", in.FilePath, err)}, nil
	}

	// Mark as read so edit/write guards pass.
	if t.tracker != nil {
		t.tracker.MarkRead(resolved)
	}

	content := string(data)
	allLines := strings.Split(content, "\n")
	// Strip trailing empty line from split if content ends with \n
	if len(allLines) > 0 && allLines[len(allLines)-1] == "" && strings.HasSuffix(content, "\n") {
		allLines = allLines[:len(allLines)-1]
	}
	totalLines := len(allLines)

	if totalLines == 0 {
		return tools.Result{Content: fmt.Sprintf("[File: %s, 0 lines]", resolved)}, nil
	}

	// offset is 1-based, clamp to valid range
	start := in.Offset
	if start < 1 {
		start = 1
	}
	startIdx := start - 1
	if startIdx >= totalLines {
		return tools.Result{Content: fmt.Sprintf(
			"[File: %s (%d lines), showing lines %d-%d (offset past end)]",
			resolved, totalLines, start, totalLines,
		)}, nil
	}

	endIdx := totalLines
	if in.Limit > 0 {
		if end := startIdx + in.Limit; end < endIdx {
			endIdx = end
		}
	}

	selected := allLines[startIdx:endIdx]

	var header string
	if startIdx == 0 && endIdx == totalLines {
		header = fmt.Sprintf("[File: %s (%d lines)]", resolved, totalLines)
	} else {
		header = fmt.Sprintf("[File: %s (%d lines), showing lines %d-%d]",
			resolved, totalLines, startIdx+1, endIdx)
	}

	body := formatLines(selected, startIdx+1)
	return tools.Result{Content: header + "\n" + body}, nil
}

// formatLines renders lines with cat -n style prefix: 6-char right-aligned
// line number + tab + content.
func formatLines(lines []string, startLine int) string {
	var b strings.Builder
	for i, line := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%6d\t%s", startLine+i, line)
	}
	return b.String()
}
