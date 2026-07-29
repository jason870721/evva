package agentdef

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/permission"
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
	// PermissionMode overrides the space-wide permission stance for this
	// member (RP-24): default | accept_edits | plan | bypass; "" = inherit
	// Settings.PermissionMode. The coarse trust knob layered over the
	// member's fine-grained permissions.json rules (RP-11): the mode decides
	// the broad stance, allow rules open holes in `default`, and deny rules
	// bind in EVERY mode — bypass included.
	PermissionMode string
	// Worktree overrides the space-wide worktree-isolation stance for this
	// member (SWT): WorktreeOn | WorktreeOff; "" = inherit
	// Settings.WorktreeIsolation. Resolve it with ResolveWorktree — the
	// member field beats the space setting, the RP-24 permission_mode
	// layering. An explicit "on" is rejected for the leader (D8): the leader
	// merges members' branches onto the base checkout and must stay on it.
	Worktree string
	// FromPersona marks a member that references a registry main-tier persona
	// instead of a workdir agents/{main,sub}/<name>/ directory (RP-29). The
	// member NAME stays in Agent for both shapes; the space resolves and
	// composes the persona def at assembly time.
	FromPersona bool
	// Model / Effort / WhenToUse are optional manifest-level overrides. For a
	// persona member they are the only pin point (it has no profile.yml); for
	// a dir member a non-empty value is authoritative over profile.yml — the
	// RP-7 §3.7 schedule precedent: the whole team's config reads in one file.
	Model     string
	Effort    string
	WhenToUse string
}

