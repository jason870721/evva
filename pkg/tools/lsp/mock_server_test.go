package lsp

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"sync"
	"testing"

	"github.com/johnny1110/evva/pkg/tools/lsp/protocol"
)

// TestMockLSPServer is the entry point for the mock LSP server subprocess.
func TestMockLSPServer(t *testing.T) {
	if os.Getenv("MOCK_LSP") != "1" {
		t.Skip("mock LSP server helper")
	}
	runMockLSP()
}

// runMockLSP implements a minimal LSP server over stdio framing.
func runMockLSP() {
	reader := bufio.NewReader(os.Stdin)
	writer := os.Stdout

	for {
		body, err := readMessage(reader)
		if err != nil {
			if err != io.EOF {
				// ignore
			}
			return
		}
		if body == nil || len(body) == 0 {
			continue
		}

		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			continue
		}

		switch req.Method {
		case protocol.MethodInitialize:
			resp := protocol.Response{JSONRPC: "2.0", ID: req.ID, Result: mustMarshal(mockCapabilities())}
			writeMessage(writer, mustMarshal(resp))
		case protocol.MethodShutdown:
			resp := protocol.Response{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage("null")}
			writeMessage(writer, mustMarshal(resp))
		case protocol.MethodExit:
			return
		case protocol.MethodDefinition:
			writeMessage(writer, mustMarshal(mockDefinitionResponse(req.ID)))
		case protocol.MethodReferences:
			writeMessage(writer, mustMarshal(mockReferencesResponse(req.ID)))
		case protocol.MethodHover:
			writeMessage(writer, mustMarshal(mockHoverResponse(req.ID)))
		case protocol.MethodDocumentSymbol:
			writeMessage(writer, mustMarshal(mockDocumentSymbolsResponse(req.ID)))
		case protocol.MethodImplementation:
			writeMessage(writer, mustMarshal(mockImplementationResponse(req.ID)))
		case protocol.MethodPrepareCallHierarchy:
			writeMessage(writer, mustMarshal(mockCallHierarchyResponse(req.ID)))
		case protocol.MethodIncomingCalls:
			writeMessage(writer, mustMarshal(mockIncomingCallsResponse(req.ID)))
		case protocol.MethodOutgoingCalls:
			writeMessage(writer, mustMarshal(mockOutgoingCallsResponse(req.ID)))
		case protocol.MethodWorkspaceSymbol:
			writeMessage(writer, mustMarshal(mockWorkspaceSymbolResponse(req.ID)))
		default:
			resp := protocol.Response{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage("null")}
			writeMessage(writer, mustMarshal(resp))
		}
	}
}

func mockCapabilities() protocol.InitializeResult {
	return protocol.InitializeResult{
		Capabilities: protocol.ServerCapabilities{
			TextDocumentSync:       &protocol.TextDocumentSyncOptions{OpenClose: true, Change: 1},
			DefinitionProvider:     true,
			ReferencesProvider:     true,
			HoverProvider:          true,
			DocumentSymbolProvider: true,
		},
	}
}

func mockDefinitionResponse(id json.RawMessage) protocol.Response {
	return protocol.Response{JSONRPC: "2.0", ID: id, Result: mustMarshal(protocol.Location{
		URI: "file:///project/other.go",
		Range: protocol.Range{
			Start: protocol.Position{Line: 9, Character: 5},
			End:   protocol.Position{Line: 9, Character: 22},
		},
	})}
}

func mockReferencesResponse(id json.RawMessage) protocol.Response {
	return protocol.Response{JSONRPC: "2.0", ID: id, Result: mustMarshal([]protocol.Location{
		{URI: "file:///project/a.go", Range: protocol.Range{
			Start: protocol.Position{Line: 3, Character: 1}, End: protocol.Position{Line: 3, Character: 9}}},
		{URI: "file:///project/b.go", Range: protocol.Range{
			Start: protocol.Position{Line: 11, Character: 4}, End: protocol.Position{Line: 11, Character: 12}}},
	})}
}

func mockHoverResponse(id json.RawMessage) protocol.Response {
	return protocol.Response{JSONRPC: "2.0", ID: id, Result: mustMarshal(protocol.Hover{
		Contents: protocol.MarkupContent{Kind: "markdown", Value: "```go\nfunc hello() string\n```\n\nReturns a greeting."},
	})}
}

