package app

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/johnny1110/evva/internal/swarm/tui/client"
)

// Run attaches to the space named by ref (id or name) and blocks until the
// operator detaches (q). member, when non-empty, opens focused on that
// member's stream. version is the CLI's own version for the /healthz skew
// probe — a mismatch becomes a status-line notice, never a refusal (unknown
// event kinds are ignored by the reducer, so newer services degrade soft).
func Run(addr, token, ref, member, version string) error {
	cli := client.New(addr, token)
	space, err := cli.ResolveSpace(ref)
	if err != nil {
		return err
	}
	if space.Status != "running" {
		return fmt.Errorf("space %q is %s — `evva swarm run %s` first", ref, space.Status, ref)
	}

	skew := ""
	if h, err := cli.Health(); err == nil && version != "" && h.Version != "" && h.Version != version {
		skew = fmt.Sprintf("service %s ≠ cli %s — consider matching versions", h.Version, version)
	}

	stream := client.Dial(addr, token, space.ID)
	defer stream.Close()

	m := New(cli, stream, space)
	if member != "" {
		m.focus = member
	}
	m.toast = skew

	_, err = tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}