// Settings are space-wide knobs from the manifest.
type Settings struct {
	PermissionMode string
	MaxIterations  int
	// DailyBudgetTokens is the per-member daily token cap (input+output, local
	// day) — the RP-13 budget breaker. 0 = unlimited. A member that crosses it
	// is frozen until the day rolls over (or the operator unfreezes it).
	// Negative values normalize to 0 at manifest load (RP-24 §5): unlike the
	// member-level knob there is nothing at space level to be exempt FROM, so
	// any non-positive value just means "no cap".
	DailyBudgetTokens int
	// DailyBudgetTotalTokens / DailyBudgetTotalUSD are the SPACE-wide daily
	// ceiling (CST): crossing either freezes every member — the leader
	// included — until the local day rolls over (budget_stay_frozen pins).
	// Tokens compare In+Out (generation volume); USD compares priced spend
	// only (unpriced custom models are flagged, never counted as $0). 0 =
	// that axis off; negatives normalize to 0 at load.
	DailyBudgetTotalTokens int
	DailyBudgetTotalUSD    float64
	// BudgetStayFrozen keeps a budget-frozen member frozen across the day
	// rollover, requiring a manual unfreeze (default false = auto-unfreeze).
	BudgetStayFrozen bool
	// MaxMembers caps the LIVE roster size the leader's member_spawn may grow
	// to (DWF). 0 = DefaultMaxMembers. It gates only agent-driven spawning —
	// operator surfaces (add member, web form) are deliberately uncapped: the
	// guardrail is on the model, not the human. Negatives normalize to 0.
	MaxMembers int
	// StallThreshold is the RP-14 watchdog alert line: a member busy longer
	// than this (and not waiting on a human) raises a one-per-run stall notice
	// to the operator and the leader. 0 = disabled; a manifest that omits the
	// knob gets DefaultStallThreshold.
	StallThreshold time.Duration
	// StallHardTimeout, when set, auto-cancels a run busy longer than this —
	// the non-clean exit unclaims the run's mail so it retries on the next
	// wake. 0 = disabled (the default: alert-only until thresholds are tuned).
	StallHardTimeout time.Duration
	// WebhookSecret, when set, is required on every external-event POST for
	// this space (header X-Evva-Webhook-Secret, RP-15). Unset keeps the RP-9
	// loopback trust: local callers post freely, non-loopback callers are
	// rejected outright.
	WebhookSecret string
	// RetentionDays drives the RP-16 ledger vacuum: messages read more than
	// this many days ago and tasks completed at least that long ago are
	// archived to .vero/archive/ and deleted, daily. 0 = retention disabled
	// (the pre-RP-16 "never deletes history" behavior); a manifest that omits
	// the knob gets DefaultRetentionDays.
	RetentionDays int
	// EventLog mirrors the space's event stream into .vero/events/ as daily
	// jsonl files (RP-17 forensics). A manifest that omits the knob gets true;
	// `event_log: false` turns the side-channel off entirely. Note the Go
	// zero value is OFF — programmatic spaces opt in, yaml spaces opt out.
	EventLog bool
	// TaskStaleThreshold is the RP-22 workflow watchdog's ledger line: a task
	// sitting in running/verifying longer than this raises one reminder to the
	// leader (and the operator) per state entry. 0 = disabled; a manifest that
	// omits the knob gets DefaultTaskStaleThreshold. suspended is exempt —
	// that state IS deliberate parking.
	TaskStaleThreshold time.Duration
	// MailboxStaleThreshold is the RP-22 bus-health tripwire: a member whose
	// oldest unread (unclaimed) message exceeds this age raises an alert —
	// under the normal wake chain (level-triggered drain + rescan) it should
	// never fire, so when it does it means a frozen/suspended member was
	// forgotten or the wake chain regressed. 0 = disabled; omitted gets
	// DefaultMailboxStaleThreshold.
	MailboxStaleThreshold time.Duration
	// VerifyChecks enables machine-checked verification (CHK): when set, the
	// service runs Command whenever a task enters `verifying` and lands its
	// exit/output tail on the task row as evidence. Absent = feature off,
	// space byte-identical to before the wave.
	VerifyChecks *CheckSpec
	// Notify enables outbound notifications (NTF): attention-worthy moments
	// (gates, errors, ops alerts) are pushed to a webhook and/or a local
	// command, so an operator away from the console learns within seconds.
	// Absent = feature off, zero behavior change.
	Notify *NotifySpec
	// BlackboardMaxBytes caps the team blackboard document (BB): the leader's
	// blackboard_write rejects anything larger, which is what bounds the
	// wake-brief token cost across N members × every wake. A manifest that
	// omits the knob gets DefaultBlackboardMaxBytes; values above
	// MaxBlackboardMaxBytes are rejected at load.
	BlackboardMaxBytes int
	// WorktreeIsolation gives every member its own git worktree + branch
	// instead of the shared space workdir (SWT), so two workers editing the
	// same repo concurrently cannot corrupt each other's work or the
	// operator's checkout. The leader always stays on the base checkout (D8)
	// — it is what integrates members' branches via worktree_merge.
	// Per-member Worktree overrides this. Default false: a space that never
	// opts in behaves byte-identically to before the wave.
	WorktreeIsolation bool
}

// Member.Worktree values (SWT). "" inherits Settings.WorktreeIsolation.
const (
	WorktreeOn  = "on"
	WorktreeOff = "off"
)

// ResolveWorktree resolves the effective worktree-isolation stance for one
// member: an explicit member-level override wins, otherwise the space setting
// applies. Mirrors the RP-24 permission_mode layering — member field beats
// settings — so a mixed team (coders isolated, a docs writer on root) is one
// line of manifest.
func ResolveWorktree(spaceDefault bool, memberOverride string) bool {
	switch strings.TrimSpace(memberOverride) {
	case WorktreeOn:
		return true
	case WorktreeOff:
		return false
	default:
		return spaceDefault
	}
}

// CheckSpec is the operator-authored verify-time check (CHK): one shell
// command plus its timeout. The command text is the manifest's trust surface
// — the same class as permission_mode: bypass — and no agent, leader
// included, can author or edit it; agents hold exactly one lever, the
// per-task check:"off" opt-out (CHK §4).
type CheckSpec struct {
	Command string
	Timeout time.Duration
}

