package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/johnny1110/evva/pkg/tools"
)

// serve_adapter.go is the outbound mirror of client.go's newMcpTool /
// mcpToolImpl pair (MCP-1). That one wraps a remote MCP tool so evva's agent
// can call it; this one wraps an evva tools.Tool so a remote MCP client can
// call *us*. Same shape, opposite direction — deliberately so, because the
// inbound half has already shipped and survived review.
//
// The pairing to keep in step:
//
//	inbound   sdkTool -> tools.Tool     newMcpTool   + ConvertResult
//	outbound  tools.Tool -> sdkTool     adaptTool    + resultToMCP

// adaptTool wraps an evva tool as an SDK tool definition plus its handler.
//
// It uses the untyped Server.AddTool form rather than the generic
// AddTool[In, Out]: evva tools carry a hand-written JSON Schema
// (tools.Tool.Schema()) that the SDK would otherwise try to *infer* from a Go
// type it has no access to. Passing the schema through verbatim keeps the
// contract the model already sees in evva identical to the one an external
// caller sees over MCP.
func adaptTool(t tools.Tool, logger *slog.Logger) (*mcpsdk.Tool, mcpsdk.ToolHandler) {
	schema := t.Schema()
	if len(schema) == 0 || string(schema) == "null" {
		// Mirrors newMcpTool's guard: a tool with no usable schema still needs
		// a valid object schema or clients reject the tool definition.
		schema = json.RawMessage(`{"type":"object"}`)
	}
	def := &mcpsdk.Tool{
		Name:        t.Name(),
		Description: t.Description(),
		InputSchema: json.RawMessage(schema),
	}

	handler := func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		var args json.RawMessage
		if req != nil && req.Params != nil {
			args = req.Params.Arguments
		}
		res, err := t.Execute(ctx, logger, args)
		if err != nil {
			// A tool returning a Go error is an evva-side failure, not a
			// model-facing one. MCP's convention is to report tool failures
			// in-band (IsError) so the calling model can react, rather than as
			// a protocol error that surfaces as a broken connection.
			return errorResult("evva: " + t.Name() + ": " + err.Error()), nil
		}
		return resultToMCP(res), nil
	}
	return def, handler
}

// resultToMCP converts an evva tool result into an SDK CallToolResult — the
// mirror of ConvertResult (result.go:31).
//
// Content is always a non-nil slice: the wire field has no omitempty, so a nil
// slice marshals to JSON null and strict clients reject it.
func resultToMCP(r tools.Result) *mcpsdk.CallToolResult {
	out := &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{},
		IsError: r.IsError,
	}
	if r.Content != "" {
		out.Content = append(out.Content, &mcpsdk.TextContent{Text: r.Content})
	}
	for _, b := range r.ContentBlocks {
		switch b.Type {
		case tools.ContentBlockText:
			if b.Text != "" {
				out.Content = append(out.Content, &mcpsdk.TextContent{Text: b.Text})
			}
		case tools.ContentBlockImage:
			if b.Image == nil {
				continue
			}
			// tools.ImageBlock carries base64 text; the SDK wants raw bytes and
			// re-encodes on the wire itself. Undecodable data is reported as a
			// text note rather than dropped silently — a caller seeing nothing
			// would have no way to tell an empty result from a broken one.
			raw, err := base64.StdEncoding.DecodeString(b.Image.Base64Data)
			if err != nil {
				out.Content = append(out.Content, &mcpsdk.TextContent{
					Text: "[image content dropped: " + err.Error() + "]",
				})
				continue
			}
			out.Content = append(out.Content, &mcpsdk.ImageContent{
				MIMEType: b.Image.MIMEType,
				Data:     raw,
			})
		}
	}
	// Metadata is deliberately not forwarded: it is a UI-side payload that
	// never reaches evva's own model either (see tools.Result's doc), so
	// shipping it to an external caller would expose more than the local
	// contract does.
	return out
}

// errorResult is the in-band failure shape: one text block, IsError set.
func errorResult(msg string) *mcpsdk.CallToolResult {
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: msg}},
		IsError: true,
	}
}
