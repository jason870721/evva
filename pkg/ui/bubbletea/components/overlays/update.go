package overlays

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/johnny1110/evva/pkg/update"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
)

// updateCheckResult is sent from the background goroutine to the overlay
// when the GitHub API call completes.
type updateCheckResult struct {
	release *update.Release
	err     error
}

// updateApplyResult is sent when the download+replace completes.
type updateApplyResult struct {
	err error
}

// Update is the /update overlay. It checks GitHub for a newer release and
// offers to download and apply it.
type Update struct {
	current string
	release *update.Release   // nil while checking
	errMsg  string
	phase   updatePhase
}

type updatePhase int

const (
	updateChecking updatePhase = iota
	updateReady
	updateApplying
	updateDone
)

// NewUpdate kicks off a background version check and returns the overlay.
func NewUpdate() *Update {
	u := &Update{
		current: update.CurrentVersion(),
		phase:   updateChecking,
	}
	return u
}

func (u *Update) Key() string  { return "update" }
func (u *Update) Modal() bool  { return true }
func (u *Update) Hint() string { return "[y] apply update · [n] cancel · [Esc] close" }

func (u *Update) Update(msg tea.Msg) (bool, tea.Cmd) {
	switch m := msg.(type) {
	case updateCheckResult:
		if m.err != nil {
			u.errMsg = m.err.Error()
			u.phase = updateReady
			return false, nil
		}
		latest := strings.TrimPrefix(m.release.Version, "v")
		cur := strings.TrimPrefix(u.current, "v")
		if latest == cur {
			u.phase = updateDone
			return false, nil
		}
		u.release = m.release
		u.phase = updateReady
		return false, nil

	case updateApplyResult:
		if m.err != nil {
			u.errMsg = m.err.Error()
			u.phase = updateReady
			return false, nil
		}
		u.phase = updateDone
		return false, nil

	case tea.KeyMsg:
		switch m.String() {
		case "esc", "ctrl+c":
			return true, nil
		case "y", "Y":
			if u.phase == updateReady && u.release != nil {
				u.phase = updateApplying
				u.errMsg = ""
				return false, func() tea.Msg {
					_, err := update.Apply(context.Background(), u.release)
					return updateApplyResult{err: err}
				}
			}
		case "n", "N":
			return true, nil
		}
	}
	return false, nil
}

// StartCheck returns the command that kicks off the background version check.
func (u *Update) StartCheck() tea.Cmd {
	return func() tea.Msg {
		release, err := update.Check(context.Background(), update.DefaultOwner, update.DefaultRepo)
		return updateCheckResult{release: release, err: err}
	}
}

func (u *Update) View(width int, th *theme.Theme) string {
	var b strings.Builder
	b.WriteString(th.PanelHeader.Render("▰ /UPDATE"))
	b.WriteByte('\n')
	b.WriteString(th.DimText.Render("Current version: " + u.current))
	b.WriteString("\n\n")

	switch u.phase {
	case updateChecking:
		b.WriteString(th.DimText.Render("Checking for updates..."))
	case updateReady:
		if u.errMsg != "" {
			b.WriteString(th.ErrorBanner.Render("✗ " + u.errMsg))
		} else if u.release != nil {
			b.WriteString(fmt.Sprintf("New version available: %s → %s\n",
				u.current, u.release.Version))
			b.WriteString(th.DimText.Render(u.release.URL))
			b.WriteString("\n")
		}
	case updateApplying:
		b.WriteString("Downloading and applying update...")
	case updateDone:
		if u.errMsg != "" {
			b.WriteString(th.ErrorBanner.Render("✗ " + u.errMsg))
		} else if u.release != nil {
			b.WriteString(th.ContextFill.Render("Updated to " + u.release.Version + "!"))
			b.WriteByte('\n')
			b.WriteString(th.DimText.Render("Please restart evva for the update to take effect."))
		} else {
			b.WriteString(th.ContextFill.Render("evva is up-to-date (" + u.current + ")"))
		}
	}

	b.WriteByte('\n')
	if u.phase == updateReady && u.release != nil && u.errMsg == "" {
		b.WriteByte('\n')
		b.WriteString(th.FooterHint.Render("[y] apply update · [n / Esc] cancel"))
	} else if u.phase == updateDone || u.errMsg != "" {
		b.WriteByte('\n')
		b.WriteString(th.FooterHint.Render("[Esc] close"))
	}

	return th.InputBorder.Render(strings.TrimRight(b.String(), "\n"))
}