// NotifySpec is the operator's outbound-notification config (NTF). At least
// one of URL/Command must be set; both may be. Delivery is best-effort by
// contract — one retry, then drop and count (the event-log discipline), so a
// dead endpoint can never wedge a space.
type NotifySpec struct {
	URL       string   // webhook endpoint; POSTed the payload
	Format    string   // NotifyFormatJSON (default) | NotifyFormatSlack
	Secret    string   // sent as X-Evva-Webhook-Secret when non-empty
	Events    []string // groups to send: gates | errors | alerts; empty = all three
	Command   string   // local exec (<shell> -c); receives the JSON payload on stdin
	RateLimit int      // max sends per minute per space; 0 = DefaultNotifyRateLimit
}

// Notify payload formats: plain JSON (the documented shape), or the
// lowest-common-denominator {"text": …} that Slack-compatible webhooks eat.
const (
	NotifyFormatJSON  = "json"
	NotifyFormatSlack = "slack"
)

// Notify event groups: gates = approval_needed + question_needed; errors =
// error + iter_limit; alerts = ops_alert (the promoted watchdog / breaker
// notices).
const (
	NotifyGroupGates  = "gates"
	NotifyGroupErrors = "errors"
	NotifyGroupAlerts = "alerts"
)

// DefaultNotifyRateLimit caps sends per minute per space when the manifest
// omits notify.rate_limit — enough for a real burst (a wide swarm's gate
// storm), low enough that a misbehaving space can't flood a channel.
const DefaultNotifyRateLimit = 12

// DefaultStallThreshold is the alert line a manifest gets when it does not set
// settings.stall_threshold. Long enough that legitimate tool-heavy runs don't
// page the operator; short enough that a hung run is noticed the same hour.
const DefaultStallThreshold = 10 * time.Minute

// DefaultMaxMembers is the live-roster ceiling member_spawn enforces when the
// manifest omits max_members (DWF): double the largest shipped example's
// roster (werewolf's 13) minus headroom — a guardrail, not a wall.
const DefaultMaxMembers = 16

// DefaultRetentionDays is the ledger retention window a manifest gets when it
// does not set settings.retention_days. A month keeps the web/API working set
// small on a 24/7 swarm while the archive retains the full history.
const DefaultRetentionDays = 30

// DefaultTaskStaleThreshold is the task-age reminder line a manifest gets when
// it does not set settings.task_stale_threshold. A day is long enough that
// ordinary multi-hour work never pings, short enough that a card forgotten on
// the board is surfaced the next morning.
const DefaultTaskStaleThreshold = 24 * time.Hour

// DefaultMailboxStaleThreshold is the unread-age tripwire a manifest gets when
// it does not set settings.mailbox_stale_threshold. Half an hour: the wake
// chain normally drains in seconds, so anything older signals a frozen or
// broken member, not load.
const DefaultMailboxStaleThreshold = 30 * time.Minute

// DefaultCheckTimeout / MaxCheckTimeout bound a verify check's runtime — the
// bash tool's norms (pkg/tools/shell: default 2 min, max 10 min). A check
// past its timeout is tree-killed and lands as failing evidence.
const (
	DefaultCheckTimeout = 2 * time.Minute
	MaxCheckTimeout     = 10 * time.Minute
)

// DefaultBlackboardMaxBytes / MaxBlackboardMaxBytes bound the team blackboard
// (BB §5.1): 4 KiB ≈ 1k tokens is a generous standing brief, and even the
// 16 KiB hard ceiling keeps the per-wake injection cost of one board within a
// few thousand tokens. The cap is enforced at write time — the ONE point that
// makes the wake-brief cost bounded by construction.
const (
	DefaultBlackboardMaxBytes = 4096
	MaxBlackboardMaxBytes     = 16384
)

// scheduleYml is the on-disk schedule block shared by the manifest's leader and
// workers (and mirrored by profile.yml). Exactly one of cron/every is set.
type scheduleYml struct {
	Cron   string `yaml:"cron,omitempty"`
	Every  string `yaml:"every,omitempty"`
	Prompt string `yaml:"prompt,omitempty"`
}

