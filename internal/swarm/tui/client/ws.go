package client

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sync"
	"time"

	"golang.org/x/net/websocket"

	"github.com/johnny1110/evva/internal/swarm/tui/wire"
)

// Msg is one Stream emission: exactly one field is set. Event is a live wire
// event; CmdErr is the socket's error echo for a failed inbound command;
// Status is a connection-state change (the app re-hydrates on every
// Connected=true — initial connect and reconnect alike, the v1.7.4
// non-destructive contract).
type Msg struct {
	Event  *wire.Event
	CmdErr *wire.CommandError
	Status *Status
}

// Status reports the socket's connection state. Attempt counts consecutive
// failed dials while disconnected (the "reconnecting (nth)…" status line).
type Status struct {
	Connected bool
	Attempt   int
	Err       error
}

// Backoff bounds for the reconnect loop (PRD §5.2: 1s → 15s cap).
const (
	backoffMin = time.Second
	backoffMax = 15 * time.Second
)

// Stream manages one space's live socket: dial, decode, reconnect with
// exponential backoff, and outbound command sends. Messages() delivers
// everything; the channel closes only on Close().
type Stream struct {
	addr, token, space string

	msgs chan Msg
	out  chan wire.Command
	done chan struct{}

	mu     sync.Mutex
	closed bool

	// dialFn is swappable for tests (an in-process pipe server).
	dialFn func() (*websocket.Conn, error)
}

// Dial starts a stream for one space. It returns immediately; the first
// Status message reports the initial connect (or its failure).
func Dial(addr, token, space string) *Stream {
	s := &Stream{
		addr: addr, token: token, space: space,
		msgs: make(chan Msg, 256),
		out:  make(chan wire.Command, 16),
		done: make(chan struct{}),
	}
	s.dialFn = s.dialWS
	go s.run()
	return s
}

// Messages is the stream's delivery channel. Closed on Close().
func (s *Stream) Messages() <-chan Msg { return s.msgs }

// Send queues one inbound command (gate reply, leader run). Queued commands
// survive a reconnect — they send on the next live socket. A full queue
// errors instead of blocking the UI loop.
func (s *Stream) Send(cmd wire.Command) error {
	select {
	case s.out <- cmd:
		return nil
	default:
		return fmt.Errorf("command queue full — the socket has been down too long")
	}
}

// Close stops the stream and closes Messages().
func (s *Stream) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	close(s.done)
}

func (s *Stream) dialWS() (*websocket.Conn, error) {
	u := fmt.Sprintf("ws://%s/ws?token=%s&space=%s",
		s.addr, url.QueryEscape(s.token), url.QueryEscape(s.space))
	return websocket.Dial(u, "", "http://"+s.addr)
}

// emit delivers one Msg unless the stream is shutting down. The channel is
// generously buffered; a consumer that stopped reading (the program is
// exiting) must not deadlock the socket goroutine, so a full buffer drops.
func (s *Stream) emit(m Msg) {
	select {
	case s.msgs <- m:
	case <-s.done:
	default:
	}
}

// run is the manage loop: dial → pump until the socket dies → backoff →
// redial, forever, until Close().
func (s *Stream) run() {
	defer close(s.msgs)
	attempt := 0
	for {
		select {
		case <-s.done:
			return
		default:
		}

		ws, err := s.dialFn()
		if err != nil {
			attempt++
			s.emit(Msg{Status: &Status{Connected: false, Attempt: attempt, Err: err}})
			if !s.sleep(backoffFor(attempt)) {
				return
			}
			continue
		}
		attempt = 0
		s.emit(Msg{Status: &Status{Connected: true}})
		err = s.pump(ws)
		ws.Close()
		select {
		case <-s.done:
			return
		default:
		}
		attempt = 1
		s.emit(Msg{Status: &Status{Connected: false, Attempt: attempt, Err: err}})
		if !s.sleep(backoffFor(attempt)) {
			return
		}
	}
}

// pump runs one live connection: a writer goroutine draining the command
// queue and the blocking read loop. Returns the read error that ended it.
func (s *Stream) pump(ws *websocket.Conn) error {
	// connDone releases the writer when the READ side dies — without it the
	// writer would sit in its select until the next command or Close() and
	// pump could never return to redial.
	connDone := make(chan struct{})
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for {
			select {
			case cmd := <-s.out:
				b, err := json.Marshal(cmd)
				if err != nil {
					continue
				}
				if err := websocket.Message.Send(ws, string(b)); err != nil {
					// Requeue so the command retries on the next connection —
					// a gate reply must not vanish with a dropped socket.
					select {
					case s.out <- cmd:
					default:
					}
					return
				}
			case <-connDone:
				return
			case <-s.done:
				return
			}
		}
	}()

	var readErr error
	for {
		var raw string
		if err := websocket.Message.Receive(ws, &raw); err != nil {
			readErr = err
			break
		}
		ev, cmdErr := wire.ParseFrame([]byte(raw))
		switch {
		case cmdErr != nil:
			s.emit(Msg{CmdErr: cmdErr})
		case ev != nil:
			s.emit(Msg{Event: ev})
		}
	}
	close(connDone)
	ws.Close() // unblocks the writer's Send if it is mid-write
	<-writerDone
	return readErr
}

// sleep waits d or until Close(); false = closing.
func (s *Stream) sleep(d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-s.done:
		return false
	}
}

// backoffFor doubles from backoffMin per consecutive attempt, capped.
func backoffFor(attempt int) time.Duration {
	d := backoffMin << max(attempt-1, 0)
	if d > backoffMax || d <= 0 {
		return backoffMax
	}
	return d
}
