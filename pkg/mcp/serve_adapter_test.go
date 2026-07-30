package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/johnny1110/evva/pkg/tools"
)

// fakeTool is a tools.Tool whose Execute is supplied per-test, so one harness
// covers the success, in-band-error and Go-error paths.
type fakeTool struct {
	name   string
	desc   string
	schema string
	exec   func(ctx context.Context, in json.RawMessage) (tools.Result, error)
}

func (f *fakeTool) Name() string        { return f.name }
func (f *fakeTool) Description() string { return f.desc }
func (f *fakeTool) Schema() json.RawMessage {
	if f.schema == "" {
		return nil
	}
	return json.RawMessage(f.schema)
}
func (f *fakeTool) Execute(ctx context.Context, _ *slog.Logger, in json.RawMessage) (tools.Result, error) {
	return f.exec(ctx, in)
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// serveOne stands a wrapped evva tool up behind an in-memory MCP server and
// returns a connected client session — the smallest possible end-to-end proof
// that the adapter speaks real MCP, not just that its Go types line up.
func serveOne(t *testing.T, tool tools.Tool) *mcpsdk.ClientSession {
	t.Helper()
	srv := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "evva", Version: "test"}, nil)
	srv.AddTool(adaptTool(tool, quietLogger()))

	serverT, clientT := mcpsdk.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := srv.Connect(ctx, serverT, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	cli := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "probe", Version: "test"}, nil)
	sess, err := cli.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

func TestAdaptToolRoundTrips(t *testing.T) {
	echo := &fakeTool{
		name:   "repo_map",
		desc:   "Summarises the repository.",
		schema: `{"type":"object","properties":{"depth":{"type":"integer"}},"required":["depth"]}`,
		exec: func(_ context.Context, in json.RawMessage) (tools.Result, error) {
			var args struct {
				Depth int `json:"depth"`
			}
			if err := json.Unmarshal(in, &args); err != nil {
				return tools.Result{}, err
			}
			return tools.Result{Content: "depth=" + strings.Repeat("*", args.Depth)}, nil
		},
	}
	sess := serveOne(t, echo)
	ctx := context.Background()

	// The definition a remote client discovers must carry evva's own
	// hand-written schema verbatim — not something the SDK inferred.
	list, err := sess.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(list.Tools) != 1 {
		t.Fatalf("got %d tools, want 1", len(list.Tools))
	}
	got := list.Tools[0]
	if got.Name != "repo_map" || got.Description != "Summarises the repository." {
		t.Errorf("identity not forwarded: %+v", got)
	}
	raw, _ := json.Marshal(got.InputSchema)
	if !strings.Contains(string(raw), `"required":["depth"]`) {
		t.Errorf("schema was not passed through verbatim: %s", raw)
	}

	res, err := sess.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "repo_map",
		Arguments: map[string]any{"depth": 3},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %+v", res)
	}
	if text := allText(res); text != "depth=***" {
		t.Errorf("content = %q, want %q", text, "depth=***")
	}
}

func TestAdaptToolInBandErrorSetsIsError(t *testing.T) {
	// A tool reporting failure the evva way (Result.IsError) must surface as
	// MCP's IsError, so the calling model can react instead of the call
	// looking successful.
	failing := &fakeTool{
		name: "grep", schema: `{"type":"object"}`,
		exec: func(context.Context, json.RawMessage) (tools.Result, error) {
			return tools.Result{Content: "no such file", IsError: true}, nil
		},
	}
	res, err := serveOne(t, failing).CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "grep"})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if !res.IsError {
		t.Error("Result.IsError did not propagate to CallToolResult.IsError")
	}
	if !strings.Contains(allText(res), "no such file") {
		t.Errorf("error text lost: %q", allText(res))
	}
}

func TestAdaptToolGoErrorBecomesInBandFailure(t *testing.T) {
	// A Go error is an evva-side fault. It must come back as an errored tool
	// result, NOT as a transport/protocol error — a protocol error would look
	// like a broken server to the client rather than a failed call.
	boom := &fakeTool{
		name: "calc", schema: `{"type":"object"}`,
		exec: func(context.Context, json.RawMessage) (tools.Result, error) {
			return tools.Result{}, errors.New("divide by zero")
		},
	}
	res, err := serveOne(t, boom).CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "calc"})
	if err != nil {
		t.Fatalf("Go error escaped as a protocol error: %v", err)
	}
	if !res.IsError {
		t.Error("want IsError for a tool that returned a Go error")
	}
	if text := allText(res); !strings.Contains(text, "divide by zero") || !strings.Contains(text, "calc") {
		t.Errorf("error text should name the tool and the cause, got %q", text)
	}
}

func TestAdaptToolSuppliesObjectSchemaWhenToolHasNone(t *testing.T) {
	// Mirrors newMcpTool's guard in the inbound direction: clients reject a
	// tool definition whose inputSchema is null.
	def, _ := adaptTool(&fakeTool{name: "t", schema: ""}, quietLogger())
	raw, err := json.Marshal(def.InputSchema)
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	if string(raw) != `{"type":"object"}` {
		t.Errorf("schema fallback = %s, want {\"type\":\"object\"}", raw)
	}
}

func TestResultToMCPConvertsImageBlocks(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G'}
	out := resultToMCP(tools.Result{
		Content: "here it is",
		ContentBlocks: []tools.ContentBlock{
			{Type: tools.ContentBlockText, Text: "extra"},
			{Type: tools.ContentBlockImage, Image: &tools.ImageBlock{
				MIMEType:   "image/png",
				Base64Data: base64.StdEncoding.EncodeToString(png),
			}},
		},
	})
	if len(out.Content) != 3 {
		t.Fatalf("got %d content blocks, want 3 (text + text block + image)", len(out.Content))
	}
	img, ok := out.Content[2].(*mcpsdk.ImageContent)
	if !ok {
		t.Fatalf("third block is %T, want *ImageContent", out.Content[2])
	}
	if img.MIMEType != "image/png" || string(img.Data) != string(png) {
		t.Errorf("image not decoded to raw bytes: %+v", img)
	}
}

func TestResultToMCPReportsUndecodableImage(t *testing.T) {
	out := resultToMCP(tools.Result{ContentBlocks: []tools.ContentBlock{
		{Type: tools.ContentBlockImage, Image: &tools.ImageBlock{MIMEType: "image/png", Base64Data: "!!!not base64!!!"}},
	}})
	// Dropping it silently would leave the caller unable to tell an empty
	// result from a broken one.
	if len(out.Content) != 1 || !strings.Contains(allText(out), "image content dropped") {
		t.Errorf("undecodable image should become a visible note, got %+v", out.Content)
	}
}

func TestResultToMCPNeverEmitsNilContent(t *testing.T) {
	// The wire field has no omitempty: a nil slice marshals to null, which
	// strict clients reject.
	out := resultToMCP(tools.Result{})
	if out.Content == nil {
		t.Fatal("Content is nil; must be an empty slice")
	}
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"content":[]`) {
		t.Errorf("empty result marshalled as %s, want an empty content array", raw)
	}
}

func TestResultToMCPDropsMetadata(t *testing.T) {
	// Metadata never reaches evva's own model either; an external caller must
	// not see more than the local contract exposes.
	out := resultToMCP(tools.Result{Content: "ok", Metadata: map[string]string{"secret": "path/to/file"}})
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "secret") {
		t.Errorf("metadata leaked to the wire: %s", raw)
	}
}

// allText concatenates every text block of a result.
func allText(r *mcpsdk.CallToolResult) string {
	var b strings.Builder
	for _, c := range r.Content {
		if tc, ok := c.(*mcpsdk.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}