// memberYml is one leader/worker entry in evva-swarm.yml. Exactly one of
// agent/persona names the member (RP-29): agent → workdir directory member,
// persona → registry main-tier persona member.
type memberYml struct {
	Agent          string       `yaml:"agent,omitempty"`
	Persona        string       `yaml:"persona,omitempty"`
	Model          string       `yaml:"model,omitempty"`
	Effort         string       `yaml:"effort,omitempty"`
	WhenToUse      string       `yaml:"when_to_use,omitempty"`
	Schedule       *scheduleYml `yaml:"schedule,omitempty"`
	BudgetTokens   int          `yaml:"budget_tokens,omitempty"`
	PermissionMode string       `yaml:"permission_mode,omitempty"` // "" = inherit settings (RP-24)
	Worktree       string       `yaml:"worktree,omitempty"`        // "" = inherit settings (SWT)
}

// memberFromYml validates and converts one manifest member entry. ctx names
// the entry ("leader", `worker "x"`) for error messages. Exactly one of
// agent/persona must be set; effort, schedule, and permission_mode fail fast
// here so a typo rejects the manifest at register time.
func memberFromYml(y memberYml, ctx string) (Member, error) {
	agentName := strings.TrimSpace(y.Agent)
	personaName := strings.TrimSpace(y.Persona)
	if (agentName == "") == (personaName == "") {
		return Member{}, fmt.Errorf("agentdef: manifest %s: exactly one of agent/persona is required", ctx)
	}
	name := agentName
	fromPersona := false
	if personaName != "" {
		name, fromPersona = personaName, true
	}
	sched, err := parseScheduleYml(y.Schedule)
	if err != nil {
		return Member{}, fmt.Errorf("agentdef: manifest %s schedule: %w", ctx, err)
	}
	mode, err := parsePermissionMode(y.PermissionMode)
	if err != nil {
		return Member{}, fmt.Errorf("agentdef: manifest %s permission_mode: %w", ctx, err)
	}
	wt, err := parseWorktreeMode(y.Worktree)
	if err != nil {
		return Member{}, fmt.Errorf("agentdef: manifest %s worktree: %w", ctx, err)
	}
	effort := strings.TrimSpace(y.Effort)
	if effort != "" && llm.ParseEffort(effort) == 0 {
		return Member{}, fmt.Errorf("agentdef: manifest %s: invalid effort %q (want low|medium|high|ultra)", ctx, effort)
	}
	return Member{
		Agent: name, FromPersona: fromPersona,
		Model: strings.TrimSpace(y.Model), Effort: effort, WhenToUse: strings.TrimSpace(y.WhenToUse),
		Schedule: sched, BudgetTokens: y.BudgetTokens, PermissionMode: mode, Worktree: wt,
	}, nil
}

// memberToYml is the inverse of memberFromYml (WriteManifest).
func memberToYml(m Member) memberYml {
	y := memberYml{
		Model: m.Model, Effort: m.Effort, WhenToUse: m.WhenToUse,
		Schedule: toScheduleYml(m.Schedule), BudgetTokens: m.BudgetTokens, PermissionMode: m.PermissionMode,
		Worktree: m.Worktree,
	}
	if m.FromPersona {
		y.Persona = m.Agent
	} else {
		y.Agent = m.Agent
	}
	return y
}

