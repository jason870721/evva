package mcp

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/johnny1110/evva/pkg/tools"
)

func TestConvertResult_Text(t *testing.T) {
	r := &mcpsdk.CallToolResult{Content: []mcpsdk.Content{
		&mcpsdk.TextContent{Text: "line one"},
		&mcpsdk.TextContent{Text: "line two"},
	}}
	res, err := ConvertResult(r, "srv", "tool", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, "line one") || !strings.Contains(res.Content, "line two") {
		t.Fatalf("text not concatenated: %q", res.Content)
	}
	if len(res.ContentBlocks) != 0 {
		t.Fatalf("text-only result should have no content blocks")
	}
}

func TestConvertResult_Image(t *testing.T) {
	raw := []byte{0x89, 0x50, 0x4e, 0x47} // arbitrary bytes
	r := &mcpsdk.CallToolResult{Content: []mcpsdk.Content{
		&mcpsdk.ImageContent{MIMEType: "image/png", Data: raw},
	}}
	res, err := ConvertResult(r, "srv", "tool", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.ContentBlocks) != 1 {
		t.Fatalf("want 1 image block, got %d", len(res.ContentBlocks))
	}
	b := res.ContentBlocks[0]
	if b.Type != tools.ContentBlockImage || b.Image == nil {
		t.Fatalf("block not an image: %+v", b)
	}
	if b.Image.MIMEType != "image/png" {
		t.Fatalf("mime = %q", b.Image.MIMEType)
	}
	// SDK delivers raw bytes; ConvertResult must base64-encode for ImageBlock.
	if b.Image.Base64Data != base64.StdEncoding.EncodeToString(raw) {
		t.Fatalf("image data not base64-encoded: %q", b.Image.Base64Data)
	}
}

func TestConvertResult_BlobPersisted(t *testing.T) {
	home := t.TempDir()
	blob := []byte("binary-bytes-here")
	r := &mcpsdk.CallToolResult{Content: []mcpsdk.Content{
		&mcpsdk.EmbeddedResource{Resource: &mcpsdk.ResourceContents{URI: "x://b", Blob: blob}},
	}}
	res, err := ConvertResult(r, "srv", "tool", home)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, "binary content saved at") {
		t.Fatalf("expected a saved-blob note, got %q", res.Content)
	}
	// A file must exist under <home>/mcp-blobs with the blob's bytes.
	dir := filepath.Join(home, "mcp-blobs")
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("want 1 blob file, got %d", len(entries))
	}
	got, _ := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if string(got) != string(blob) {
		t.Fatalf("blob bytes mismatch: %q", got)
	}
}

func TestConvertResult_BlobNoAppHome(t *testing.T) {
	r := &mcpsdk.CallToolResult{Content: []mcpsdk.Content{
		&mcpsdk.EmbeddedResource{Resource: &mcpsdk.ResourceContents{URI: "x://b", Blob: []byte("data")}},
	}}
	res, err := ConvertResult(r, "srv", "tool", "") // empty evvaHome
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, "blob persistence disabled") {
		t.Fatalf("expected the no-AppHome note, got %q", res.Content)
	}
}

func TestConvertResult_Truncation(t *testing.T) {
	big := strings.Repeat("a", maxResultChars+500)
	r := &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: big}}}
	res, err := ConvertResult(r, "srv", "tool", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, "[truncated") {
		t.Fatalf("oversize result should carry a truncation marker")
	}
}

func TestConvertResult_IsErrorPropagates(t *testing.T) {
	r := &mcpsdk.CallToolResult{IsError: true, Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "boom"}}}
	res, err := ConvertResult(r, "srv", "tool", "")
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("IsError should propagate from the SDK result")
	}
}

func TestConvertResult_Nil(t *testing.T) {
	if _, err := ConvertResult(nil, "s", "t", ""); err == nil {
		t.Fatalf("nil result should error")
	}
}
