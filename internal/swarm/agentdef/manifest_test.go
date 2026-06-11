package agentdef

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLoadManifestHappy(t *testing.T) {
	m, err := LoadManifest(filepath.Join("testdata", "evva-swarm.yml"))
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m.Name != "test-eng-team" {
		t.Errorf("Name = %q", m.Name)
	}
	if m.Leader.Agent != "leader" {
		t.Errorf("Leader = %q", m.Leader.Agent)
	}
	want := []Member{{Agent: "backend-dev"}, {Agent: "frontend-dev"}}
	if !reflect.DeepEqual(m.Workers, want) {
		t.Errorf("Workers = %+v, want %+v", m.Workers, want)
	}
	if m.Settings.PermissionMode != "default" || m.Settings.MaxIterations != 50 {
		t.Errorf("Settings = %+v", m.Settings)
	}
}

func writeManifest(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "evva-swarm.yml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadManifestMissingFile(t *testing.T) {
	if _, err := LoadManifest(filepath.Join("testdata", "nope.yml")); err == nil {
		t.Fatal("want error for missing manifest")
	}
}

func TestLoadManifestDuplicateWorker(t *testing.T) {
	p := writeManifest(t, `
name: dup
leader:
  agent: leader
workers:
  - agent: eng
  - agent: eng
`)
	_, err := LoadManifest(p)
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("err = %v, want a duplicate-name error", err)
	}
}

func TestLoadManifestWorkerCollidesWithLeader(t *testing.T) {
	p := writeManifest(t, `
name: dup
leader:
  agent: leader
workers:
  - agent: leader
`)
	if _, err := LoadManifest(p); err == nil {
		t.Fatal("want error when a worker reuses the leader's name")
	}
}

func TestLoadManifestMissingLeader(t *testing.T) {
	p := writeManifest(t, `
name: noleader
workers:
  - agent: eng
`)
	if _, err := LoadManifest(p); err == nil {
		t.Fatal("want error when leader.agent is missing")
	}
}

func TestLoadManifestEmptyWorkerName(t *testing.T) {
	p := writeManifest(t, `
name: empty
leader:
  agent: leader
workers:
  - agent: ""
`)
	if _, err := LoadManifest(p); err == nil {
		t.Fatal("want error for an empty worker name")
	}
}

// A missing name is now ACCEPTED (Docker-style): the service assigns a handle
// (--name > manifest name > generated), so the manifest no longer requires one.
func TestLoadManifestMissingNameIsAllowed(t *testing.T) {
	p := writeManifest(t, `
leader:
  agent: leader
`)
	m, err := LoadManifest(p)
	if err != nil {
		t.Fatalf("a nameless manifest should load, got %v", err)
	}
	if m.Name != "" {
		t.Fatalf("Name = %q, want empty (service assigns the handle)", m.Name)
	}
}

func TestManifestBudgetFieldsRoundTrip(t *testing.T) {
	p := writeManifest(t, `
name: budgeted
leader:
  agent: lead
  budget_tokens: -1
workers:
  - agent: w1
    budget_tokens: 250000
  - agent: w2
settings:
  daily_budget_tokens: 1000000
  budget_stay_frozen: true
`)
	m, err := LoadManifest(p)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m.Settings.DailyBudgetTokens != 1000000 || !m.Settings.BudgetStayFrozen {
		t.Errorf("Settings = %+v", m.Settings)
	}
	if m.Leader.BudgetTokens != -1 {
		t.Errorf("leader budget = %d, want -1", m.Leader.BudgetTokens)
	}
	if m.Workers[0].BudgetTokens != 250000 || m.Workers[1].BudgetTokens != 0 {
		t.Errorf("worker budgets = %d/%d, want 250000/0", m.Workers[0].BudgetTokens, m.Workers[1].BudgetTokens)
	}

	// WriteManifest must carry the budget fields back out.
	out := filepath.Join(t.TempDir(), "evva-swarm.yml")
	if err := WriteManifest(out, m); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	m2, err := LoadManifest(out)
	if err != nil {
		t.Fatalf("re-LoadManifest: %v", err)
	}
	if m2.Settings.DailyBudgetTokens != 1000000 || !m2.Settings.BudgetStayFrozen ||
		m2.Leader.BudgetTokens != -1 || m2.Workers[0].BudgetTokens != 250000 {
		t.Errorf("round-trip lost budget fields: %+v / leader %d / w1 %d",
			m2.Settings, m2.Leader.BudgetTokens, m2.Workers[0].BudgetTokens)
	}
}