// manifestYml is the on-disk schema for evva-swarm.yml (design §4.4).
type manifestYml struct {
	Name     string      `yaml:"name,omitempty"`
	Workdir  string      `yaml:"workdir,omitempty"`
	Leader   memberYml   `yaml:"leader"`
	Workers  []memberYml `yaml:"workers,omitempty"`
	Settings struct {
		PermissionMode         string     `yaml:"permission_mode,omitempty"`
		MaxIterations          int        `yaml:"max_iterations,omitempty"`
		DailyBudgetTokens      int        `yaml:"daily_budget_tokens,omitempty"`
		DailyBudgetTotalTokens int        `yaml:"daily_budget_total_tokens,omitempty"`
		DailyBudgetTotalUSD    float64    `yaml:"daily_budget_total_usd,omitempty"`
		BudgetStayFrozen       bool       `yaml:"budget_stay_frozen,omitempty"`
		MaxMembers             int        `yaml:"max_members,omitempty"`
		StallThreshold         string     `yaml:"stall_threshold,omitempty"`    // duration; "" = default, "0" = off
		StallHardTimeout       string     `yaml:"stall_hard_timeout,omitempty"` // duration; "" or "0" = off
		WebhookSecret          string     `yaml:"webhook_secret,omitempty"`
		RetentionDays          string     `yaml:"retention_days,omitempty"`          // days; "" = default 30, "0" = off
		EventLog               *bool      `yaml:"event_log,omitempty"`               // nil = default true
		TaskStaleThreshold     string     `yaml:"task_stale_threshold,omitempty"`    // duration; "" = default 24h, "0" = off
		MailboxStaleThreshold  string     `yaml:"mailbox_stale_threshold,omitempty"` // duration; "" = default 30m, "0" = off
		VerifyChecks           *checkYml  `yaml:"verify_checks,omitempty"`           // nil = checks off
		Notify                 *notifyYml `yaml:"notify,omitempty"`                  // nil = notifications off
		BlackboardMaxBytes     int        `yaml:"blackboard_max_bytes,omitempty"`    // 0 = default 4096, max 16384
		WorktreeIsolation      bool       `yaml:"worktree_isolation,omitempty"`      // false = shared workdir (SWT)
	} `yaml:"settings,omitempty"`
}

// checkYml is the on-disk settings.verify_checks block (CHK).
type checkYml struct {
	Command string `yaml:"command,omitempty"`
	Timeout string `yaml:"timeout,omitempty"` // duration; "" = default 2m, max 10m
}

// parseCheckYml validates the optional verify_checks block: absent = feature
// off; present demands a non-empty command and a timeout inside
// (0, MaxCheckTimeout] — "" defaults to DefaultCheckTimeout. An explicit "0"
// is rejected rather than read as "off": a check that can never run is a
// misconfiguration; disabling checks means removing the block.
func parseCheckYml(y *checkYml) (*CheckSpec, error) {
	if y == nil {
		return nil, nil
	}
	cmd := strings.TrimSpace(y.Command)
	if cmd == "" {
		return nil, fmt.Errorf("command is required (remove the verify_checks block to disable checks)")
	}
	timeout := DefaultCheckTimeout
	if s := strings.TrimSpace(y.Timeout); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil {
			return nil, fmt.Errorf("timeout: %w", err)
		}
		timeout = d
	}
	if timeout <= 0 || timeout > MaxCheckTimeout {
		return nil, fmt.Errorf("timeout must be within (0, %s], got %s", MaxCheckTimeout, timeout)
	}
	return &CheckSpec{Command: cmd, Timeout: timeout}, nil
}

// notifyYml is the on-disk settings.notify block (NTF).
type notifyYml struct {
	URL       string   `yaml:"url,omitempty"`
	Format    string   `yaml:"format,omitempty"`     // "" = json
	Secret    string   `yaml:"secret,omitempty"`     // X-Evva-Webhook-Secret
	Events    []string `yaml:"events,omitempty"`     // gates | errors | alerts; empty = all
	Command   string   `yaml:"command,omitempty"`    // local exec, JSON on stdin
	RateLimit int      `yaml:"rate_limit,omitempty"` // sends/min; 0 = default 12
}

