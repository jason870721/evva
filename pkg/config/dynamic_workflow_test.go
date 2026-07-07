package config

import "testing"

// The solo dynamic-workflow knobs default to opt-in-off with a seeded worker
// cap, and survive a persist + reload round-trip.
func TestDynamicWorkflowConfigDefaultsAndRoundTrip(t *testing.T) {
	home := t.TempDir()
	wd := t.TempDir()

	cfg, err := Load(LoadOptions{AppName: "alpha", AppHome: home, WorkDir: wd})
	if err != nil {
		t.Fatal(err)
	}

	// Defaults: opt-in off, worker cap seeded to 4.
	if cfg.GetEnableDynamicWorkflow() {
		t.Error("EnableDynamicWorkflow should default to false (opt-in)")
	}
	if got := cfg.GetWorkflowMaxWorkers(); got != 4 {
		t.Errorf("WorkflowMaxWorkers default = %d, want 4", got)
	}

	// Persist a toggle + a cap change, then reload from the same home.
	if err := cfg.SetEnableDynamicWorkflow(true); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetWorkflowMaxWorkers(2); err != nil {
		t.Fatal(err)
	}

	reloaded, err := Load(LoadOptions{AppName: "alpha", AppHome: home, WorkDir: wd})
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.GetEnableDynamicWorkflow() {
		t.Error("EnableDynamicWorkflow should persist as true across reload")
	}
	if got := reloaded.GetWorkflowMaxWorkers(); got != 2 {
		t.Errorf("WorkflowMaxWorkers round-trip = %d, want 2", got)
	}
}

// A non-positive worker cap in YAML normalizes to the default rather than
// zero (an uncapped or zero-capped engine would either starve or stampede).
func TestWorkflowMaxWorkersNormalizesNonPositive(t *testing.T) {
	home := t.TempDir()
	wd := t.TempDir()
	cfg, err := Load(LoadOptions{AppName: "alpha", AppHome: home, WorkDir: wd})
	if err != nil {
		t.Fatal(err)
	}
	// Zeros are omitempty, so they vanish from YAML and load re-seeds the
	// default.
	cfg.WorkflowMaxWorkers = 0
	if err := cfg.SaveFile(); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load(LoadOptions{AppName: "alpha", AppHome: home, WorkDir: wd})
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.GetWorkflowMaxWorkers(); got != 4 {
		t.Errorf("zero workflow_max_workers should normalize to 4, got %d", got)
	}
}
