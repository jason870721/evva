package bubbletea

import "github.com/johnny1110/evva/pkg/ui"

// Register the bundled reference TUI under the name "bubbletea" so a host
// can select it with `evva -tui bubbletea` (the default). Blank-import this
// package for the side effect:
//
//	import _ "github.com/johnny1110/evva/pkg/ui/bubbletea"
func init() {
	ui.Register("bubbletea", func(evvaHome string) ui.UI {
		return New(evvaHome)
	})
}
