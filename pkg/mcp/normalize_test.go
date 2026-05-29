package mcp

import "testing"

func TestNormalizeName(t *testing.T) {
	cases := map[string]string{
		"filesystem":    "filesystem",
		"read_file":     "read_file",
		"read-file":     "read-file",
		"github.com":    "github_com",
		"my server":     "my_server",
		"@scope/pkg":    "_scope_pkg",
		"emoji😀here":    "emoji_here",
		"Mixed.Case-99": "Mixed_Case-99",
	}
	for in, want := range cases {
		if got := NormalizeName(in); got != want {
			t.Errorf("NormalizeName(%q) = %q, want %q", in, got, want)
		}
	}
}
