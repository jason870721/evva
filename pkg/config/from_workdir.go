package config

import (
	"path/filepath"
)

// setupWorkDirParam sets WorkDir to the supplied workdir and derives
// WorkDirSkillsDir from it. Workdir-local skills live under
// `<wd>/.<app>/skills/<name>/SKILL.md` and override same-named
// AppHome skills when the registry merges the two layers.
func setupWorkDirParam(cfg *Config, workdir string) {
	cfg.WorkDir = workdir
	cfg.WorkDirSkillsDir = filepath.Join(workdir, "."+cfg.AppName, "skills")
}
