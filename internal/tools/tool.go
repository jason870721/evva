package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
)

// Tool is the contract every tool must satisfy.
// Stateless tools are typically package-level singletons (shell.Bash).
// Stateful tools receive backing state via constructor (fs.NewRead, task.NewCreate).
type Tool interface {
	Name() string
	Description() string
	Schema() json.RawMessage
	Execute(ctx context.Context, input json.RawMessage) (Result, error)
}

// ContentBlockType discriminates the kind of content in a block.
type ContentBlockType string

const (
	ContentBlockText  ContentBlockType = "text"
	ContentBlockImage ContentBlockType = "image"
)

// ImageBlock holds base64-encoded image data for multimodal tool results.
type ImageBlock struct {
	MIMEType     string // "image/png", "image/jpeg", "image/gif", "image/webp"
	Base64Data   string // base64-encoded image bytes
	OriginalSize int64  // original file size in bytes
}

// ContentBlock is one element in a tool result's content list.
type ContentBlock struct {
	Type  ContentBlockType
	Text  string      // populated when Type == ContentBlockText
	Image *ImageBlock // populated when Type == ContentBlockImage
}

// Result is what every tool returns to the agent.
//
// Metadata is an optional, tool-specific structured payload that flows
// through to the event sink (carried on ToolUseResultPayload.Metadata) so
// UIs can render richer detail than the human-readable Content string
// allows. Stays opaque to the agent layer — the UI type-asserts on it.
// Common payloads today:
//   - *fs.FileDiff for write_file / edit_file mutations
//
// LLM-facing tool results carry only Content + IsError; Metadata never
// goes to the model.
type Result struct {
	Content       string
	IsError       bool
	Metadata      any
	ContentBlocks []ContentBlock
}

// NewImageResult returns a Result with a single image content block.
func NewImageResult(data []byte, mimeType string, originalSize int64) Result {
	b64ImgStrData := base64.StdEncoding.EncodeToString(data)
	return Result{
		ContentBlocks: []ContentBlock{{
			Type: ContentBlockImage,
			Image: &ImageBlock{
				MIMEType:     mimeType,
				Base64Data:   b64ImgStrData,
				OriginalSize: originalSize,
			},
		}},
	}
}

// Call is what the LLM emits when it wants to invoke a tool.
type Call struct {
	ID    string
	Name  string
	Input json.RawMessage
}
