package ui_test

import (
	"context"
	"testing"

	"github.com/johnny1110/evva/pkg/event"
	"github.com/johnny1110/evva/pkg/ui"
)

// stubUI is a do-nothing ui.UI used to exercise the registry without
// dragging in a real frontend.
type stubUI struct{ home string }

func (stubUI) Emit(event.Event)          {}
func (stubUI) Attach(ui.Controller)      {}
func (stubUI) Run(context.Context) error { return nil }

func TestRegistryRegisterLookup(t *testing.T) {
	const name = "stub-test-ui"
	ui.Register(name, func(home string) ui.UI { return stubUI{home: home} })

	factory, ok := ui.Lookup(name)
	if !ok {
		t.Fatalf("Lookup(%q) = _, false; want a factory", name)
	}
	got := factory("/tmp/evva-home")
	if s, ok := got.(stubUI); !ok || s.home != "/tmp/evva-home" {
		t.Fatalf("factory built %#v; want stubUI{home:\"/tmp/evva-home\"}", got)
	}
}

func TestRegistryLookupUnknown(t *testing.T) {
	if _, ok := ui.Lookup("definitely-not-registered"); ok {
		t.Fatal("Lookup of unknown name reported ok=true")
	}
}

func TestRegistryNamesSortedAndPresent(t *testing.T) {
	ui.Register("zzz-names-test", func(string) ui.UI { return stubUI{} })
	ui.Register("aaa-names-test", func(string) ui.UI { return stubUI{} })

	names := ui.Names()
	var sawA, sawZ bool
	var aIdx, zIdx int
	for i, n := range names {
		switch n {
		case "aaa-names-test":
			sawA, aIdx = true, i
		case "zzz-names-test":
			sawZ, zIdx = true, i
		}
	}
	if !sawA || !sawZ {
		t.Fatalf("Names() = %v; want both aaa-/zzz- entries present", names)
	}
	if aIdx > zIdx {
		t.Fatalf("Names() not sorted: aaa- at %d after zzz- at %d (%v)", aIdx, zIdx, names)
	}
}
