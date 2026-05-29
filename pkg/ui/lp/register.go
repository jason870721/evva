package lp

import "github.com/johnny1110/evva/pkg/ui"

// Register the low-profile TUI under the name "lp" so it can be selected
// with `evva -tui lp`. Blank-import this package for the side effect:
//
//	import _ "github.com/johnny1110/evva/pkg/ui/lp"
func init() {
	ui.Register("lp", func(evvaHome string) ui.UI {
		return New(evvaHome)
	})
}
