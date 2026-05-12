package fs

import (
	"context"
	"encoding/json"
	"os"

	"github.com/johnny1110/evva/internal/tools"
)

// Write is the singleton WriteTool.
var Write tools.Tool = &WriteTool{}

type WriteTool struct{}

func NewWrite() *WriteTool { return &WriteTool{} }

func (t *WriteTool) Name() string { return string(tools.WRITE_FILE) }

func (t *WriteTool) Description() string {
	return "Writes a file to the local filesystem.\n\n" +
		"- Overwrites any existing file at the path.\n" +
		"- If editing an existing file, you MUST Read it first (the tool will fail otherwise).\n" +
		"- Prefer Edit for modifying existing files — only use Write to create new files or for full rewrites.\n" +
		"- Never create .md/README files unless explicitly requested."
}

func (t *WriteTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"required":["file_path","content"],
		"properties":{
			"file_path":{"type":"string","description":"The absolute path to the file to write (must be absolute, not relative)"},
			"content":{"type":"string","description":"The content to write to the file"}
		}
	}`)
}

type writeInput struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
}

func (t *WriteTool) Execute(_ context.Context, input json.RawMessage) (tools.Result, error) {
	var in writeInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tools.Result{IsError: true, Content: err.Error()}, nil
	}
	if err := os.WriteFile(in.FilePath, []byte(in.Content), 0o644); err != nil {
		return tools.Result{IsError: true, Content: err.Error()}, nil
	}
	return tools.Result{Content: "ok"}, nil
}