// parseNotifyYml validates the optional notify block: absent = feature off;
// present demands at least one target (url and/or command), a known format,
// known event-group names, and a non-negative rate limit (0 = the default).
func parseNotifyYml(y *notifyYml) (*NotifySpec, error) {
	if y == nil {
		return nil, nil
	}
	spec := &NotifySpec{
		URL:       strings.TrimSpace(y.URL),
		Format:    strings.TrimSpace(y.Format),
		Secret:    strings.TrimSpace(y.Secret),
		Command:   strings.TrimSpace(y.Command),
		RateLimit: y.RateLimit,
	}
	if spec.URL == "" && spec.Command == "" {
		return nil, fmt.Errorf("at least one of url/command is required (remove the notify block to disable notifications)")
	}
	switch spec.Format {
	case "":
		spec.Format = NotifyFormatJSON
	case NotifyFormatJSON, NotifyFormatSlack:
	default:
		return nil, fmt.Errorf("invalid format %q (want %q or %q)", spec.Format, NotifyFormatJSON, NotifyFormatSlack)
	}
	if spec.RateLimit < 0 {
		return nil, fmt.Errorf("rate_limit must not be negative (got %d)", spec.RateLimit)
	}
	if spec.RateLimit == 0 {
		spec.RateLimit = DefaultNotifyRateLimit
	}
	seen := map[string]bool{}
	for _, g := range y.Events {
		g = strings.TrimSpace(g)
		switch g {
		case NotifyGroupGates, NotifyGroupErrors, NotifyGroupAlerts:
		default:
			return nil, fmt.Errorf("unknown events group %q (want %s|%s|%s)", g, NotifyGroupGates, NotifyGroupErrors, NotifyGroupAlerts)
		}
		if !seen[g] {
			seen[g] = true
			spec.Events = append(spec.Events, g)
		}
	}
	return spec, nil
}

// parseBlackboardMaxBytes reads the optional blackboard cap: 0 (omitted) →
// DefaultBlackboardMaxBytes, otherwise a positive byte count no larger than
// MaxBlackboardMaxBytes. There is no "0 = off" reading — the feature's off
// switch is an empty board, not an unwritable one; a cap nothing fits under
// is a misconfiguration and fails the manifest at register time.
func parseBlackboardMaxBytes(n int) (int, error) {
	if n == 0 {
		return DefaultBlackboardMaxBytes, nil
	}
	if n < 0 {
		return 0, fmt.Errorf("must be positive: %d", n)
	}
	if n > MaxBlackboardMaxBytes {
		return 0, fmt.Errorf("must not exceed %d bytes, got %d", MaxBlackboardMaxBytes, n)
	}
	return n, nil
}

// parseRetentionDays reads the optional retention knob: "" → DefaultRetentionDays,
// "0" → disabled, otherwise a positive whole number of days.
func parseRetentionDays(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return DefaultRetentionDays, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("not a whole number of days: %q", s)
	}
	if n < 0 {
		return 0, fmt.Errorf("must not be negative: %q", s)
	}
	return n, nil
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

// parsePermissionMode reads an optional permission_mode knob (settings-level
// or member-level, RP-24): "" inherits, anything else must be one of the four
// modes. Validated at load so a typo ("yolo") rejects the whole manifest at
// register time instead of silently falling back to default deep inside
// agent.New — the schedule-knob fail-fast precedent.
func parsePermissionMode(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	if _, ok := permission.ParseMode(s); !ok {
		return "", fmt.Errorf("invalid permission_mode %q (want default|accept_edits|plan|bypass)", s)
	}
	return s, nil
}

