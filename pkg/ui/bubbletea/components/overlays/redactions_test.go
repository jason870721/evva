package overlays

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/johnny1110/evva/pkg/ui"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
)

// redactionsCtrl is a ui.Controller stub that only answers Redactions.
// The overlay reads nothing else.
type redactionsCtrl struct {
	ui.Controller
	rows []ui.RedactionInfo
}

func (c redactionsCtrl) Redactions() []ui.RedactionInfo { return c.rows }

func sampleRows() []ui.RedactionInfo {
	return []ui.RedactionInfo{
		{
			Placeholder: "[REDACTED:github-token:4f2a]",
			RuleID:      "github-token",
			Why:         "GitHub access token",
			Value:       "ghp_016C7869F0BE4A1E9C2F3D5A7B8E0D2C4F6A",
			Count:       2,
		},
		{
			Placeholder: "[REDACTED:aws-access-key:9c31]",
			RuleID:      "aws-access-key",
			Why:         "AWS access key ID",
			Value:       "AKIAIOSFODNN7EXAMPLE",
			Count:       1,
		},
	}
}

func newTestRedactions(t *testing.T, rows []ui.RedactionInfo) *Redactions {
	t.Helper()
	o := NewRedactions(redactionsCtrl{rows: rows})
	if o == nil {
		t.Fatal("NewRedactions returned nil for a non-nil controller")
	}
	return o
}

func TestRedactionsHidesValuesUntilAsked(t *testing.T) {
	// The default view must be safe to leave on screen during a
	// screen-share. Anything else makes the panel itself a leak.
	o := newTestRedactions(t, sampleRows())
	view := o.View(80, theme.Default())

	for _, secret := range []string{"ghp_016C7869F0BE4A1E9C2F3D5A7B8E0D2C4F6A", "AKIAIOSFODNN7EXAMPLE"} {
		if strings.Contains(view, secret) {
			t.Errorf("%q was visible without an explicit reveal", secret)
		}
	}
	// The placeholder and the rule must still be there — that is what
	// makes a false positive diagnosable.
	if !strings.Contains(view, "[REDACTED:github-token:4f2a]") {
		t.Error("the placeholder should always be shown")
	}
	if !strings.Contains(view, "GitHub access token") {
		t.Error("the rule explanation should always be shown")
	}
}

func TestRedactionsRevealTogglesWithR(t *testing.T) {
	o := newTestRedactions(t, sampleRows())

	if done, _ := o.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}}); done {
		t.Fatal("'r' should not close the overlay")
	}
	shown := o.View(80, theme.Default())
	if !strings.Contains(shown, "ghp_016C7869F0BE4A1E9C2F3D5A7B8E0D2C4F6A") {
		t.Errorf("reveal did not show the value:\n%s", shown)
	}

	o.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if hidden := o.View(80, theme.Default()); strings.Contains(hidden, "ghp_016C7869") {
		t.Error("a second 'r' should hide the values again")
	}
}

func TestRedactionsShowsLengthWhileMasked(t *testing.T) {
	// Length alone often identifies which key it was, without the
	// characters going on screen.
	o := newTestRedactions(t, sampleRows())
	if v := o.View(80, theme.Default()); !strings.Contains(v, "(40 chars)") {
		t.Errorf("masked rows should carry the value length:\n%s", v)
	}
}

func TestRedactionsCountsOccurrences(t *testing.T) {
	o := newTestRedactions(t, sampleRows())
	v := o.View(80, theme.Default())
	if !strings.Contains(v, "2 secret(s), 3 occurrence(s)") {
		t.Errorf("summary line wrong:\n%s", v)
	}
	if !strings.Contains(v, "×2") {
		t.Errorf("repeat count missing from the row:\n%s", v)
	}
}

func TestRedactionsEmptyStateIsHonest(t *testing.T) {
	// "Nothing matched" is not "nothing is there", and the panel should
	// not let an operator read it as a clean bill of health.
	o := newTestRedactions(t, nil)
	v := o.View(80, theme.Default())
	if !strings.Contains(v, "Nothing has been redacted") {
		t.Errorf("missing empty state:\n%s", v)
	}
	if !strings.Contains(v, "not that none exist") {
		t.Errorf("the empty state should not read as a guarantee:\n%s", v)
	}
}

func TestRedactionsClosesOnEsc(t *testing.T) {
	o := newTestRedactions(t, sampleRows())
	if done, _ := o.Update(tea.KeyMsg{Type: tea.KeyEsc}); !done {
		t.Error("Esc should close the overlay")
	}
}

func TestRedactionsTruncatesAMultilineValue(t *testing.T) {
	// A PEM block is thousands of characters across dozens of lines and
	// would push the panel off screen.
	pem := "-----BEGIN RSA PRIVATE KEY-----\n" + strings.Repeat("MIIEowIBAAKCAQEA\n", 40) + "-----END RSA PRIVATE KEY-----"
	o := newTestRedactions(t, []ui.RedactionInfo{{
		Placeholder: "[REDACTED:private-key:1111]", RuleID: "private-key",
		Why: "PEM private key block", Value: pem, Count: 1,
	}})
	o.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})

	v := o.View(80, theme.Default())
	for _, line := range strings.Split(v, "\n") {
		if len(line) > 400 {
			t.Errorf("a revealed value produced a %d-char line", len(line))
		}
	}
	if strings.Count(v, "MIIEowIBAAKCAQEA") > 2 {
		t.Error("the whole PEM body was rendered instead of a truncation")
	}
}

func TestNewRedactionsToleratesNilController(t *testing.T) {
	if o := NewRedactions(nil); o != nil {
		t.Error("want nil so the App can hint instead of opening an empty panel")
	}
}
