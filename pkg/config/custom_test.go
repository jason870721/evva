package config

import (
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func loadIntoTemp(t *testing.T) *Config {
	t.Helper()
	cfg, err := Load(LoadOptions{AppName: "custom-test", AppHome: t.TempDir(), WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

func TestCustomConfig_GetMissing(t *testing.T) {
	cfg := loadIntoTemp(t)
	if _, ok := cfg.GetCustom("missing"); ok {
		t.Error("GetCustom on absent key should return ok=false")
	}
}

func TestCustomConfig_SetGet(t *testing.T) {
	cfg := loadIntoTemp(t)
	if err := cfg.SetCustom("friday.token", "sk-x"); err != nil {
		t.Fatalf("SetCustom: %v", err)
	}
	v, ok := cfg.GetCustom("friday.token")
	if !ok {
		t.Fatal("GetCustom missed the value we just set")
	}
	if s, _ := v.(string); s != "sk-x" {
		t.Errorf("value: got %v want sk-x", v)
	}
}

func TestCustomConfig_EmptyKeyRejected(t *testing.T) {
	cfg := loadIntoTemp(t)
	if err := cfg.SetCustom("", "x"); err == nil {
		t.Error("expected error for empty key")
	}
}

func TestCustomConfig_Delete(t *testing.T) {
	cfg := loadIntoTemp(t)
	_ = cfg.SetCustom("k", 42)
	if err := cfg.DeleteCustom("k"); err != nil {
		t.Fatalf("DeleteCustom: %v", err)
	}
	if _, ok := cfg.GetCustom("k"); ok {
		t.Error("DeleteCustom did not remove the entry")
	}
	// Missing key is a no-op
	if err := cfg.DeleteCustom("nope"); err != nil {
		t.Errorf("DeleteCustom of missing key should be no-op; got %v", err)
	}
}

func TestCustomConfig_RoundTrip(t *testing.T) {
	home := t.TempDir()
	wd := t.TempDir()
	cfg, err := Load(LoadOptions{AppName: "round", AppHome: home, WorkDir: wd})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.SetCustom("friday.token", "sk-x"); err != nil {
		t.Fatalf("SetCustom: %v", err)
	}
	if err := cfg.SetCustom("nested", map[string]any{"a": 1, "b": "two"}); err != nil {
		t.Fatalf("SetCustom nested: %v", err)
	}

	// Reload — SetCustom already persisted via SaveFile.
	cfg2, err := Load(LoadOptions{AppName: "round", AppHome: home, WorkDir: wd})
	if err != nil {
		t.Fatalf("Load second time: %v", err)
	}
	v, ok := cfg2.GetCustom("friday.token")
	if !ok || v != "sk-x" {
		t.Errorf("friday.token after reload: got %v ok=%v", v, ok)
	}
	nested, ok := cfg2.GetCustom("nested")
	if !ok {
		t.Fatal("nested missing after reload")
	}
	// yaml.v3 decodes map[string]any as map[string]any (string keys).
	m, ok := nested.(map[string]any)
	if !ok {
		t.Fatalf("nested type after reload: got %T", nested)
	}
	if !reflect.DeepEqual(m["b"], "two") {
		t.Errorf("nested.b: got %v want two", m["b"])
	}
}

func TestCustomConfig_EmptyMapProducesNoCustomYAMLKey(t *testing.T) {
	home := t.TempDir()
	wd := t.TempDir()
	cfg, err := Load(LoadOptions{AppName: "empty", AppHome: home, WorkDir: wd})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.SaveFile(); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(home, "config", "empty-config.yml"))
	if err != nil {
		t.Fatalf("read yaml: %v", err)
	}
	if got := string(data); containsKey(got, "custom:") {
		t.Errorf("empty CustomConfig should not write `custom:` key; YAML was:\n%s", got)
	}
}

func TestCustomConfig_Clone_DeepCopiesMap(t *testing.T) {
	cfg := loadIntoTemp(t)
	_ = cfg.SetCustom("a", 1)
	cl := cfg.Clone()
	cl.CustomConfig["a"] = 99
	v, _ := cfg.GetCustom("a")
	if v == 99 {
		t.Error("Clone should deep-copy CustomConfig map structure")
	}
}

func TestCustomConfig_ConcurrentSetGet(t *testing.T) {
	cfg := loadIntoTemp(t)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			_ = cfg.SetCustom("k", i)
		}(i)
		go func() {
			defer wg.Done()
			_, _ = cfg.GetCustom("k")
		}()
	}
	wg.Wait()
}

func containsKey(yaml, key string) bool {
	// Looks for `<key>` at line start (allowing leading whitespace would be
	// wrong for a top-level YAML key). Simple substring check is enough
	// since the key is unique.
	for _, line := range splitLines(yaml) {
		if line == key || (len(line) > len(key) && line[:len(key)] == key) {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
