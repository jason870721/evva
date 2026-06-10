package agentdef

import (
	"fmt"
	"os"
	"strings"
	"time"

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
	// BudgetTokens overrides the space-wide daily token budget for this member
	// (RP-13): >0 = own daily cap, <0 = unlimited (exempt even when the space
	// sets a default), 0 = inherit Settings.DailyBudgetTokens.
	BudgetTokens int
}

// Settings are space-wide knobs from the manifest.
type Settings struct {
	PermissionMode string
	MaxIterations  int
	// DailyBudgetTokens is the per-member daily token cap (input+output, local
	// day) — the RP-13 budget breaker. 0 = unlimited. A member that crosses it
	// is frozen until the day rolls over (or the operator unfreezes it).
	DailyBudgetTokens int
	// BudgetStayFrozen keeps a budget-frozen member frozen across the day
	// rollover, requiring a manual unfreeze (default false = auto-unfreeze).
	BudgetStayFrozen bool
	// StallThreshold is the RP-14 watchdog alert line: a member busy longer
	// than this (and not waiting on a human) raises a one-per-run stall notice
	// to the operator and the leader. 0 = disabled; a manifest that omits the
	// knob gets DefaultStallThreshold.
	StallThreshold time.Duration
	// StallHardTimeout, when set, auto-cancels a run busy longer than this —
	// the non-clean exit unclaims the run's mail so it retries on the next
	// wake. 0 = disabled (the default: alert-only until thresholds are tuned).
	StallHardTimeout time.Duration
}

// DefaultStallThreshold is the alert line a manifest gets when it does not set
// settings.stall_threshold. Long enough that legitimate tool-heavy runs don't
// page the operator; short enough that a hung run is noticed the same hour.
const DefaultStallThreshold = 10 * time.Minute

// scheduleYml is the on-disk schedule block shared by the manifest's leader and
// workers (and mirrored by profile.yml). Exactly one of cron/every is set.
type scheduleYml struct {
	Cron   string `yaml:"cron,omitempty"`
	Every  string `yaml:"every,omitempty"`
	Prompt string `yaml:"prompt,omitempty"`
}

// memberYml is one leader/worker entry in evva-swarm.yml.
type memberYml struct {
	Agent        string       `yaml:"agent"`
	Schedule     *scheduleYml `yaml:"schedule,omitempty"`
	BudgetTokens int          `yaml:"budget_tokens,omitempty"`
}

// manifestYml is the on-disk schema for evva-swarm.yml (design §4.4).
type manifestYml struct {
	Name     string      `yaml:"name,omitempty"`
	Workdir  string      `yaml:"workdir,omitempty"`
	Leader   memberYml   `yaml:"leader"`
	Workers  []memberYml `yaml:"workers,omitempty"`
	Settings struct {
		PermissionMode    string `yaml:"permission_mode,omitempty"`
		MaxIterations     int    `yaml:"max_iterations,omitempty"`
		DailyBudgetTokens int    `yaml:"daily_budget_tokens,omitempty"`
		BudgetStayFrozen  bool   `yaml:"budget_stay_frozen,omitempty"`
		StallThreshold    string `yaml:"stall_threshold,omitempty"`    // duration; "" = default, "0" = off
		StallHardTimeout  string `yaml:"stall_hard_timeout,omitempty"` // duration; "" or "0" = off
	} `yaml:"settings,omitempty"`
}

// parseStallDuration reads an optional duration knob: "" → def, "0" → disabled,
// otherwise a positive time.ParseDuration value.
func parseStallDuration(s string, def time.Duration) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return def, nil
	}
	if s == "0" {
		return 0, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, err
	}
	if d < 0 {
		return 0, fmt.Errorf("must not be negative: %q", s)
	}
	return d, nil
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
	stall, err := parseStallDuration(y.Settings.StallThreshold, DefaultStallThreshold)
	if err != nil {
		return Manifest{}, fmt.Errorf("agentdef: manifest settings.stall_threshold: %w", err)
	}
	hard, err := parseStallDuration(y.Settings.StallHardTimeout, 0)
	if err != nil {
		return Manifest{}, fmt.Errorf("agentdef: manifest settings.stall_hard_timeout: %w", err)
	}
	m := Manifest{
		Name:    y.Name,
		Workdir: y.Workdir,
		Leader:  Member{Agent: y.Leader.Agent, Schedule: leaderSched, BudgetTokens: y.Leader.BudgetTokens},
		Settings: Settings{
			PermissionMode:    y.Settings.PermissionMode,
			MaxIterations:     y.Settings.MaxIterations,
			DailyBudgetTokens: y.Settings.DailyBudgetTokens,
			BudgetStayFrozen:  y.Settings.BudgetStayFrozen,
			StallThreshold:    stall,
			StallHardTimeout:  hard,
		},
	}
	for _, w := range y.Workers {
		ws, err := parseScheduleYml(w.Schedule)
		if err != nil {
			return Manifest{}, fmt.Errorf("agentdef: manifest worker %q schedule: %w", w.Agent, err)
		}
		m.Workers = append(m.Workers, Member{Agent: w.Agent, Schedule: ws, BudgetTokens: w.BudgetTokens})
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
	y.Leader = memberYml{Agent: m.Leader.Agent, Schedule: toScheduleYml(m.Leader.Schedule), BudgetTokens: m.Leader.BudgetTokens}
	for _, w := range m.Workers {
		y.Workers = append(y.Workers, memberYml{Agent: w.Agent, Schedule: toScheduleYml(w.Schedule), BudgetTokens: w.BudgetTokens})
	}
	y.Settings.PermissionMode = m.Settings.PermissionMode
	y.Settings.MaxIterations = m.Settings.MaxIterations
	y.Settings.DailyBudgetTokens = m.Settings.DailyBudgetTokens
	y.Settings.BudgetStayFrozen = m.Settings.BudgetStayFrozen
	// Stall knobs round-trip losslessly: the default emits nothing (reloads as
	// the default), an explicit off emits "0", anything else its duration.
	switch m.Settings.StallThreshold {
	case DefaultStallThreshold: // omit
	case 0:
		y.Settings.StallThreshold = "0"
	default:
		y.Settings.StallThreshold = m.Settings.StallThreshold.String()
	}
	if m.Settings.StallHardTimeout > 0 {
		y.Settings.StallHardTimeout = m.Settings.StallHardTimeout.String()
	}
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