// RP-24: the per-member permission_mode knob — member field > settings, ""
// inherits, invalid values reject the whole manifest at load (fail-fast).
func TestManifestPermissionModeKnob(t *testing.T) {
	p := writeManifest(t, `
name: tiered
leader:
  agent: lead
workers:
  - agent: trader
    permission_mode: bypass
  - agent: analyst
settings:
  permission_mode: default
`)
	m, err := LoadManifest(p)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m.Settings.PermissionMode != "default" {
		t.Errorf("settings mode = %q, want default", m.Settings.PermissionMode)
	}
	if m.Leader.PermissionMode != "" {
		t.Errorf("leader mode = %q, want empty (inherit)", m.Leader.PermissionMode)
	}
	if m.Workers[0].PermissionMode != "bypass" || m.Workers[1].PermissionMode != "" {
		t.Errorf("worker modes = %q/%q, want bypass/empty",
			m.Workers[0].PermissionMode, m.Workers[1].PermissionMode)
	}

	// WriteManifest must carry the member modes back out.
	out := filepath.Join(t.TempDir(), "evva-swarm.yml")
	if err := WriteManifest(out, m); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	m2, err := LoadManifest(out)
	if err != nil {
		t.Fatalf("re-LoadManifest: %v", err)
	}
	if m2.Workers[0].PermissionMode != "bypass" || m2.Settings.PermissionMode != "default" {
		t.Errorf("round-trip lost permission modes: %+v / w0 %q", m2.Settings, m2.Workers[0].PermissionMode)
	}
}

func TestManifestPermissionModeInvalid(t *testing.T) {
	for name, yml := range map[string]string{
		"member": `
leader:
  agent: lead
workers:
  - agent: w1
    permission_mode: yolo
`,
		"settings": `
leader:
  agent: lead
settings:
  permission_mode: yolo
`,
	} {
		_, err := LoadManifest(writeManifest(t, yml))
		if err == nil || !strings.Contains(err.Error(), "permission_mode") {
			t.Errorf("%s-level invalid mode: err = %v, want a permission_mode error", name, err)
		}
	}
}

// RP-24 §5: a negative settings-level daily budget normalizes to 0 (unlimited)
// at load; the member-level knob keeps its signed semantics (<0 = exempt).
func TestManifestNegativeSettingsBudgetNormalizes(t *testing.T) {
	p := writeManifest(t, `
leader:
  agent: lead
  budget_tokens: -1
settings:
  daily_budget_tokens: -1
`)
	m, err := LoadManifest(p)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m.Settings.DailyBudgetTokens != 0 {
		t.Errorf("settings budget = %d, want 0 (negatives normalize to unlimited)", m.Settings.DailyBudgetTokens)
	}
	if m.Leader.BudgetTokens != -1 {
		t.Errorf("leader budget = %d, want -1 (member-level sign is meaningful: exempt)", m.Leader.BudgetTokens)
	}
}

