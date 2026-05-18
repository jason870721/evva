package mouse

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/johnny1110/evva/internal/ui/bubbletea_v2/events"
)

func TestCopyProducesCmd(t *testing.T) {
	cmd := Copy("hello")
	if cmd == nil {
		t.Fatal("Copy should never return a nil cmd")
	}
	msg := cmd()
	c, ok := msg.(events.ClipboardMsg)
	if !ok {
		t.Fatalf("expected ClipboardMsg, got %T", msg)
	}
	if !c.OK {
		t.Errorf("write of small payload should succeed, got Err=%v", c.Err)
	}
	if c.Size != len("hello") {
		t.Errorf("Size = %d, want %d", c.Size, len("hello"))
	}
}

func TestCopyEmptyPayload(t *testing.T) {
	msg := Copy("")()
	c, _ := msg.(events.ClipboardMsg)
	if c.OK {
		t.Errorf("empty payload should report !OK")
	}
	if c.Err == nil {
		t.Errorf("empty payload should report an Err")
	}
}

func TestCopyTooLarge(t *testing.T) {
	huge := strings.Repeat("x", osc52MaxPayload+1)
	c, _ := Copy(huge)().(events.ClipboardMsg)
	if c.Size != len(huge) {
		t.Errorf("Size = %d, want %d", c.Size, len(huge))
	}
	if c.OK {
		// Native clipboard may accept large payloads (pbcopy has no
		// hard limit). Only OSC52 path enforces the size cap.
		if c.Method == "osc52" {
			t.Errorf("osc52 oversized payload should report !OK")
		}
	} else {
		if c.Err == nil {
			t.Errorf("failed payload should populate Err")
		}
	}
}

func TestIsWheelEvent(t *testing.T) {
	cases := []struct {
		name   string
		button tea.MouseButton
		want   bool
	}{
		{"wheel up", tea.MouseButtonWheelUp, true},
		{"wheel down", tea.MouseButtonWheelDown, true},
		{"wheel left", tea.MouseButtonWheelLeft, true},
		{"wheel right", tea.MouseButtonWheelRight, true},
		{"left click", tea.MouseButtonLeft, false},
		{"right click", tea.MouseButtonRight, false},
		{"middle click", tea.MouseButtonMiddle, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := tea.MouseMsg{Button: tc.button}
			if got := IsWheelEvent(msg); got != tc.want {
				t.Errorf("IsWheelEvent(%v) = %v, want %v", tc.button, got, tc.want)
			}
		})
	}
}
