package bubbletea

import (
	"testing"
)

func TestMatchSlashCommands(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []string // expected command names in order
	}{
		{"empty", "", nil},
		{"plain text", "hello", nil},
		{"path-like input (no /)", "config", nil},
		{"single slash shows all defaults", "/", []string{"/compact", "/config", "/model", "/clear", "/exit"}},
		{"prefix narrows", "/c", []string{"/compact", "/config", "/clear"}},
		{"unique prefix /co", "/co", []string{"/compact", "/config"}},
		{"unique prefix /cl", "/cl", []string{"/clear"}},
		{"exact match collapses", "/config", []string{"/config"}},
		{"case-insensitive", "/CL", []string{"/clear"}},
		{"no match", "/zzz", []string{}},
		{"leading whitespace trimmed", "  /m  ", []string{"/model"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := matchSlashCommands(c.input, defaultSlashCommands)
			if len(got) != len(c.want) {
				t.Fatalf("len: want %d (%v), got %d (%v)", len(c.want), c.want, len(got), got)
			}
			for i := range got {
				if got[i].name != c.want[i] {
					t.Errorf("index %d: want %s, got %s", i, c.want[i], got[i].name)
				}
			}
		})
	}
}

// TestMatchSlashCommands_HardCap verifies the panel never renders more
// than slashMaxSuggestions rows even when many entries would match. We
// inject a synthetic catalog so the test isn't sensitive to future
// changes in the default list size.
func TestMatchSlashCommands_HardCap(t *testing.T) {
	all := []slashCommand{
		{"/alpha", ""}, {"/beta", ""}, {"/gamma", ""},
		{"/delta", ""}, {"/epsilon", ""}, {"/zeta", ""}, {"/eta", ""},
	}
	got := matchSlashCommands("/", all)
	if len(got) != slashMaxSuggestions {
		t.Fatalf("hard cap not enforced: want %d, got %d (%v)", slashMaxSuggestions, len(got), got)
	}
}

// TestMatchSlashCommands_SkillRowsMerged verifies skill entries from a
// controller (passed in as extra slashCommand rows) are matched the same
// way as built-ins and obey the hard cap.
func TestMatchSlashCommands_SkillRowsMerged(t *testing.T) {
	all := append([]slashCommand{}, defaultSlashCommands...)
	all = append(all,
		slashCommand{name: "/git-commit", desc: "how to commit (rules)"},
		slashCommand{name: "/review", desc: "code review checklist"},
	)

	got := matchSlashCommands("/g", all)
	if len(got) != 1 || got[0].name != "/git-commit" {
		t.Errorf("skill prefix match: got %v", got)
	}

	got = matchSlashCommands("/", all)
	if len(got) != slashMaxSuggestions {
		t.Errorf("merged catalog should still cap at %d: got %d", slashMaxSuggestions, len(got))
	}
}
