package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// prettyHandler renders log records as a single compact line:
//
//	15:04:05.123 INFO  agent.go:42 llm call ok agentId=abc bytes=42
//
// It keeps slog's structured semantics (groups, WithAttrs, ReplaceAttr-style
// resolution) but drops the noise that makes the stdlib text handler ugly:
// long absolute source paths and full RFC3339 timestamps.
type prettyHandler struct {
	w         io.Writer
	mu        *sync.Mutex
	level     slog.Leveler
	addSource bool
	attrs     []slog.Attr
	groups    []string
}

func newPretty(w io.Writer, opts *slog.HandlerOptions) *prettyHandler {
	if opts == nil {
		opts = &slog.HandlerOptions{}
	}
	var lvl slog.Leveler = slog.LevelInfo
	if opts.Level != nil {
		lvl = opts.Level
	}
	return &prettyHandler{
		w:         w,
		mu:        &sync.Mutex{},
		level:     lvl,
		addSource: opts.AddSource,
	}
}

func (h *prettyHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.level.Level()
}

func (h *prettyHandler) Handle(_ context.Context, r slog.Record) error {
	var sb strings.Builder
	sb.Grow(128)

	if !r.Time.IsZero() {
		sb.WriteString(r.Time.Format("15:04:05.000"))
		sb.WriteByte(' ')
	}
	sb.WriteString(levelLabel(r.Level))
	sb.WriteByte(' ')

	if h.addSource && r.PC != 0 {
		frame, _ := runtime.CallersFrames([]uintptr{r.PC}).Next()
		file := frame.File
		if i := strings.LastIndex(file, "/"); i >= 0 {
			file = file[i+1:]
		}
		fmt.Fprintf(&sb, "%s:%d ", file, frame.Line)
	}
	sb.WriteString(r.Message)

	for _, a := range h.attrs {
		h.writeAttr(&sb, a, h.groups)
	}
	r.Attrs(func(a slog.Attr) bool {
		h.writeAttr(&sb, a, h.groups)
		return true
	})
	sb.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.w.Write([]byte(sb.String()))
	return err
}

func (h *prettyHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	nh := *h
	nh.attrs = append(append([]slog.Attr(nil), h.attrs...), attrs...)
	return &nh
}

func (h *prettyHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	nh := *h
	nh.groups = append(append([]string(nil), h.groups...), name)
	return &nh
}

func (h *prettyHandler) writeAttr(sb *strings.Builder, a slog.Attr, groups []string) {
	if a.Key == "" {
		return
	}
	a.Value = a.Value.Resolve()
	if a.Value.Kind() == slog.KindGroup {
		nested := append(groups, a.Key)
		for _, sub := range a.Value.Group() {
			h.writeAttr(sb, sub, nested)
		}
		return
	}
	sb.WriteByte(' ')
	if len(groups) > 0 {
		sb.WriteString(strings.Join(groups, "."))
		sb.WriteByte('.')
	}
	sb.WriteString(a.Key)
	sb.WriteByte('=')
	v := a.Value.String()
	if needsQuote(v) {
		sb.WriteString(strconv.Quote(v))
	} else {
		sb.WriteString(v)
	}
}

func levelLabel(l slog.Level) string {
	switch {
	case l < slog.LevelInfo:
		return "DEBUG"
	case l < slog.LevelWarn:
		return "INFO "
	case l < slog.LevelError:
		return "WARN "
	default:
		return "ERROR"
	}
}

func needsQuote(s string) bool {
	if s == "" {
		return true
	}
	for _, r := range s {
		if r == ' ' || r == '=' || r == '"' || r < 0x20 {
			return true
		}
	}
	return false
}
