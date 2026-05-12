package fs

import (
	"context"
	"encoding/json"
	"os"

	"github.com/johnny1110/evva/internal/tools"
)

// Read is the singleton ReadTool. Stateless — every call is a pure function
// of its input — so one instance suffices across all agents.
var Read tools.Tool = &ReadTool{}

type ReadTool struct{}

func NewRead() *ReadTool { return &ReadTool{} }

func (t *ReadTool) Name() string { return string(tools.READ_FILE) }

func (t *ReadTool) Description() string {
	return "Reads a file from the local filesystem by absolute path.\n\n" +
		"- Default reads up to 2000 lines from the start; use offset/limit for partial reads of large files.\n" +
		"- Returns content in cat -n format (line numbers starting at 1).\n" +
		"- Supports images (PNG, JPG, etc.) — displayed visually.\n" +
		"- Supports PDF (.pdf) — large PDFs (>10 pages) require the pages parameter (max 20 pages per request).\n" +
		"- Supports Jupyter notebooks (.ipynb) — returns cells with outputs.\n" +
		"- Reads only files, not directories."
}

func (t *ReadTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"required":["file_path"],
		"properties":{
			"file_path":{"type":"string","description":"The absolute path to the file to read"},
			"offset":{"type":"integer","minimum":0,"description":"The line number to start reading from. Only provide if the file is too large to read at once."},
			"limit":{"type":"integer","exclusiveMinimum":0,"description":"The number of lines to read. Only provide if the file is too large to read at once."},
			"pages":{"type":"string","description":"Page range for PDF files (e.g., \"1-5\", \"3\", \"10-20\"). Max 20 pages per request."}
		}
	}`)
}

type readInput struct {
	FilePath string `json:"file_path"`
}

func (t *ReadTool) Execute(_ context.Context, input json.RawMessage) (tools.Result, error) {
	var in readInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tools.Result{IsError: true, Content: err.Error()}, nil
	}
	data, err := os.ReadFile(in.FilePath)
	if err != nil {
		return tools.Result{IsError: true, Content: err.Error()}, nil
	}
	return tools.Result{Content: string(data)}, nil
}
