package configtool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnny1110/evva/pkg/config"
)

// The output_style setting validates against the resolved catalog before
// persisting: built-ins and disk styles are accepted, typos are rejected
// with the available set listed, and "default" clears the overlay.
func TestOutputStyleSettingValidates(t *testing.T) {
	home := t.TempDir()
	styleDir := filepath.Join(home, "output-styles")
	if err := os.MkdirAll(styleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(styleDir, "pirate.md"),
		[]byte("---\nname: pirate\n---\nYarr."), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		AppHome:           home,
		WorkDir:           t.TempDir(),
		AppHomeConfigFile: filepath.Join(home, "evva-config.yml"),
	}

	sc, ok := Get("output_style")
	if !ok {
		t.Fatal("output_style setting missing from registry")
	}

	for _, valid := range []string{"Explanatory", "pirate", "default"} {
		if err := sc.Set(cfg, valid); err != nil {
			t.Errorf("Set(%q) rejected a valid style: %v", valid, err)
		}
	}
	if got := sc.Get(cfg); got != "default" {
		t.Errorf("after Set(default), Get = %v, want default", got)
	}

	err := sc.Set(cfg, "ghost")
	if err == nil {
		t.Fatal("Set(ghost) must reject an unknown style")
	}
	for _, want := range []string{"ghost", "Explanatory", "pirate", "default"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("rejection should mention %q, got: %v", want, err)
		}
	}
}
