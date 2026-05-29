package mcp

import "testing"

func TestBuildToolName(t *testing.T) {
	if got := BuildToolName("filesystem", "read_file"); got != "mcp__filesystem__read_file" {
		t.Errorf("BuildToolName = %q", got)
	}
	// Both segments normalized.
	if got := BuildToolName("git hub", "list repos"); got != "mcp__git_hub__list_repos" {
		t.Errorf("BuildToolName normalize = %q", got)
	}
}

func TestParseToolName(t *testing.T) {
	info := ParseToolName("mcp__filesystem__read_file")
	if info == nil || info.Server != "filesystem" || info.Tool != "read_file" {
		t.Fatalf("ParseToolName = %+v", info)
	}

	// Tool segment containing __ rejoins.
	info = ParseToolName("mcp__srv__a__b")
	if info == nil || info.Server != "srv" || info.Tool != "a__b" {
		t.Fatalf("ParseToolName multi = %+v", info)
	}

	// Non-mcp / malformed inputs return nil.
	for _, bad := range []string{"", "read_file", "mcp__", "mcp__srv", "notmcp__a__b"} {
		if got := ParseToolName(bad); got != nil {
			t.Errorf("ParseToolName(%q) = %+v, want nil", bad, got)
		}
	}
}

func TestToolNameRoundTrip(t *testing.T) {
	name := BuildToolName("filesystem", "read_file")
	info := ParseToolName(name)
	if info == nil || info.Server != "filesystem" || info.Tool != "read_file" {
		t.Fatalf("round trip failed: %q -> %+v", name, info)
	}
}
