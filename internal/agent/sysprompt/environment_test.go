package sysprompt

import (
	"strings"
	"testing"
	"time"
)

// RP-5: a long-running persona (swarm member) drops the drifting "- Today:" date
// from its environment section so the system-prompt prefix stays bit-stable for
// prompt caching, while an ordinary agent keeps the date.
func TestEnvironmentSection_OmitDate(t *testing.T) {
	base := PromptContext{
		OS:       "darwin",
		Shell:    "zsh",
		WorkDir:  "/tmp/project",
		EvvaHome: "/tmp/.evva",
		Today:    time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC),
	}

	// Ordinary agent (OmitDate false): the date line is present.
	withDate := base
	got := environmentSection(withDate)
	if !strings.Contains(got, "- Today:") {
		t.Errorf("OmitDate=false should keep the date line; got:\n%s", got)
	}
	for _, want := range []string{"# Environment", "- OS / shell: darwin / zsh", "/tmp/project"} {
		if !strings.Contains(got, want) {
			t.Errorf("OmitDate=false missing %q in:\n%s", want, got)
		}
	}

	// Long-running persona (OmitDate true): the date is gone, everything else stays.
	noDate := base
	noDate.OmitDate = true
	got2 := environmentSection(noDate)
	if strings.Contains(got2, "Today") {
		t.Errorf("OmitDate=true must drop the date line; got:\n%s", got2)
	}
	for _, want := range []string{"# Environment", "- OS / shell: darwin / zsh", "/tmp/project"} {
		if !strings.Contains(got2, want) {
			t.Errorf("OmitDate=true dropped non-date info %q:\n%s", want, got2)
		}
	}
}

// RP-5 cache property: with the date omitted the section is fully time-invariant,
// so two renders with an unset Today (which would otherwise default to time.Now())
// are byte-identical — a rebuilt swarm member reuses one cached prompt prefix.
func TestEnvironmentSection_OmitDateIsBitStable(t *testing.T) {
	ctx := PromptContext{OS: "linux", Shell: "bash", WorkDir: "/srv/app", EvvaHome: "/root/.evva", OmitDate: true}
	a := environmentSection(ctx)
	b := environmentSection(ctx)
	if a != b {
		t.Errorf("date-free section not bit-stable across renders:\n%q\nvs\n%q", a, b)
	}
	if strings.Contains(a, "Today") {
		t.Errorf("unexpected date in date-free section:\n%s", a)
	}
}