func TestManifestStallKnobs(t *testing.T) {
	// Explicit values parse and round-trip.
	p := writeManifest(t, `
name: stallish
leader:
  agent: lead
settings:
  stall_threshold: 5m
  stall_hard_timeout: 30m
`)
	m, err := LoadManifest(p)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m.Settings.StallThreshold != 5*time.Minute || m.Settings.StallHardTimeout != 30*time.Minute {
		t.Errorf("stall knobs = %v/%v, want 5m/30m", m.Settings.StallThreshold, m.Settings.StallHardTimeout)
	}
	out := filepath.Join(t.TempDir(), "evva-swarm.yml")
	if err := WriteManifest(out, m); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	if m2, err := LoadManifest(out); err != nil || m2.Settings.StallThreshold != 5*time.Minute || m2.Settings.StallHardTimeout != 30*time.Minute {
		t.Errorf("round-trip = %v/%v (err %v)", m2.Settings.StallThreshold, m2.Settings.StallHardTimeout, err)
	}

	// Omitted → alert default, hard off; "0" → explicitly off, surviving a round-trip.
	p = writeManifest(t, "name: bare\nleader:\n  agent: lead\n")
	if m, err = LoadManifest(p); err != nil || m.Settings.StallThreshold != DefaultStallThreshold || m.Settings.StallHardTimeout != 0 {
		t.Errorf("defaults = %v/%v (err %v), want %v/0", m.Settings.StallThreshold, m.Settings.StallHardTimeout, err, DefaultStallThreshold)
	}
	p = writeManifest(t, "name: off\nleader:\n  agent: lead\nsettings:\n  stall_threshold: \"0\"\n")
	if m, err = LoadManifest(p); err != nil || m.Settings.StallThreshold != 0 {
		t.Errorf("explicit off = %v (err %v), want 0", m.Settings.StallThreshold, err)
	}
	if err := WriteManifest(out, m); err != nil {
		t.Fatalf("WriteManifest(off): %v", err)
	}
	if m2, err := LoadManifest(out); err != nil || m2.Settings.StallThreshold != 0 {
		t.Errorf("off round-trip = %v (err %v), want 0", m2.Settings.StallThreshold, err)
	}

	// Garbage and negatives are load-time errors.
	for _, bad := range []string{"abc", "-5m"} {
		p = writeManifest(t, "name: bad\nleader:\n  agent: lead\nsettings:\n  stall_threshold: "+bad+"\n")
		if _, err := LoadManifest(p); err == nil {
			t.Errorf("stall_threshold %q should fail to load", bad)
		}
	}
}

// RP-15: settings.webhook_secret loads (trimmed), defaults to "", and
// round-trips through WriteManifest.
func TestManifestWebhookSecret(t *testing.T) {
	p := writeManifest(t, "leader:\n  agent: lead\nsettings:\n  webhook_secret: \"  hunter2  \"\n")
	m, err := LoadManifest(p)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m.Settings.WebhookSecret != "hunter2" {
		t.Fatalf("WebhookSecret = %q, want trimmed %q", m.Settings.WebhookSecret, "hunter2")
	}
	if err := WriteManifest(p, m); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	back, err := LoadManifest(p)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if back.Settings.WebhookSecret != "hunter2" {
		t.Fatalf("round-trip WebhookSecret = %q, want %q", back.Settings.WebhookSecret, "hunter2")
	}

	plain := writeManifest(t, "leader:\n  agent: lead\n")
	m2, err := LoadManifest(plain)
	if err != nil {
		t.Fatalf("LoadManifest (omitted): %v", err)
	}
	if m2.Settings.WebhookSecret != "" {
		t.Fatalf("omitted webhook_secret = %q, want empty", m2.Settings.WebhookSecret)
	}
}

// RP-16: settings.retention_days — "" → default 30, "0" → off, "45" → 45;
// garbage / negative fail the load; values round-trip (default omits, off
// emits "0").
func TestManifestRetentionKnob(t *testing.T) {
	m, err := LoadManifest(writeManifest(t, "leader:\n  agent: lead\n"))
	if err != nil {
		t.Fatalf("LoadManifest (omitted): %v", err)
	}
	if m.Settings.RetentionDays != DefaultRetentionDays {
		t.Fatalf("omitted retention_days = %d, want default %d", m.Settings.RetentionDays, DefaultRetentionDays)
	}

	for yml, want := range map[string]int{
		"leader:\n  agent: lead\nsettings:\n  retention_days: \"0\"\n": 0,
		"leader:\n  agent: lead\nsettings:\n  retention_days: 45\n":    45,
	} {
		p := writeManifest(t, yml)
		m, err := LoadManifest(p)
		if err != nil {
			t.Fatalf("LoadManifest: %v", err)
		}
		if m.Settings.RetentionDays != want {
			t.Fatalf("retention_days = %d, want %d", m.Settings.RetentionDays, want)
		}
		if err := WriteManifest(p, m); err != nil {
			t.Fatalf("WriteManifest: %v", err)
		}
		back, err := LoadManifest(p)
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		if back.Settings.RetentionDays != want {
			t.Fatalf("round-trip retention_days = %d, want %d", back.Settings.RetentionDays, want)
		}
	}

	for _, bad := range []string{"abc", "-3", "1.5"} {
		p := writeManifest(t, "leader:\n  agent: lead\nsettings:\n  retention_days: \""+bad+"\"\n")
		if _, err := LoadManifest(p); err == nil {
			t.Errorf("retention_days %q should fail to load", bad)
		}
	}
}

