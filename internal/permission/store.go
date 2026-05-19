package permission

import "sync"

// Store holds the active set of allow/deny/ask rules across all three
// Sources. Project + User rules are populated by Load(); Session rules are
// added at runtime by the broker when the user picks "Allow for this
// session."
//
// All Store operations are safe for concurrent use — the gate runs in
// dispatchToolCalls' parallel goroutines.
type Store struct {
	mu    sync.RWMutex
	rules []Rule
}

// NewStore returns an empty Store. The loader populates it with project
// and user rules at startup; callers add session rules at runtime.
func NewStore() *Store {
	return &Store{}
}

// ReplaceAll atomically swaps the entire rule list. Used by the loader so a
// settings-file reload doesn't expose a half-written intermediate state.
func (s *Store) ReplaceAll(rs []Rule) {
	s.mu.Lock()
	s.rules = append([]Rule(nil), rs...)
	s.mu.Unlock()
}

// AddSessionRule appends a session-scope rule. Idempotent: a duplicate is
// silently dropped so the rule list doesn't grow unbounded if the user
// repeatedly approves the same call.
func (s *Store) AddSessionRule(r Rule) {
	r.Source = SourceSession
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.rules {
		if existing.Source == SourceSession &&
			existing.ToolName == r.ToolName &&
			existing.Content == r.Content &&
			existing.Behavior == r.Behavior {
			return
		}
	}
	s.rules = append(s.rules, r)
}

// Snapshot returns a defensive copy of the current rule list for inspection.
// Callers should treat the result as immutable.
func (s *Store) Snapshot() []Rule {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Rule, len(s.rules))
	copy(out, s.rules)
	return out
}

// firstMatch returns the first rule that matches call AND has the given
// behavior, scanning in source-priority order: session, project, user.
// Session wins because it's the most recent / most specific to the user's
// current intent. ok=false means no match.
func (s *Store) firstMatch(call ToolCall, b Behavior) (Rule, bool) {
	priority := []Source{SourceSession, SourceProject, SourceUser}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, src := range priority {
		for _, r := range s.rules {
			if r.Source != src {
				continue
			}
			if r.Behavior != b {
				continue
			}
			if matchToolCall(r, call) {
				return r, true
			}
		}
	}
	return Rule{}, false
}

// projectRules returns the subset of rules backed by the project-scope
// file. Used by the loader's Save() to know what to serialize.
func (s *Store) projectRules() []Rule {
	return s.bySource(SourceProject)
}

// userRules returns the user-scope rules.
func (s *Store) userRules() []Rule {
	return s.bySource(SourceUser)
}

func (s *Store) bySource(src Source) []Rule {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Rule
	for _, r := range s.rules {
		if r.Source == src {
			out = append(out, r)
		}
	}
	return out
}
