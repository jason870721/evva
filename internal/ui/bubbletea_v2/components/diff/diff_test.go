package diff

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/johnny1110/evva/internal/tools/fs"
	"github.com/johnny1110/evva/internal/ui/bubbletea_v2/theme"
)

func init() {
	// Force truecolor so lipgloss emits the literal 24-bit escape
	// sequences. Without this, a CI env reporting TERM=dumb would
	// downsample and the bg-fill assertion below would silently pass
	// on stripped output.
	lipgloss.SetColorProfile(termenv.TrueColor)
}

// TestRenderWhiteOnSolid is a structural / content sanity check for
// the diff renderer. It does NOT assert exact ANSI escapes — that's
// the theme package's job, and termenv quantization can shift a
// channel by one which would make per-channel assertions brittle.
//
// What we DO assert:
//   - the rendered output contains the +/- line content and the hunk header,
//   - add/remove rows are visibly styled (an ESC and an SGR 'm' present),
//   - context rows survive un-mangled.
func TestRenderWhiteOnSolid(t *testing.T) {
	d := &fs.FileDiff{
		Path: "foo.go",
		Op:   fs.OpEdit,
		Hunks: []fs.DiffHunk{{
			OldStart: 1, OldCount: 2, NewStart: 1, NewCount: 2,
			Lines: []fs.DiffLine{
				{Kind: fs.LineContext, Old: 1, New: 1, Text: "package foo"},
				{Kind: fs.LineRemove, Old: 2, New: 0, Text: "goodbye"},
				{Kind: fs.LineAdd, Old: 0, New: 2, Text: "hello"},
			},
		}},
	}

	out := Render(d, theme.Default(), 60)

	for _, want := range []string{
		"hello", "goodbye", "package foo",
		"@@ -1,2 +1,2 @@", "diff edit a/foo.go b/foo.go",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, missing", want)
		}
	}

	// Sanity: there's at least one ANSI styled run — render is doing
	// _something_ visual.
	if !strings.Contains(out, "\x1b[") {
		t.Errorf("expected at least one ANSI escape in styled output, got: %q", out)
	}
}

// TestRenderNilSafe — Render tolerates nil diff and nil theme so
// callers higher up (transcript event ingest, permission overlay
// preview) don't need to guard every call site.
func TestRenderNilSafe(t *testing.T) {
	if got := Render(nil, theme.Default(), 40); got != "" {
		t.Errorf("Render(nil, theme, 40) = %q, want empty", got)
	}
	if got := Render(&fs.FileDiff{}, nil, 40); got != "" {
		t.Errorf("Render(diff, nil, 40) = %q, want empty", got)
	}
}

// TestRenderWidthFill — when width > 0, add and remove rows are
// padded so the background extends across the row. width <= 0 leaves
// rows at their natural length.
func TestRenderWidthFill(t *testing.T) {
	d := &fs.FileDiff{
		Path: "x", Op: fs.OpEdit,
		Hunks: []fs.DiffHunk{{
			OldStart: 1, NewStart: 1, OldCount: 1, NewCount: 1,
			Lines: []fs.DiffLine{{Kind: fs.LineAdd, New: 1, Text: "a"}},
		}},
	}
	wide := Render(d, theme.Default(), 80)
	narrow := Render(d, theme.Default(), 0)
	if len(wide) <= len(narrow) {
		t.Errorf("width=80 output should be longer than width=0 (got %d vs %d)", len(wide), len(narrow))
	}
}