// parseWorktreeMode reads an optional member-level worktree knob (SWT): ""
// inherits settings.worktree_isolation, otherwise it must be "on" or "off".
// Validated at load — the parsePermissionMode precedent — so a typo ("true",
// "yes") rejects the manifest at register time rather than silently reading as
// "inherit" and quietly dropping the isolation the operator asked for.
func parseWorktreeMode(s string) (string, error) {
	switch s = strings.TrimSpace(s); s {
	case "", WorktreeOn, WorktreeOff:
		return s, nil
	default:
		return "", fmt.Errorf("invalid worktree %q (want on|off)", s)
	}
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

	leader, err := memberFromYml(y.Leader, "leader")
	if err != nil {
		return Manifest{}, err
	}
	settingsMode, err := parsePermissionMode(y.Settings.PermissionMode)
	if err != nil {
		return Manifest{}, fmt.Errorf("agentdef: manifest settings.permission_mode: %w", err)
	}
	stall, err := parseStallDuration(y.Settings.StallThreshold, DefaultStallThreshold)
	if err != nil {
		return Manifest{}, fmt.Errorf("agentdef: manifest settings.stall_threshold: %w", err)
	}
	hard, err := parseStallDuration(y.Settings.StallHardTimeout, 0)
	if err != nil {
		return Manifest{}, fmt.Errorf("agentdef: manifest settings.stall_hard_timeout: %w", err)
	}
	retention, err := parseRetentionDays(y.Settings.RetentionDays)
	if err != nil {
		return Manifest{}, fmt.Errorf("agentdef: manifest settings.retention_days: %w", err)
	}
	taskStale, err := parseStallDuration(y.Settings.TaskStaleThreshold, DefaultTaskStaleThreshold)
	if err != nil {
		return Manifest{}, fmt.Errorf("agentdef: manifest settings.task_stale_threshold: %w", err)
	}
	mailboxStale, err := parseStallDuration(y.Settings.MailboxStaleThreshold, DefaultMailboxStaleThreshold)
	if err != nil {
		return Manifest{}, fmt.Errorf("agentdef: manifest settings.mailbox_stale_threshold: %w", err)
	}
	checks, err := parseCheckYml(y.Settings.VerifyChecks)
	if err != nil {
		return Manifest{}, fmt.Errorf("agentdef: manifest settings.verify_checks: %w", err)
	}
	notify, err := parseNotifyYml(y.Settings.Notify)
	if err != nil {
		return Manifest{}, fmt.Errorf("agentdef: manifest settings.notify: %w", err)
	}
	boardCap, err := parseBlackboardMaxBytes(y.Settings.BlackboardMaxBytes)
	if err != nil {
		return Manifest{}, fmt.Errorf("agentdef: manifest settings.blackboard_max_bytes: %w", err)
	}
	// Space-level budget: negatives normalize to 0 = unlimited (RP-24 §5).
	// The member-level knob keeps its signed semantics (<0 = exempt); here
	// there is no space default to be exempt from, so the sign is meaningless
	// and an operator's `-1` plainly intends "no cap".
	budget := max(y.Settings.DailyBudgetTokens, 0)
	totalTok := max(y.Settings.DailyBudgetTotalTokens, 0)
	totalUSD := max(y.Settings.DailyBudgetTotalUSD, 0)
	m := Manifest{
		Name:    y.Name,
		Workdir: y.Workdir,
		Leader:  leader,
		Settings: Settings{
			PermissionMode:         settingsMode,
			MaxIterations:          y.Settings.MaxIterations,
			DailyBudgetTokens:      budget,
			DailyBudgetTotalTokens: totalTok,
			DailyBudgetTotalUSD:    totalUSD,
			BudgetStayFrozen:       y.Settings.BudgetStayFrozen,
			MaxMembers:             max(y.Settings.MaxMembers, 0),
			StallThreshold:         stall,
			StallHardTimeout:       hard,
			WebhookSecret:          strings.TrimSpace(y.Settings.WebhookSecret),
			RetentionDays:          retention,
			EventLog:               y.Settings.EventLog == nil || *y.Settings.EventLog,
			TaskStaleThreshold:     taskStale,
			MailboxStaleThreshold:  mailboxStale,
			VerifyChecks:           checks,
			Notify:                 notify,
			BlackboardMaxBytes:     boardCap,
			WorktreeIsolation:      y.Settings.WorktreeIsolation,
		},
	}
	for _, w := range y.Workers {
		wm, err := memberFromYml(w, fmt.Sprintf("worker %q", strings.TrimSpace(w.Agent+w.Persona)))
		if err != nil {
			return Manifest{}, err
		}
		m.Workers = append(m.Workers, wm)
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
	y.Leader = memberToYml(m.Leader)
	for _, w := range m.Workers {
		y.Workers = append(y.Workers, memberToYml(w))
	}
	y.Settings.PermissionMode = m.Settings.PermissionMode
	y.Settings.MaxIterations = m.Settings.MaxIterations
	y.Settings.DailyBudgetTokens = m.Settings.DailyBudgetTokens
	y.Settings.DailyBudgetTotalTokens = m.Settings.DailyBudgetTotalTokens
	y.Settings.DailyBudgetTotalUSD = m.Settings.DailyBudgetTotalUSD
	y.Settings.BudgetStayFrozen = m.Settings.BudgetStayFrozen
	y.Settings.MaxMembers = m.Settings.MaxMembers
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
	y.Settings.WebhookSecret = m.Settings.WebhookSecret
	// Retention round-trips like the stall knobs: default → omit, off → "0".
	switch m.Settings.RetentionDays {
	case DefaultRetentionDays: // omit
	case 0:
		y.Settings.RetentionDays = "0"
	default:
		y.Settings.RetentionDays = strconv.Itoa(m.Settings.RetentionDays)
	}
	if !m.Settings.EventLog { // default (true) omits; only an explicit off is written
		off := false
		y.Settings.EventLog = &off
	}
	// RP-22 stale fuses round-trip like the stall knobs: default omits, off = "0".
	switch m.Settings.TaskStaleThreshold {
	case DefaultTaskStaleThreshold: // omit
	case 0:
		y.Settings.TaskStaleThreshold = "0"
	default:
		y.Settings.TaskStaleThreshold = m.Settings.TaskStaleThreshold.String()
	}
	switch m.Settings.MailboxStaleThreshold {
	case DefaultMailboxStaleThreshold: // omit
	case 0:
		y.Settings.MailboxStaleThreshold = "0"
	default:
		y.Settings.MailboxStaleThreshold = m.Settings.MailboxStaleThreshold.String()
	}
	// verify_checks round-trips whole: absent block = off; the default
	// timeout emits nothing (reloads as the default).
	if c := m.Settings.VerifyChecks; c != nil {
		y.Settings.VerifyChecks = &checkYml{Command: c.Command}
		if c.Timeout != DefaultCheckTimeout {
			y.Settings.VerifyChecks.Timeout = c.Timeout.String()
		}
	}
	// notify round-trips whole: absent block = off; defaulted fields (json
	// format, default rate limit) emit nothing and reload as the defaults.
	if n := m.Settings.Notify; n != nil {
		ny := &notifyYml{URL: n.URL, Secret: n.Secret, Events: n.Events, Command: n.Command}
		if n.Format != NotifyFormatJSON {
			ny.Format = n.Format
		}
		if n.RateLimit != DefaultNotifyRateLimit {
			ny.RateLimit = n.RateLimit
		}
		y.Settings.Notify = ny
	}
	// The blackboard cap round-trips like the other defaulted knobs: the
	// default emits nothing (reloads as the default).
	if m.Settings.BlackboardMaxBytes != DefaultBlackboardMaxBytes {
		y.Settings.BlackboardMaxBytes = m.Settings.BlackboardMaxBytes
	}
	// Worktree isolation is plain omitempty: false = off = omitted, which is
	// exactly the default. Per-member overrides ride along in memberToYml.
	y.Settings.WorktreeIsolation = m.Settings.WorktreeIsolation
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
	// D8: the leader integrates members' branches onto the base checkout, so
	// it must BE on the base checkout. Only an explicit opt-in is an error —
	// settings.worktree_isolation deliberately does not reach the leader.
	if m.Leader.Worktree == WorktreeOn {
		return fmt.Errorf(`agentdef: manifest: leader.worktree: %q is not supported — the leader merges members' work onto the base checkout and must stay on it (it can read any worktree by absolute path)`, WorktreeOn)
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
