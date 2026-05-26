package version

import (
	"strings"
	"testing"
)

// TestVersion_NonEmpty guards against a stray refactor wiping the
// constant; downstream apps reading version.Version expect a
// non-empty string they can log.
func TestVersion_NonEmpty(t *testing.T) {
	if Version == "" {
		t.Fatal("version.Version is empty — release identity is required")
	}
}

// TestString_NoStamp returns just "v<Version>" when no build stamp is
// set (the default for plain `go build`).
func TestString_NoStamp(t *testing.T) {
	prev := BuildStamp
	BuildStamp = ""
	defer func() { BuildStamp = prev }()

	got := String()
	if got != "v"+Version {
		t.Errorf("String() = %q; want %q", got, "v"+Version)
	}
}

// TestString_WithStamp formats "v<Version>+<stamp>" when ldflags
// populated BuildStamp.
func TestString_WithStamp(t *testing.T) {
	prev := BuildStamp
	BuildStamp = "abc1234"
	defer func() { BuildStamp = prev }()

	got := String()
	if !strings.HasPrefix(got, "v"+Version+"+") {
		t.Errorf("String() = %q; expected leading %q+", got, "v"+Version)
	}
	if !strings.HasSuffix(got, "+abc1234") {
		t.Errorf("String() = %q; expected suffix +abc1234", got)
	}
}

// TestBare_NoStamp returns Version verbatim with no "v" prefix.
func TestBare_NoStamp(t *testing.T) {
	prev := BuildStamp
	BuildStamp = ""
	defer func() { BuildStamp = prev }()

	if got := Bare(); got != Version {
		t.Errorf("Bare() = %q; want %q", got, Version)
	}
}

// TestBare_WithStamp appends "+<stamp>" without the "v" prefix —
// matches SemVer 2.0 build-metadata syntax.
func TestBare_WithStamp(t *testing.T) {
	prev := BuildStamp
	BuildStamp = "abc1234"
	defer func() { BuildStamp = prev }()

	got := Bare()
	want := Version + "+abc1234"
	if got != want {
		t.Errorf("Bare() = %q; want %q", got, want)
	}
	// Bare() returns Version verbatim plus the build metadata; it must not
	// ADD a "v" prefix of its own. The dev placeholder Version is itself
	// "vX.Y.Z-dev", so only assert the absence of an added 'v' when Version
	// is stored bare (the form real release tags use).
	if !strings.HasPrefix(Version, "v") && strings.HasPrefix(got, "v") {
		t.Errorf("Bare() should NOT add a leading 'v'; got %q", got)
	}
}
