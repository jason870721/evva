// Package fs exposes filesystem tools (Read, Write, Edit) as stateless
// singletons. Construction policy (eager vs lazy) is decided by the agent;
// this package only knows how to produce tool instances.
package fs

import (
	config "github.com/johnny1110/evva/configs"
	"github.com/johnny1110/evva/internal/tools"
	"os"
	"path/filepath"
)

// Names lists every tool name this package contributes, in canonical order.
func Names() []tools.ToolName {
	return []tools.ToolName{tools.READ_FILE, tools.WRITE_FILE, tools.EDIT_FILE}
}

// resolvePath resolves a relative path against the workdir and returns the
// absolute path.
func resolvePath(pathStr string) (string, error) {
	cfg := config.Get()
	workdir := cfg.WorkDir

	p := pathStr
	if !filepath.IsAbs(p) {
		p = filepath.Join(workdir, p)
	}
	return filepath.Abs(p)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
