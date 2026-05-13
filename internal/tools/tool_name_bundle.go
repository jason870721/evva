package tools

import (
	"github.com/johnny1110/evva/internal/tools/active"
	"github.com/johnny1110/evva/internal/tools/deferred"
	"slices"
)

// for agent tools bundle

func All() []ToolName {
	return slices.Concat(active.Names(), deferred.Names())
}

func ReadOnly() []ToolName {
	return []ToolName{READ_FILE, WEB_SEARCH, GREP, LS}
}
