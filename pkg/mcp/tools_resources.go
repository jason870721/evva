package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/johnny1110/evva/pkg/tools"
)

// --- list_mcp_resources ----------------------------------------------------

type listResourcesTool struct{ m *Manager }

// NewListResourcesTool builds the deferred list_mcp_resources tool against
// the supplied manager. A nil manager yields a tool that reports "no MCP
// manager configured" — safe for hosts with no MCP servers.
func NewListResourcesTool(m *Manager) tools.Tool { return &listResourcesTool{m: m} }

func (t *listResourcesTool) Name() string { return string(tools.LIST_MCP_RESOURCES) }

func (t *listResourcesTool) Description() string {
	return "List available resources from configured MCP servers. Each resource includes uri/name/mimeType/description plus a `server` field showing which server it came from. Optional `server` arg filters to one."
}

func (t *listResourcesTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"properties":{"server":{"type":"string","description":"Optional server name to filter."}}
	}`)
}

// resourceEntry mirrors ref's ListMcpResourcesTool output shape so the
// model sees consistent fields regardless of which server returned them.
type resourceEntry struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	MIMEType    string `json:"mimeType,omitempty"`
	Description string `json:"description,omitempty"`
	Server      string `json:"server"`
}

func (t *listResourcesTool) Execute(ctx context.Context, lgr *slog.Logger, raw json.RawMessage) (tools.Result, error) {
	if t.m == nil {
		return tools.Result{IsError: true, Content: "list_mcp_resources: no MCP manager configured"}, nil
	}
	var in struct {
		Server string `json:"server"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &in); err != nil {
			return tools.Result{IsError: true, Content: fmt.Sprintf("list_mcp_resources: decode: %v", err)}, nil
		}
	}

	targets := t.m.list()
	if in.Server != "" {
		var matched []*Client
		for _, c := range targets {
			if c.Name == in.Server {
				matched = append(matched, c)
			}
		}
		if len(matched) == 0 {
			available := make([]string, 0, len(targets))
			for _, c := range targets {
				available = append(available, c.Name)
			}
			sort.Strings(available)
			return tools.Result{
				IsError: true,
				Content: fmt.Sprintf("list_mcp_resources: server %q not found; available: %v", in.Server, available),
			}, nil
		}
		targets = matched
	}

	var entries []resourceEntry
	for _, c := range targets {
		c.mu.RLock()
		status := c.status
		session := c.session
		caps := c.caps
		c.mu.RUnlock()
		if status != StatusConnected || session == nil {
			continue
		}
		if caps == nil || caps.Resources == nil {
			// Server doesn't advertise resources — skip silently; most
			// servers expose tools without resources.
			continue
		}
		res, err := session.ListResources(ctx, nil)
		if err != nil {
			lgr.Warn("list_mcp_resources", "server", c.Name, "err", err)
			continue
		}
		for _, r := range res.Resources {
			entries = append(entries, resourceEntry{
				URI:         r.URI,
				Name:        r.Name,
				MIMEType:    r.MIMEType,
				Description: r.Description,
				Server:      c.Name,
			})
		}
	}

	if len(entries) == 0 {
		return tools.Result{Content: "No resources found. MCP servers may still provide tools even if they have no resources."}, nil
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Server != entries[j].Server {
			return entries[i].Server < entries[j].Server
		}
		return entries[i].URI < entries[j].URI
	})
	body, _ := json.MarshalIndent(entries, "", "  ")
	return tools.Result{Content: string(body)}, nil
}

// --- read_mcp_resource -----------------------------------------------------

type readResourceTool struct{ m *Manager }

// NewReadResourceTool builds the deferred read_mcp_resource tool against
// the supplied manager.
func NewReadResourceTool(m *Manager) tools.Tool { return &readResourceTool{m: m} }

func (t *readResourceTool) Name() string { return string(tools.READ_MCP_RESOURCE) }

func (t *readResourceTool) Description() string {
	return "Reads a specific resource from an MCP server. `server` (required): the MCP server name. `uri` (required): the resource URI to read. Text content returns inline; binary blobs are persisted under <APP_HOME>/mcp-blobs/ and the path is returned in place of the bytes."
}

func (t *readResourceTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"required":["server","uri"],
		"properties":{
			"server":{"type":"string","description":"The MCP server name."},
			"uri":{"type":"string","description":"The resource URI to read."}
		}
	}`)
}

func (t *readResourceTool) Execute(ctx context.Context, lgr *slog.Logger, raw json.RawMessage) (tools.Result, error) {
	if t.m == nil {
		return tools.Result{IsError: true, Content: "read_mcp_resource: no MCP manager configured"}, nil
	}
	var in struct {
		Server string `json:"server"`
		URI    string `json:"uri"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("read_mcp_resource: decode: %v", err)}, nil
	}
	if in.Server == "" || in.URI == "" {
		return tools.Result{IsError: true, Content: "read_mcp_resource: server and uri are required"}, nil
	}
	c := t.m.Client(in.Server)
	if c == nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("read_mcp_resource: server %q not found", in.Server)}, nil
	}
	c.mu.RLock()
	status := c.status
	session := c.session
	caps := c.caps
	c.mu.RUnlock()
	if status != StatusConnected || session == nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("read_mcp_resource: server %q is not connected (status=%s)", in.Server, status)}, nil
	}
	if caps == nil || caps.Resources == nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("read_mcp_resource: server %q does not support resources", in.Server)}, nil
	}

	res, err := session.ReadResource(ctx, &mcpsdk.ReadResourceParams{URI: in.URI})
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("read_mcp_resource: %v", err)}, nil
	}

	type contentEntry struct {
		URI         string `json:"uri"`
		MIMEType    string `json:"mimeType,omitempty"`
		Text        string `json:"text,omitempty"`
		BlobSavedTo string `json:"blobSavedTo,omitempty"`
	}
	var out []contentEntry
	for _, item := range res.Contents {
		entry := contentEntry{URI: item.URI, MIMEType: item.MIMEType}
		switch {
		case item.Text != "":
			entry.Text = item.Text
		case len(item.Blob) > 0:
			if c.evvaHome == "" {
				entry.Text = "[binary content received but no AppHome configured]"
				break
			}
			if path, size, perr := persistResourceBlob(item, c.evvaHome); perr == nil {
				entry.BlobSavedTo = path
				entry.Text = fmt.Sprintf("[binary content saved at %s, %d bytes]", path, size)
			} else {
				entry.Text = fmt.Sprintf("[binary content not saved: %v]", perr)
			}
		}
		out = append(out, entry)
	}
	if len(out) == 0 {
		return tools.Result{IsError: true, Content: "read_mcp_resource: resource returned no contents"}, nil
	}
	body, _ := json.MarshalIndent(struct {
		Contents []contentEntry `json:"contents"`
	}{Contents: out}, "", "  ")
	return tools.Result{Content: string(body)}, nil
}
