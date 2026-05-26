package lp

import (
	"testing"

	"github.com/johnny1110/evva/pkg/event"
	"github.com/johnny1110/evva/pkg/ui"
)

// Compile-time checks that *UI satisfies the public contracts. Failure here
// means the ui.UI / event.Sink surface drifted and lp hasn't kept up.
var (
	_ ui.UI      = (*UI)(nil)
	_ event.Sink = (*UI)(nil)
)

func TestNew(t *testing.T) {
	u := New("/tmp/evva-lp-test-home")
	if u == nil {
		t.Fatal("New returned nil")
	}
	if u.program == nil {
		t.Fatal("program not initialised")
	}
	if u.model == nil {
		t.Fatal("model not initialised")
	}
}

// TestRegisteredAsLp pins the side effect of register.go: importing this
// package must register the "lp" UI so `evva -tui lp` resolves.
func TestRegisteredAsLp(t *testing.T) {
	factory, ok := ui.Lookup("lp")
	if !ok {
		t.Fatal(`ui.Lookup("lp") = _, false; register.go init() did not run`)
	}
	if got := factory("/tmp/evva-lp-test-home"); got == nil {
		t.Fatal("lp factory returned a nil ui.UI")
	}
}
