package fs

import (
	"context"
	"encoding/json"
	"os"
	"strings"

	"github.com/johnny1110/evva/internal/tools"
)

// Edit is the singleton EditTool.
var Edit tools.Tool = &EditTool{}

type EditTool struct{}

func NewEdit() *EditTool { return &EditTool{} }

func (t *EditTool) Name() string { return string(tools.EDIT_FILE) }

func (t *EditTool) Description() string {
	return "Performs exact string replacements in files.\n\n" +
		"Usage:\n" +
		"- You must use Read at least once in the conversation before editing.\n" +
		"- Preserve exact indentation as it appears AFTER the line-number prefix in Read output. " +
		"Never include any part of the line-number prefix in old_string or new_string.\n" +
		"- Prefer editing existing files. Never create new files unless explicitly required.\n" +
		"- The edit will FAIL if old_string is not unique — provide more surrounding context or use replace_all."
}

func (t *EditTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"required":["file_path","old_string","new_string"],
		"properties":{
			"file_path":{"type":"string","description":"The absolute path to the file to modify"},
			"old_string":{"type":"string","description":"The text to replace"},
			"new_string":{"type":"string","description":"The text to replace it with (must be different from old_string)"},
			"replace_all":{"type":"boolean","default":false,"description":"Replace all occurrences of old_string (default false)"}
		}
	}`)
}

type editInput struct {
	FilePath   string `json:"file_path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all"`
}

func (t *EditTool) Execute(_ context.Context, input json.RawMessage) (tools.Result, error) {
	var in editInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tools.Result{IsError: true, Content: err.Error()}, nil
	}
	data, err := os.ReadFile(in.FilePath)
	if err != nil {
		return tools.Result{IsError: true, Content: err.Error()}, nil
	}

	n := 1
	if in.ReplaceAll {
		n = -1
	}
	updated := strings.Replace(string(data), in.OldString, in.NewString, n)
	if err := os.WriteFile(in.FilePath, []byte(updated), 0o644); err != nil {
		return tools.Result{IsError: true, Content: err.Error()}, nil
	}
	return tools.Result{Content: "ok"}, nil
}
