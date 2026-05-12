package tools

import "fmt"

// Group is a unit of registration: a list of tool names whose instances are
// produced together by a single Build call. Stateless tools form one-name
// groups; tools that share backing state (e.g. the six task tools sharing one
// *Store) form a multi-name group whose Build allocates that state once.
//
// Build MUST return exactly len(Names) tools, in the same order as Names.
type Group struct {
	Names []ToolName
	Build func() []Tool
}

var (
	groups   []Group
	memberOf = map[ToolName]groupRef{}
)

type groupRef struct {
	group  int
	member int
}

// RegisterGroup adds a group to the global registry. Panics on duplicate
// names — registration is wiring code, not runtime config; a clash is always
// a bug. Conventionally called from each tool package's init().
func RegisterGroup(g Group) {
	idx := len(groups)
	groups = append(groups, g)
	for i, n := range g.Names {
		if _, dup := memberOf[n]; dup {
			panic(fmt.Sprintf("tools: duplicate registration for %q", n))
		}
		memberOf[n] = groupRef{group: idx, member: i}
	}
}

// Register is sugar for a single stateless tool. The group's Build returns
// the same instance every call, so no state is allocated.
func Register(name ToolName, t Tool) {
	RegisterGroup(Group{
		Names: []ToolName{name},
		Build: func() []Tool { return []Tool{t} },
	})
}

// Build resolves a list of tool names to fresh instances. Tools that belong
// to the same Group share the backing state allocated for this call; a
// separate call yields independent state, so each agent gets isolated tools.
//
// Returns an error if any name has no registered group.
func Build(names []ToolName) ([]Tool, error) {
	cache := map[int][]Tool{}
	out := make([]Tool, 0, len(names))
	for _, n := range names {
		ref, ok := memberOf[n]
		if !ok {
			return nil, fmt.Errorf("tools: no factory for %q", n)
		}
		instances, ok := cache[ref.group]
		if !ok {
			instances = groups[ref.group].Build()
			if got, want := len(instances), len(groups[ref.group].Names); got != want {
				return nil, fmt.Errorf("tools: group for %q returned %d tools, want %d", n, got, want)
			}
			cache[ref.group] = instances
		}
		out = append(out, instances[ref.member])
	}
	return out, nil
}
