package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/johnny1110/evva/pkg/tools/lsp/protocol"
)

func TestReadMessageValid(t *testing.T) {
	// Content-Length must match the exact byte length of the body.
	body := `{"ok":true}`
	msg := "Content-Length: " + itoa(len(body)) + "\r\n\r\n" + body
	r := bufio.NewReader(strings.NewReader(msg))

	got, err := readMessage(r)
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	if string(got) != body {
		t.Errorf("expected body %q, got %q", body, string(got))
	}
}

// itoa is a simple int-to-string helper to avoid strconv import in tests.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

func TestReadMessageMissingContentLength(t *testing.T) {
	msg := "Content-Type: text/plain\r\n\r\nbody"
	r := bufio.NewReader(strings.NewReader(msg))

	_, err := readMessage(r)
	if err == nil {
		t.Error("expected error for missing Content-Length")
	}
}

func TestReadMessageExtraHeaders(t *testing.T) {
	msg := "Content-Type: application/vscode-jsonrpc; charset=utf-8\r\nContent-Length: 3\r\n\r\nx{}y"
	r := bufio.NewReader(strings.NewReader(msg))

	body, err := readMessage(r)
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	if string(body) != "x{}" {
		t.Errorf("expected body %q, got %q", "x{}", string(body))
	}
}

func TestReadMessageEmpty(t *testing.T) {
	msg := "Content-Length: 0\r\n\r\n"
	r := bufio.NewReader(strings.NewReader(msg))

	body, err := readMessage(r)
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	if body != nil {
		t.Errorf("expected nil body for zero Content-Length, got %q", string(body))
	}
}

func TestReadMessageEOF(t *testing.T) {
	r := bufio.NewReader(strings.NewReader(""))
	_, err := readMessage(r)
	if err != io.EOF {
		t.Errorf("expected EOF, got %v", err)
	}
}

func TestClientWithMockConn(t *testing.T) {
	conn := newMockConn()
	defer conn.Close()

	client := &Client{
		stdin:     conn.Stdin,
		stdout:    conn.Stdout,
		pending:   make(map[int64]chan *response),
		handlers:  make(map[string]NotificationHandler),
		connCtx:   context.Background(),
		connClose: func() {},
	}
	go client.readLoop(nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Initialize.
	raw, err := client.Request(ctx, protocol.MethodInitialize, protocol.InitializeParams{
		ProcessID:    1234,
		RootURI:      "file:///test",
		Capabilities: protocol.DefaultClientCapabilities(),
	})
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}

	var initResult protocol.InitializeResult
	if err := json.Unmarshal(raw, &initResult); err != nil {
		t.Fatalf("unmarshal initialize result: %v", err)
	}
	if !initResult.Capabilities.DefinitionProvider {
		t.Error("expected DefinitionProvider to be true")
	}

	// Initialized notification.
	if err := client.Notify(ctx, protocol.MethodInitialized, nil); err != nil {
		t.Fatalf("initialized: %v", err)
	}

	// Definition request.
	raw, err = client.Request(ctx, protocol.MethodDefinition, protocol.TextDocumentPositionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test/main.go"},
		Position:     protocol.Position{Line: 5, Character: 10},
	})
	if err != nil {
		t.Fatalf("definition: %v", err)
	}

	var loc protocol.Location
	if err := json.Unmarshal(raw, &loc); err != nil {
		t.Fatalf("unmarshal location: %v", err)
	}
	if loc.URI != "file:///project/other.go" {
		t.Errorf("expected URI file:///project/other.go, got %s", loc.URI)
	}
}

func TestClientConcurrentRequests(t *testing.T) {
	conn := newMockConn()
	defer conn.Close()

	client := &Client{
		stdin:     conn.Stdin,
		stdout:    conn.Stdout,
		pending:   make(map[int64]chan *response),
		handlers:  make(map[string]NotificationHandler),
		connCtx:   context.Background(),
		connClose: func() {},
	}
	go client.readLoop(nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := client.Request(ctx, protocol.MethodInitialize, protocol.InitializeParams{
		ProcessID:    1234,
		RootURI:      "file:///test",
		Capabilities: protocol.DefaultClientCapabilities(),
	}); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	_ = client.Notify(ctx, protocol.MethodInitialized, nil)

	type result struct {
		id  int
		err error
	}
	results := make(chan result, 3)
	for i := 0; i < 3; i++ {
		go func(id int) {
			_, err := client.Request(ctx, protocol.MethodDefinition, protocol.TextDocumentPositionParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test/main.go"},
				Position:     protocol.Position{Line: uint32(id), Character: 1},
			})
			results <- result{id: id, err: err}
		}(i)
	}
	for i := 0; i < 3; i++ {
		r := <-results
		if r.err != nil {
			t.Errorf("request %d failed: %v", r.id, r.err)
		}
	}
}

func TestClientCancelContext(t *testing.T) {
	conn := newMockConn()
	defer conn.Close()

	client := &Client{
		stdin:     conn.Stdin,
		stdout:    conn.Stdout,
		pending:   make(map[int64]chan *response),
		handlers:  make(map[string]NotificationHandler),
		connCtx:   context.Background(),
		connClose: func() {},
	}
	go client.readLoop(nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.Request(ctx, protocol.MethodDefinition, protocol.TextDocumentPositionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test/main.go"},
		Position:     protocol.Position{Line: 1, Character: 1},
	})
	if err == nil {
		t.Error("expected error from cancelled context")
	}
}
