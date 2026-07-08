package config

import "testing"

// The output-style knob defaults to "default" (no overlay), normalizes the
// "default" spelling to an empty stored value (so a pristine config file
// never grows the key), and survives a persist + reload round-trip.
func TestOutputStyleDefaultsNormalizationAndRoundTrip(t *testing.T) {
	home := t.TempDir()
	wd := t.TempDir()

	cfg, err := Load(LoadOptions{AppName: "alpha", AppHome: home, WorkDir: wd})
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.GetOutputStyle(); got != "default" {
		t.Errorf("GetOutputStyle default = %q, want %q", got, "default")
	}

	if err := cfg.SetOutputStyle("Explanatory"); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load(LoadOptions{AppName: "alpha", AppHome: home, WorkDir: wd})
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.GetOutputStyle(); got != "Explanatory" {
		t.Errorf("output_style round-trip = %q, want Explanatory", got)
	}

	// Setting "default" (any case, padded) clears the stored value.
	if err := reloaded.SetOutputStyle("  Default "); err != nil {
		t.Fatal(err)
	}
	if reloaded.OutputStyle != "" {
		t.Errorf("SetOutputStyle(default) should store empty, got %q", reloaded.OutputStyle)
	}
	if got := reloaded.GetOutputStyle(); got != "default" {
		t.Errorf("GetOutputStyle after reset = %q, want default", got)
	}
}
