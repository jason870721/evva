// Command stdio-echo-server is a minimal MCP server used by pkg/mcp's
// integration tests. It speaks the MCP stdio transport and exposes one
// tool (echo) and one text resource (echo://greeting). The pkg/mcp tests
// build this binary and spawn it via the CommandTransport, exercising the
// full connect → tools/list → tools/call → resources path against a real
// subprocess.
//
// It lives under testdata/ so the normal build ignores it; the tests
// compile it explicitly with `go build`.
package main

import (
	"context"
	"log"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type echoArgs struct {
	Text string `json:"text" jsonschema:"the text to echo back"`
}

func main() {
	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "echo", Version: "0.0.1"}, nil)

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "echo",
		Description: "Echoes back the supplied text.",
	}, func(_ context.Context, _ *mcpsdk.CallToolRequest, args echoArgs) (*mcpsdk.CallToolResult, any, error) {
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "echo: " + args.Text}},
		}, nil, nil
	})

	server.AddResource(
		&mcpsdk.Resource{
			URI:      "echo://greeting",
			Name:     "greeting",
			MIMEType: "text/plain",
		},
		func(_ context.Context, _ *mcpsdk.ReadResourceRequest) (*mcpsdk.ReadResourceResult, error) {
			return &mcpsdk.ReadResourceResult{
				Contents: []*mcpsdk.ResourceContents{{
					URI:      "echo://greeting",
					MIMEType: "text/plain",
					Text:     "hello from the echo server",
				}},
			}, nil
		},
	)

	if err := server.Run(context.Background(), &mcpsdk.StdioTransport{}); err != nil {
		log.Fatalf("echo server: %v", err)
	}
}
