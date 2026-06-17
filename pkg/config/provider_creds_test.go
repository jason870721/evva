package config

import (
	"testing"

	"github.com/johnny1110/evva/pkg/constant"
)

// Provider credentials set via the config setters must survive a SaveFile +
// reload — for EVERY provider, including newly-added ones like qwen. Regression
// for the hardcoded setupLLMProviderConfig list that silently dropped providers
// not on it, which read back as "my api_key/api_url won't persist".
func TestProviderCredsRoundTrip(t *testing.T) {
	home := t.TempDir()
	wd := t.TempDir()

	cfg, err := Load(LoadOptions{AppName: "alpha", AppHome: home, WorkDir: wd})
	if err != nil {
		t.Fatal(err)
	}

	key := func(name string) string { return "sk-" + name + "-key" }
	url := func(name string) string { return "https://" + name + ".example/custom/v1" }

	// Set a custom url + key for every cloud provider (ollama is key-less).
	for _, p := range constant.GetAllProviders() {
		if p.Name == constant.OLLAMA.Name {
			continue
		}
		if err := cfg.SetProviderAPIURL(p.Name, url(p.Name)); err != nil {
			t.Fatalf("SetProviderAPIURL(%s): %v", p.Name, err)
		}
		if err := cfg.SetProviderAPIKey(p.Name, key(p.Name)); err != nil {
			t.Fatalf("SetProviderAPIKey(%s): %v", p.Name, err)
		}
	}

	// Reload from the same home — the values must come back from the YAML.
	reloaded, err := Load(LoadOptions{AppName: "alpha", AppHome: home, WorkDir: wd})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range constant.GetAllProviders() {
		if p.Name == constant.OLLAMA.Name {
			continue
		}
		if got := reloaded.GetProviderAPIKey(p.Name); got != key(p.Name) {
			t.Errorf("%s api_key after reload = %q, want %q", p.Name, got, key(p.Name))
		}
		if got := reloaded.GetProviderAPIURL(p.Name); got != url(p.Name) {
			t.Errorf("%s api_url after reload = %q, want %q", p.Name, got, url(p.Name))
		}
	}
}
