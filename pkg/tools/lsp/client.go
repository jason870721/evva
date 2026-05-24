package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/johnny1110/evva/pkg/tools/lsp/protocol"
)

// NotificationHandler receives incoming notifications from the server.
type NotificationHandler func(params json.RawMessage)

// response is the internal envelope for a pending request.
type response struct {
	result json.RawMessage
	err    error
}

// Client is a JSON-RPC 2.0 client that speaks the LSP framing protocol
// (Content-Length header + JSON body) over stdin/stdout of a child process.
type Client struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser

	mu        sync.Mutex
	pending   map[int64]chan *response
	handlers  map[string]NotificationHandler
	nextID    int64
	connCtx   context.Context
	connClose context.CancelFunc
}

// Start spawns the LSP server process and starts the reader goroutine.
// The caller owns the returned Client and must call Close to clean up.
func Start(ctx context.Context, command string, args []string, logger *slog.Logger) (*Client, error) {
	connCtx, connClose := context.WithCancel(ctx)

	cmd := exec.CommandContext(connCtx, command, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		connClose()
		return nil, fmt.Errorf("lsp: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		connClose()
		return nil, fmt.Errorf("lsp: stdout pipe: %w", err)
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		connClose()
		return nil, fmt.Errorf("lsp: start %s: %w", command, err)
	}

	c := &Client{
		cmd:       cmd,
		stdin:     stdin,
		stdout:    stdout,
		pending:   make(map[int64]chan *response),
		handlers:  make(map[string]NotificationHandler),
		connCtx:   connCtx,
		connClose: connClose,
	}

	go c.readLoop(logger)
	return c, nil
}

// Request sends a JSON-RPC request and blocks until the response arrives or
// ctx expires. Returns the raw JSON result field or an error.
func (c *Client) Request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	// Check context before sending — avoid writing to a pipe when the
	// caller has already cancelled.
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	id := atomic.AddInt64(&c.nextID, 1)
	respCh := make(chan *response, 1)

	c.mu.Lock()
	c.pending[id] = respCh
	c.mu.Unlock()

	if err := c.send(id, method, params); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}

	select {
	case <-ctx.Done():
		// Best-effort cancellation notification; ignore errors since we are
		// already timing out.
		_ = c.notify(context.Background(), protocol.MethodCancelRequest, protocol.CancelParams{ID: id})
		// Drain the response briefly in case it arrives during shutdown.
		select {
		case resp := <-respCh:
			c.mu.Lock()
			delete(c.pending, id)
			c.mu.Unlock()
			return resp.result, resp.err
		case <-time.After(500 * time.Millisecond):
		}
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	case resp := <-respCh:
		return resp.result, resp.err
	}
}

// Notify sends a JSON-RPC notification (no id, no response expected).
func (c *Client) Notify(ctx context.Context, method string, params any) error {
	return c.send(0, method, params)
}

// OnNotify registers a handler for incoming notifications. Handlers are
// called synchronously from the reader goroutine — keep them fast.
func (c *Client) OnNotify(method string, handler NotificationHandler) {
	c.mu.Lock()
	c.handlers[method] = handler
	c.mu.Unlock()
}

// Close sends a graceful shutdown sequence: shutdown request → exit
// notification → kill process. Callers should use Server.Stop instead of
// calling this directly — Server manages the handshake timeout.
func (c *Client) Close() error {
	c.connClose()
	if c.cmd != nil {
		return c.cmd.Wait()
	}
	return nil
}

// Process returns the underlying os.Process for signal operations.
func (c *Client) Process() *os.Process {
	return c.cmd.Process
}

// ── internal ───────────────────────────────────────────────────────────

// notify is the internal notification sender that ignores the outgoing
// context. Used by Request for cancellation and by the public Notify.
func (c *Client) notify(ctx context.Context, method string, params any) error {
	return c.send(0, method, params)
}

// send marshals a JSON-RPC message and writes it to stdin.
// id=0 means notification (omitted from JSON).
func (c *Client) send(id int64, method string, params any) error {
	msg := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
	}
	if id != 0 {
		msg["id"] = id
	}
	if params != nil {
		msg["params"] = params
	}

	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("lsp: marshal %s: %w", method, err)
	}

	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	if _, err := c.stdin.Write([]byte(header)); err != nil {
		return fmt.Errorf("lsp: write header: %w", err)
	}
	if _, err := c.stdin.Write(body); err != nil {
		return fmt.Errorf("lsp: write body: %w", err)
	}
	return nil
}

// readLoop is the single goroutine that reads messages from stdout.
// It parses the Content-Length header, reads the body, and dispatches
// to either a pending request channel or a notification handler.
func (c *Client) readLoop(logger *slog.Logger) {
	reader := bufio.NewReader(c.stdout)
	for {
		select {
		case <-c.connCtx.Done():
			return
		default:
		}

		body, err := readMessage(reader)
		if err != nil {
			if logger != nil && !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) {
				logger.Debug("lsp.client.read_error", "err", err)
			}
			return
		}
		if body == nil {
			continue
		}
		c.dispatch(body)
	}
}

// readMessage reads one LSP-framed message from r. Returns nil body with nil
// error for EOF on a clean stream (connection closed).
func readMessage(r *bufio.Reader) ([]byte, error) {
	// Read headers until the empty line.
	var contentLen int = -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			v := strings.TrimSpace(line[len("content-length:"):])
			n, err := strconv.Atoi(v)
			if err != nil {
				return nil, fmt.Errorf("lsp: bad Content-Length %q: %w", v, err)
			}
			contentLen = n
		}
		// Ignore other headers (Content-Type, etc.)
	}

	if contentLen < 0 {
		return nil, fmt.Errorf("lsp: missing Content-Length header")
	}
	if contentLen == 0 {
		return nil, nil
	}

	body := make([]byte, contentLen)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, fmt.Errorf("lsp: read body (%d bytes): %w", contentLen, err)
	}
	return body, nil
}

// dispatch routes a raw JSON-RPC message body. If it has an "id" field it's
// a response — route it to the matching pending channel. Otherwise it's a
// notification — call the registered handler.
func (c *Client) dispatch(body []byte) {
	var envelope struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return
	}

	if len(envelope.ID) > 0 {
		// Response — parse the numeric id.
		var rid int64
		if err := json.Unmarshal(envelope.ID, &rid); err != nil {
			return
		}
		c.mu.Lock()
		ch, ok := c.pending[rid]
		if ok {
			delete(c.pending, rid)
		}
		c.mu.Unlock()
		if !ok {
			return
		}
		var resp response
		if envelope.Error != nil {
			resp.err = fmt.Errorf("lsp: %s (code %d)", envelope.Error.Message, envelope.Error.Code)
		} else {
			resp.result = envelope.Result
		}
		select {
		case ch <- &resp:
		default:
		}
		return
	}

	// Notification — look up handler.
	c.mu.Lock()
	handler, ok := c.handlers[envelope.Method]
	c.mu.Unlock()
	if ok && handler != nil {
		handler(envelope.Params)
	}
}

// writeMessage is used by the mock server test helper. It writes a single
// LSP-framed message body to w.
func writeMessage(w io.Writer, body []byte) error {
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	if _, err := w.Write([]byte(header)); err != nil {
		return err
	}
	_, err := w.Write(body)
	return err
}
