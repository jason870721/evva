package configtool

import (
	"reflect"
	"testing"
)

// TestSupportedSettingsKeys pins the exact key set so an accidental
// addition or removal is visible in review. The registry is the contract
// between the tool and every downstream consumer (prompt body, permission
// posture, the model's view of what it can tune) — drift here should never
// be silent. Update this list deliberately when adding a setting, and add
// the matching field to the /config overlay's buildConfigFields too.
func TestSupportedSettingsKeys(t *testing.T) {
	// Sorted lexicographically (sort.Strings): note "deepseek" sorts before
	// "default" ('e' < 'f' at the third byte).
	want := []string{
		"anthropic.api_key", "anthropic.api_url",
		"auto_compact_threshold",
		"deepseek.api_key", "deepseek.api_url",
		"default_effort", "default_profile",
		"display_thinking",
		"enable_auto_memory",
		"fetch_max_bytes",
		"max_iterations", "max_tokens",
		"ollama.api_url",
		"openai.api_key", "openai.api_url",
		"tavily_api_key",
	}
	if got := AllKeys(); !reflect.DeepEqual(got, want) {
		t.Errorf("AllKeys mismatch:\n got: %v\nwant: %v", got, want)
	}
}

// TestRegistryEntriesComplete asserts every entry is callable — a nil Get
// or Set would panic at Execute time.
func TestRegistryEntriesComplete(t *testing.T) {
	for key, sc := range SUPPORTED_SETTINGS {
		if sc.Get == nil {
			t.Errorf("%q: Get is nil", key)
		}
		if sc.Set == nil {
			t.Errorf("%q: Set is nil", key)
		}
		if sc.Description == "" {
			t.Errorf("%q: Description is empty", key)
		}
	}
}

// TestOllamaHasNoAPIKey guards the one provider asymmetry: Ollama is local
// and unauthenticated, so it gets an api_url entry but no api_key.
func TestOllamaHasNoAPIKey(t *testing.T) {
	if IsSupported("ollama.api_key") {
		t.Error("ollama.api_key should not be a supported setting (local, unauthenticated)")
	}
	if !IsSupported("ollama.api_url") {
		t.Error("ollama.api_url should be supported")
	}
}

func TestCoerceBool(t *testing.T) {
	cases := []struct {
		in      any
		want    bool
		wantErr bool
	}{
		{true, true, false},
		{false, false, false},
		{"true", true, false},
		{"TRUE", true, false},
		{" false ", false, false},
		{"yes", false, true},
		{"1", false, true},
		{1, false, true},
		{float64(1), false, true},
	}
	for _, c := range cases {
		got, err := coerceBool(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("coerceBool(%#v): err=%v wantErr=%v", c.in, err, c.wantErr)
			continue
		}
		if err == nil && got != c.want {
			t.Errorf("coerceBool(%#v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestCoerceInt(t *testing.T) {
	cases := []struct {
		in      any
		want    int
		wantErr bool
	}{
		{42, 42, false},
		{float64(42), 42, false},
		{"42", 42, false},
		{" 42 ", 42, false},
		{float64(42.5), 0, true}, // non-integer float must be rejected, not truncated
		{"abc", 0, true},
		{true, 0, true},
	}
	for _, c := range cases {
		got, err := coerceInt(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("coerceInt(%#v): err=%v wantErr=%v", c.in, err, c.wantErr)
			continue
		}
		if err == nil && got != c.want {
			t.Errorf("coerceInt(%#v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestCoerceFloat(t *testing.T) {
	cases := []struct {
		in      any
		want    float64
		wantErr bool
	}{
		{0.5, 0.5, false},
		{float64(1), 1, false},
		{1, 1, false},
		{"0.5", 0.5, false},
		{"abc", 0, true},
		{true, 0, true},
	}
	for _, c := range cases {
		got, err := coerceFloat(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("coerceFloat(%#v): err=%v wantErr=%v", c.in, err, c.wantErr)
			continue
		}
		if err == nil && got != c.want {
			t.Errorf("coerceFloat(%#v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestMaskSecret(t *testing.T) {
	cases := []struct {
		in   any
		want any
	}{
		{"", "(empty)"},
		{"abc", "****"},
		{"abcd", "****"},
		{"abcde", "****bcde"},
		{"abcdef", "****cdef"},
		{nil, "(empty)"},
	}
	for _, c := range cases {
		if got := maskSecret(c.in); got != c.want {
			t.Errorf("maskSecret(%#v) = %v, want %v", c.in, got, c.want)
		}
	}
}
