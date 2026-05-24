package theme

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func init() {
	// Force truecolor so the rendered-output assertions below see
	// fully-formed 24-bit escape sequences regardless of host TERM.
	lipgloss.SetColorProfile(termenv.TrueColor)
}

// TestDefaultDiffStylesWhiteOnSolid is the M2 contract: DiffAdd and
// DiffRemove must use true white as the foreground and the matching
// solid acid-green / glitch-red as the background. v1's pale-bg /
// neon-fg combo is gone.
//
// We compare on the lipgloss.Color hex strings (not the rendered
// escape sequences), so the test is stable across termenv color
// quantization differences across runtimes / lipgloss versions.
func TestDefaultDiffStylesWhiteOnSolid(t *testing.T) {
	th := Default()

	for _, tc := range []struct {
		name string
		got  lipgloss.Style
		fg   lipgloss.Color
		bg   lipgloss.Color
	}{
		{"DiffAdd", th.DiffAdd, fg, diffAddBg},
		{"DiffRemove", th.DiffRemove, fg, diffRemoveBg},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if gotFg := tc.got.GetForeground(); gotFg != tc.fg {
				t.Errorf("fg = %v, want %v", gotFg, tc.fg)
			}
			if gotBg := tc.got.GetBackground(); gotBg != tc.bg {
				t.Errorf("bg = %v, want %v", gotBg, tc.bg)
			}
			if !tc.got.GetBold() {
				t.Errorf("expected Bold(true)")
			}
		})
	}

	// Context rows must NOT have a background — they read as "ambient"
	// against the colored add/remove blocks. We check the rendered
	// output rather than GetBackground() because lipgloss returns a
	// NoColor{} struct (not the empty lipgloss.Color string) when
	// no bg is set, and that's awkward to compare against directly.
	if out := th.DiffContext.Render("x"); strings.Contains(out, "\x1b[48") {
		t.Errorf("DiffContext should not set a background, got rendered: %q", out)
	}
}

// TestThemeRevSet — Default() returns a *Theme with Rev >= 1 so a
// zero-value Rev stays a discriminator for "uninitialised cache".
func TestThemeRevSet(t *testing.T) {
	if r := Default().Rev; r == 0 {
		t.Errorf("Default().Rev = 0, want >= 1")
	}
}

// TestGlyphFallback — unknown statuses fall back to the dim "·".
func TestGlyphFallback(t *testing.T) {
	g := Default().Glyph("not_a_real_status")
	if g.Symbol != "·" {
		t.Errorf("unknown status glyph = %q, want %q", g.Symbol, "·")
	}
	if g.Color != dim {
		t.Errorf("unknown status color = %v, want %v", g.Color, dim)
	}
}

// TestSpinnerStyleHit — known animated statuses return ok=true; known
// terminal statuses return ok=false.
func TestSpinnerStyleHit(t *testing.T) {
	th := Default()
	if _, ok := th.SpinnerStyle("thinking"); !ok {
		t.Errorf("thinking is an animated status; want ok=true")
	}
	if _, ok := th.SpinnerStyle("crushed"); ok {
		t.Errorf("crushed is terminal; want ok=false")
	}
}
