package memdir

// MemoryType is the closed taxonomy a memory file declares in its `type:`
// frontmatter. Ported from ref/src/memdir/memoryTypes.ts: four types that
// capture context NOT derivable from the current project state. Code patterns,
// architecture, and git history are derivable (grep/git/EVVA.md) and must not
// be saved as memories.
type MemoryType string

const (
	TypeUser      MemoryType = "user"
	TypeFeedback  MemoryType = "feedback"
	TypeProject   MemoryType = "project"
	TypeReference MemoryType = "reference"
)

// MemoryTypes is the canonical ordered type list. Consumed by ParseMemoryType
// and by the prompt's frontmatter example.
var MemoryTypes = []MemoryType{TypeUser, TypeFeedback, TypeProject, TypeReference}

// ParseMemoryType resolves a raw frontmatter value to a MemoryType. Returns
// ("", false) for an unknown or empty value — files without a `type:` keep
// working, and files with an unknown type degrade gracefully rather than
// erroring (parseMemoryType parity, memoryTypes.ts:28). Callers treat the
// false case as "untyped".
func ParseMemoryType(raw string) (MemoryType, bool) {
	for _, t := range MemoryTypes {
		if string(t) == raw {
			return t, true
		}
	}
	return "", false
}
