package mcp

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/johnny1110/evva/pkg/tools"
)

const maxResultChars = 100_000 // mirrors ref MCPTool.maxResultSizeChars

// ConvertResult turns an SDK CallToolResult into the agent's tools.Result.
// Text blocks concatenate into Content; image blocks become ContentBlocks;
// embedded binary resource blobs are persisted to disk under
// <evvaHome>/mcp-blobs/<random> and replaced with a "[binary saved at
// <path>]" line.
//
// evvaHome is the resolved cfg.AppHome — passed in so this stays a pure
// conversion helper (no env reads, no global state). When evvaHome is
// empty, blob persistence is disabled and the conversion emits a
// "[binary received but no AppHome configured]" note instead. That is the
// only path that touches the filesystem.
func ConvertResult(r *mcpsdk.CallToolResult, server, tool, evvaHome string) (tools.Result, error) {
	if r == nil {
		return tools.Result{}, errors.New("mcp: nil result")
	}

	var (
		textBuf strings.Builder
		blocks  []tools.ContentBlock
	)
	for _, item := range r.Content {
		switch c := item.(type) {
		case *mcpsdk.TextContent:
			textBuf.WriteString(c.Text)
			textBuf.WriteString("\n")
		case *mcpsdk.ImageContent:
			// SDK delivers Data as raw decoded bytes; tools.ImageBlock wants
			// a base64 string.
			blocks = append(blocks, tools.ContentBlock{
				Type: tools.ContentBlockImage,
				Image: &tools.ImageBlock{
					MIMEType:     c.MIMEType,
					Base64Data:   base64.StdEncoding.EncodeToString(c.Data),
					OriginalSize: int64(len(c.Data)),
				},
			})
		case *mcpsdk.AudioContent:
			fmt.Fprintf(&textBuf, "[audio content (%s, %d bytes) from %s/%s — not rendered inline]\n", c.MIMEType, len(c.Data), server, tool)
		case *mcpsdk.EmbeddedResource:
			if evvaHome == "" {
				fmt.Fprintf(&textBuf, "[binary content from %s/%s received but blob persistence disabled (no AppHome configured)]\n", server, tool)
				continue
			}
			if path, size, err := persistResourceBlob(c.Resource, evvaHome); err == nil {
				fmt.Fprintf(&textBuf, "[binary content saved at %s, %d bytes]\n", path, size)
			} else {
				fmt.Fprintf(&textBuf, "[binary content not saved: %v]\n", err)
			}
		}
	}

	content := textBuf.String()
	if n := len(content); n > maxResultChars {
		content = content[:maxResultChars] + fmt.Sprintf("\n\n[truncated %d chars]", n-maxResultChars)
	}
	return tools.Result{
		Content:       content,
		ContentBlocks: blocks,
		IsError:       r.IsError,
	}, nil
}

// persistResourceBlob writes a binary blob to <evvaHome>/mcp-blobs/<random>
// and returns the path + byte count. evvaHome MUST be non-empty (caller
// gates on that). The blob dir is created on first use with mode 0700.
//
// The SDK delivers ResourceContents.Blob as already-decoded bytes, so we
// write them straight through — no base64 decode.
//
// Filename is a 16-byte crypto-random hex string — sufficient uniqueness
// for the lifetime of an evva session without pulling in a UUID library.
func persistResourceBlob(rc *mcpsdk.ResourceContents, evvaHome string) (string, int, error) {
	if rc == nil || len(rc.Blob) == 0 {
		return "", 0, errors.New("no blob content")
	}
	blobDir := filepath.Join(evvaHome, "mcp-blobs")
	if err := os.MkdirAll(blobDir, 0o700); err != nil {
		return "", 0, err
	}
	name, err := randomBlobName()
	if err != nil {
		return "", 0, err
	}
	path := filepath.Join(blobDir, name)
	if err := os.WriteFile(path, rc.Blob, 0o600); err != nil {
		return "", 0, err
	}
	return path, len(rc.Blob), nil
}

// randomBlobName returns a 32-char lowercase-hex filename backed by 16
// bytes of crypto/rand. Used in place of a UUID library — collision odds
// at evva session scale are vanishingly small.
func randomBlobName() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