func mockDocumentSymbolsResponse(id json.RawMessage) protocol.Response {
	return protocol.Response{JSONRPC: "2.0", ID: id, Result: mustMarshal([]protocol.DocumentSymbol{
		{Name: "main", Kind: protocol.SKFunction,
			Range:          protocol.Range{Start: protocol.Position{Line: 0, Character: 0}, End: protocol.Position{Line: 10, Character: 1}},
			SelectionRange: protocol.Range{Start: protocol.Position{Line: 0, Character: 0}, End: protocol.Position{Line: 0, Character: 4}},
			Children: []*protocol.DocumentSymbol{
				{Name: "helper", Kind: protocol.SKFunction,
					Range:          protocol.Range{Start: protocol.Position{Line: 5, Character: 2}, End: protocol.Position{Line: 7, Character: 3}},
					SelectionRange: protocol.Range{Start: protocol.Position{Line: 5, Character: 2}, End: protocol.Position{Line: 5, Character: 8}},
				},
			},
		},
	})}
}

func mockImplementationResponse(id json.RawMessage) protocol.Response {
	return protocol.Response{JSONRPC: "2.0", ID: id, Result: mustMarshal([]protocol.Location{
		{URI: "file:///project/impl_a.go", Range: protocol.Range{
			Start: protocol.Position{Line: 20, Character: 1}, End: protocol.Position{Line: 20, Character: 10}}},
		{URI: "file:///project/impl_b.go", Range: protocol.Range{
			Start: protocol.Position{Line: 30, Character: 5}, End: protocol.Position{Line: 30, Character: 15}}},
	})}
}

func mockCallHierarchyResponse(id json.RawMessage) protocol.Response {
	return protocol.Response{JSONRPC: "2.0", ID: id, Result: mustMarshal([]protocol.CallHierarchyItem{
		{Name: "main", Kind: protocol.SKFunction, URI: "file:///project/main.go",
			Range:          protocol.Range{Start: protocol.Position{Line: 0, Character: 0}, End: protocol.Position{Line: 10, Character: 1}},
			SelectionRange: protocol.Range{Start: protocol.Position{Line: 0, Character: 0}, End: protocol.Position{Line: 0, Character: 4}},
		},
		{Name: "init", Kind: protocol.SKFunction, URI: "file:///project/main.go",
			Range:          protocol.Range{Start: protocol.Position{Line: 12, Character: 0}, End: protocol.Position{Line: 15, Character: 1}},
			SelectionRange: protocol.Range{Start: protocol.Position{Line: 12, Character: 5}, End: protocol.Position{Line: 12, Character: 9}},
		},
	})}
}

func mockIncomingCallsResponse(id json.RawMessage) protocol.Response {
	from := protocol.CallHierarchyItem{Name: "caller", Kind: protocol.SKFunction, URI: "file:///project/caller.go",
		Range:          protocol.Range{Start: protocol.Position{Line: 5, Character: 0}, End: protocol.Position{Line: 8, Character: 1}},
		SelectionRange: protocol.Range{Start: protocol.Position{Line: 5, Character: 0}, End: protocol.Position{Line: 5, Character: 6}},
	}
	return protocol.Response{JSONRPC: "2.0", ID: id, Result: mustMarshal([]protocol.CallHierarchyIncomingCall{
		{From: from, FromRanges: []protocol.Range{
			{Start: protocol.Position{Line: 7, Character: 2}, End: protocol.Position{Line: 7, Character: 10}},
		}},
		{From: from, FromRanges: []protocol.Range{
			{Start: protocol.Position{Line: 10, Character: 5}, End: protocol.Position{Line: 10, Character: 12}},
		}},
	})}
}

func mockOutgoingCallsResponse(id json.RawMessage) protocol.Response {
	to := protocol.CallHierarchyItem{Name: "callee", Kind: protocol.SKFunction, URI: "file:///project/callee.go",
		Range:          protocol.Range{Start: protocol.Position{Line: 3, Character: 0}, End: protocol.Position{Line: 6, Character: 1}},
		SelectionRange: protocol.Range{Start: protocol.Position{Line: 3, Character: 0}, End: protocol.Position{Line: 3, Character: 6}},
	}
	return protocol.Response{JSONRPC: "2.0", ID: id, Result: mustMarshal([]protocol.CallHierarchyOutgoingCall{
		{To: to, FromRanges: []protocol.Range{
			{Start: protocol.Position{Line: 1, Character: 2}, End: protocol.Position{Line: 1, Character: 8}},
		}},
	})}
}