// RP-17: settings.event_log — omitted → true, explicit false → false, and the
// value round-trips (true omits, false writes).
func TestManifestEventLogKnob(t *testing.T) {
	m, err := LoadManifest(writeManifest(t, "leader:\n  agent: lead\n"))
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if !m.Settings.EventLog {
		t.Fatal("omitted event_log should default to true")
	}

	p := writeManifest(t, "leader:\n  agent: lead\nsettings:\n  event_log: false\n")
	m, err = LoadManifest(p)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m.Settings.EventLog {
		t.Fatal("event_log: false should disable")
	}
	if err := WriteManifest(p, m); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	back, err := LoadManifest(p)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if back.Settings.EventLog {
		t.Fatal("explicit off lost in the round-trip")
	}
}

// RP-22: the workflow-watchdog fuses parse with stall-knob semantics
// ("" = default, "0" = off) and round-trip through WriteManifest.
func TestManifestWorkflowStaleKnobs(t *testing.T) {
	p := writeManifest(t, `
name: watchful
leader:
  agent: lead
settings:
  task_stale_threshold: 6h
  mailbox_stale_threshold: 15m
`)
	m, err := LoadManifest(p)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m.Settings.TaskStaleThreshold != 6*time.Hour || m.Settings.MailboxStaleThreshold != 15*time.Minute {
		t.Errorf("stale knobs = %v/%v, want 6h/15m", m.Settings.TaskStaleThreshold, m.Settings.MailboxStaleThreshold)
	}
	out := filepath.Join(t.TempDir(), "evva-swarm.yml")
	if err := WriteManifest(out, m); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	if m2, err := LoadManifest(out); err != nil || m2.Settings.TaskStaleThreshold != 6*time.Hour || m2.Settings.MailboxStaleThreshold != 15*time.Minute {
		t.Errorf("round-trip = %v/%v (err %v)", m2.Settings.TaskStaleThreshold, m2.Settings.MailboxStaleThreshold, err)
	}

	// Omitted → defaults; "0" → off, surviving a round-trip.
	p = writeManifest(t, "name: bare\nleader:\n  agent: lead\n")
	if m, err = LoadManifest(p); err != nil ||
		m.Settings.TaskStaleThreshold != DefaultTaskStaleThreshold ||
		m.Settings.MailboxStaleThreshold != DefaultMailboxStaleThreshold {
		t.Errorf("defaults = %v/%v (err %v), want %v/%v", m.Settings.TaskStaleThreshold,
			m.Settings.MailboxStaleThreshold, err, DefaultTaskStaleThreshold, DefaultMailboxStaleThreshold)
	}
	p = writeManifest(t, "name: off\nleader:\n  agent: lead\nsettings:\n  task_stale_threshold: \"0\"\n  mailbox_stale_threshold: \"0\"\n")
	if m, err = LoadManifest(p); err != nil || m.Settings.TaskStaleThreshold != 0 || m.Settings.MailboxStaleThreshold != 0 {
		t.Errorf("explicit off = %v/%v (err %v), want 0/0", m.Settings.TaskStaleThreshold, m.Settings.MailboxStaleThreshold, err)
	}
	if err := WriteManifest(out, m); err != nil {
		t.Fatalf("WriteManifest(off): %v", err)
	}
	if m2, err := LoadManifest(out); err != nil || m2.Settings.TaskStaleThreshold != 0 || m2.Settings.MailboxStaleThreshold != 0 {
		t.Errorf("off round-trip = %v/%v (err %v), want 0/0", m2.Settings.TaskStaleThreshold, m2.Settings.MailboxStaleThreshold, err)
	}

	// Garbage fails at load.
	p = writeManifest(t, "name: bad\nleader:\n  agent: lead\nsettings:\n  task_stale_threshold: nonsense\n")
	if _, err := LoadManifest(p); err == nil {
		t.Error("task_stale_threshold garbage should fail to load")
	}
}
