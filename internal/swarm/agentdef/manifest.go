package agentdef

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Manifest is a parsed evva-swarm.yml: the swarm name, its workdir, the leader,
// the workers, and space-wide settings. No replicas — every member name must be
// unique within the space.
type Manifest struct {
	Name     string
	Workdir  string
	Leader   Member
	Workers  []Member
	Settings Settings
}

// Member names an agent definition under agents/{main,sub}/{agent}/, with an
// optional timer schedule. A manifest schedule is authoritative over the agent's
// own profile.yml (RP-7 §3.7): the whole team's cadence lives in one
// version-controlled file rather than scattered across each profile.
type Member struct {
	Agent    string
	Schedule *Schedule // nil when the manifest declares none for this member
}

// Settings are space-wide knobs from the manifest.
type Settings struct {
	PermissionMode string
	MaxIterations  int
}

// scheduleYml is the on-disk schedule block shared by the manifest's leader and
// workers (and mirrored by profile.yml). Exactly one of cron/every is set.
type scheduleYml struct {
	Cron   string `yaml:"cron,omitempty"`
	Every  string `yaml:"every,omitempty"`
	Prompt string `yaml:"prompt,omitempty"`
}

// memberYml is one leader/worker entry in evva-swarm.yml.
type memberYml struct {
	Agent    string       `yaml:"agent"`
	Schedule *scheduleYml `yaml:"schedule,omitempty"`
}

// manifestYml is the on-disk schema for evva-swarm.yml (design §4.4).
type manifestYml struct {
	Name     string      `yaml:"name,omitempty"`
	Workdir  string      `yaml:"workdir,omitempty"`
	Leader   memberYml   `yaml:"leader"`
	Workers  []memberYml `yaml:"workers,omitempty"`
	Settings struct {
		PermissionMode string `yaml:"permission_mode,omitempty"`
		MaxIterations  int    `yaml:"max_iterations,omitempty"`
	} `yaml:"settings,omitempty"`
}

// parseScheduleYml turns an optional on-disk schedule block into a *Schedule,
// validating the cron at load time (a bad spec fails the whole manifest, not the
// first tick). nil block → nil schedule.
func parseScheduleYml(y *scheduleYml) (*Schedule, error) {
	if y == nil {
		return nil, nil
	}
	s, err := parseSchedule(y.Cron, y.Every)
	if err != nil {
		return nil, err
	}
	s.Prompt = y.Prompt
	return &s, nil
}

// LoadManifest reads and validates an evva-swarm.yml.
func LoadManifest(path string) (Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("agentdef: read manifest: %w", err)
	}
	var y manifestYml
	if err := yaml.Unmarshal(b, &y); err != nil {
		return Manifest{}, fmt.Errorf("agentdef: parse manifest %s: %w", path, err)
	}

	leaderSched, err := parseScheduleYml(y.Leader.Schedule)
	if err != nil {
		return Manifest{}, fmt.Errorf("agentdef: manifest leader %q schedule: %w", y.Leader.Agent, err)
	}
	m := Manifest{
		Name:     y.Name,
		Workdir:  y.Workdir,
		Leader:   Member{Agent: y.Leader.Agent, Schedule: leaderSched},
		Settings: Settings{PermissionMode: y.Settings.PermissionMode, MaxIterations: y.Settings.MaxIterations},
	}
	for _, w := range y.Workers {
		ws, err := parseScheduleYml(w.Schedule)
		if err != nil {
			return Manifest{}, fmt.Errorf("agentdef: manifest worker %q schedule: %w", w.Agent, err)
		}
		m.Workers = append(m.Workers, Member{Agent: w.Agent, Schedule: ws})
	}
	if err := m.validate(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// toScheduleYml converts a parsed Schedule back to its on-disk shape (WriteManifest).
func toScheduleYml(s *Schedule) *scheduleYml {
	if s == nil {
		return nil
	}
	sy := &scheduleYml{Cron: s.Cron, Prompt: s.Prompt}
	if s.Every > 0 {
		sy.Every = s.Every.String()
	}
	return sy
}

// WriteManifest re-serialises a Manifest back to evva-swarm.yml. RP-8's web
// add/remove keeps the manifest authoritative (the space rebuild reads this file,
// so dynamic membership survives a restart); the trade-off the operator accepted
// is that re-emitting drops any hand-written comments/formatting. Schedules are
// emitted too, though runtime.json stays the live authority (RP-7).
func WriteManifest(path string, m Manifest) error {
	y := manifestYml{Name: m.Name, Workdir: m.Workdir}
	y.Leader = memberYml{Agent: m.Leader.Agent, Schedule: toScheduleYml(m.Leader.Schedule)}
	for _, w := range m.Workers {
		y.Workers = append(y.Workers, memberYml{Agent: w.Agent, Schedule: toScheduleYml(w.Schedule)})
	}
	y.Settings.PermissionMode = m.Settings.PermissionMode
	y.Settings.MaxIterations = m.Settings.MaxIterations
	b, err := yaml.Marshal(y)
	if err != nil {
		return fmt.Errorf("agentdef: marshal manifest: %w", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("agentdef: write manifest %s: %w", path, err)
	}
	return nil
}

// AddWorker appends a worker so the manifest stays authoritative for membership
// (RP-8). The schedule is omitted here — the member's profile.yml + runtime.json
// carry it. Errors on a duplicate or leader-name collision (invariants #2/#7).
func (m *Manifest) AddWorker(name string) error {
	if name == m.Leader.Agent {
		return fmt.Errorf("agentdef: %q is the leader, not a worker", name)
	}
	for _, w := range m.Workers {
		if w.Agent == name {
			return fmt.Errorf("agentdef: worker %q already in manifest", name)
		}
	}
	m.Workers = append(m.Workers, Member{Agent: name})
	return nil
}

// RemoveWorker drops a worker from the manifest. A missing name is a no-op (the
// live remove already happened; the manifest just catches up).
func (m *Manifest) RemoveWorker(name string) {
	out := m.Workers[:0]
	for _, w := range m.Workers {
		if w.Agent != name {
			out = append(out, w)
		}
	}
	m.Workers = out
}

// validate enforces a leader and unique non-empty member names (leader +
// workers) — no replicas (design decision ⑦). The space name is OPTIONAL
// (Docker-style): when the manifest omits it, the service assigns one (an
// explicit `--name`, else a generated handle). So name is NOT validated here.
func (m Manifest) validate() error {
	if strings.TrimSpace(m.Leader.Agent) == "" {
		return fmt.Errorf("agentdef: manifest: leader.agent is required")
	}
	seen := map[string]bool{m.Leader.Agent: true}
	for i, w := range m.Workers {
		if strings.TrimSpace(w.Agent) == "" {
			return fmt.Errorf("agentdef: manifest: workers[%d].agent is empty", i)
		}
		if seen[w.Agent] {
			return fmt.Errorf("agentdef: manifest: duplicate agent name %q (no replicas — give each member a distinct name)", w.Agent)
		}
		seen[w.Agent] = true
	}
	return nil
}
