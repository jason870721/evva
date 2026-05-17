package config

import (
	"os"
	"path/filepath"
)

// setupWorkDirParam sets WorkDir to the process's current working directory
// and derives WorkDirSkillsDir from it. Workdir-local skills live under
// `<wd>/.evva/skills/<name>/SKILL.md` and override same-named home skills
// when the registry merges the two layers.
func setupWorkDirParam(cfg *AppConfig) {
	wd, err := os.Getwd()
	if err != nil {
		wd = "."
	}
	cfg.WorkDir = wd
	cfg.WorkDirSkillsDir = filepath.Join(wd, ".evva", "skills")
}