func mockWorkspaceSymbolResponse(id json.RawMessage) protocol.Response {
	return protocol.Response{JSONRPC: "2.0", ID: id, Result: mustMarshal([]protocol.SymbolInformation{
		{Name: "ServeHTTP", Kind: protocol.SKFunction,
			Location: protocol.Location{URI: "file:///project/server.go",
				Range: protocol.Range{Start: protocol.Position{Line: 25, Character: 1}, End: protocol.Position{Line: 25, Character: 10}}}},
		{Name: "Server", Kind: protocol.SKClass,
			Location: protocol.Location{URI: "file:///project/server.go",
				Range: protocol.Range{Start: protocol.Position{Line: 10, Character: 1}, End: protocol.Position{Line: 10, Character: 7}}}},
		{Name: "handleRequest", Kind: protocol.SKMethod,
			Location: protocol.Location{URI: "file:///project/handler.go",
				Range: protocol.Range{Start: protocol.Position{Line: 42, Character: 1}, End: protocol.Position{Line: 42, Character: 14}}}},
	})}
}

func mustMarshal(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic("mock: marshal: " + err.Error())
	}
	return data
}

// ── in-process mock connection ────────────────────────────────────────

type mockConn struct {
	Stdin  io.WriteCloser
	Stdout io.ReadCloser

	cancel chan struct{}
	wg     sync.WaitGroup
}

func newMockConn() *mockConn {
	mockStdin, clientStdin := io.Pipe()
	clientStdout, mockStdout := io.Pipe()

	conn := &mockConn{
		Stdin:  clientStdin,
		Stdout: clientStdout,
		cancel: make(chan struct{}),
	}

	conn.wg.Add(1)
	go conn.mockLoop(mockStdin, mockStdout)
	return conn
}

func (mc *mockConn) mockLoop(stdin io.ReadCloser, stdout io.WriteCloser) {
	defer mc.wg.Done()
	reader := bufio.NewReader(stdin)

	for {
		select {
		case <-mc.cancel:
			return
		default:
		}

		body, err := readMessage(reader)
		if err != nil {
			return
		}
		if body == nil || len(body) == 0 {
			continue
		}

		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			continue
		}

		switch req.Method {
		case protocol.MethodInitialize:
			resp := protocol.Response{JSONRPC: "2.0", ID: req.ID, Result: mustMarshal(mockCapabilities())}
			writeMessage(stdout, mustMarshal(resp))
		case protocol.MethodShutdown:
			resp := protocol.Response{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage("null")}
			writeMessage(stdout, mustMarshal(resp))
		case protocol.MethodExit:
			return
		case protocol.MethodDefinition:
			writeMessage(stdout, mustMarshal(mockDefinitionResponse(req.ID)))
		case protocol.MethodReferences:
			writeMessage(stdout, mustMarshal(mockReferencesResponse(req.ID)))
		case protocol.MethodHover:
			writeMessage(stdout, mustMarshal(mockHoverResponse(req.ID)))
		case protocol.MethodDocumentSymbol:
			writeMessage(stdout, mustMarshal(mockDocumentSymbolsResponse(req.ID)))
		case protocol.MethodImplementation:
			writeMessage(stdout, mustMarshal(mockImplementationResponse(req.ID)))
		case protocol.MethodPrepareCallHierarchy:
			writeMessage(stdout, mustMarshal(mockCallHierarchyResponse(req.ID)))
		case protocol.MethodIncomingCalls:
			writeMessage(stdout, mustMarshal(mockIncomingCallsResponse(req.ID)))
		case protocol.MethodOutgoingCalls:
			writeMessage(stdout, mustMarshal(mockOutgoingCallsResponse(req.ID)))
		case protocol.MethodWorkspaceSymbol:
			writeMessage(stdout, mustMarshal(mockWorkspaceSymbolResponse(req.ID)))
		default:
			resp := protocol.Response{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage("null")}
			writeMessage(stdout, mustMarshal(resp))
		}
	}
}

func (mc *mockConn) Close() {
	close(mc.cancel)
	mc.Stdin.Close()
	mc.Stdout.Close()
	mc.wg.Wait()
}
