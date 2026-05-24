package hooks

import "github.com/bmatcuk/doublestar/v4"

// matchTool tests whether toolName matches the matcher glob. An empty
// matcher is "match anything" so hooks declared without a matcher fire
// for every tool.
//
// The matcher uses doublestar syntax — same matcher dependency the
// permission package already pulls in. Plain tool-name strings like
// "Write" are valid (a literal match); "Write|Edit" is NOT supported in
// v1 (use two separate matcher entries).
func matchTool(matcher, toolName string) bool {
	if matcher == "" {
		return true
	}
	ok, err := doublestar.Match(matcher, toolName)
	if err != nil {
		return false
	}
	return ok
}
