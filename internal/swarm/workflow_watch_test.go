package swarm

import (
	"strings"
	"testing"
	"time"

	"github.com/johnny1110/evva/internal/swarm/agentdef"
	"github.com/johnny1110/evva/internal/swarm/store"
)

// RP-22 workflow watchdog: task stale-age reminders (once per stay in a
// state) and mailbox backlog alerts (once per episode). The sweeps are called
// directly — they are tick-goroutine-only state, and these tests never Start
// the supervisor, so there is no tick to race.

// watchSpace is ctlSpace plus the RP-22 settings and live metrics the
// watchdog reads.
func watchSpace(t *testing.T, taskStale, mailboxStale time.Duration) (*SwarmSpace, *Supervisor) {
	t.Helper()
	sp, _ := ctlSpace(t, map[string]agentdef.Role{"leader": agentdef.RoleLeader, "w": agentdef.RoleWorker})
	sp.settings = agentdef.Settings{TaskStaleThreshold: taskStale, MailboxStaleThreshold: mailboxStale}
	sp.metrics = newSpaceMetrics()
	return sp, NewSupervisor(sp)
}

// unreadBodies returns recipient's unread messages as "subject\nbody" strings.
func unreadBodies(t *testing.T, sp *SwarmSpace, recipient string) []string {
	t.Helper()
	ids, err := sp.Store.UnreadFor(recipient)
	if err != nil {
		t.Fatalf("UnreadFor(%s): %v", recipient, err)
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		m, err := sp.Store.GetMessage(id)
		if err != nil {
			t.Fatalf("GetMessage: %v", err)
		}
		out = append(out, m.Subject+"\n"+m.Body)
	}
	return out
}

// drainUnread marks every unread message for recipient read — what the run
// loop would do, which these loop-less tests must do by hand.
func drainUnread(t *testing.T, sp *SwarmSpace, recipient string) {
	t.Helper()
	ids, err := sp.Store.UnreadFor(recipient)
	if err != nil {
		t.Fatalf("UnreadFor(%s): %v", recipient, err)
	}
	for _, id := range ids {
		if err := sp.Store.MarkRead(id); err != nil {
			t.Fatalf("MarkRead: %v", err)
		}
	}
}

func runningTask(t *testing.T, sp *SwarmSpace, title string) int64 {
	t.Helper()
	id, err := sp.Store.CreateTask(store.Task{Title: title, Spec: "spec", Assignee: "w", CreatedBy: "leader"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if err := sp.Store.TransitionTask(id, store.StatusRunning, store.Actor{Name: "leader", Role: store.RoleLeader}, ""); err != nil {
		t.Fatalf("TransitionTask: %v", err)
	}
	return id
}

func TestStaleTaskRemindsOncePerStay(t *testing.T) {
	sp, sup := watchSpace(t, 20*time.Millisecond, 0)
	id := runningTask(t, sp, "build the API")

	// Fresh task: not yet stale.
	sup.sweepStaleTasks(time.Now())
	if got := unreadBodies(t, sp, "leader"); len(got) != 0 {
		t.Fatalf("fresh task should not remind; leader got %v", got)
	}

	time.Sleep(40 * time.Millisecond)
	sup.sweepStaleTasks(time.Now())
	leaderMail := unreadBodies(t, sp, "leader")
	if len(leaderMail) != 1 || !strings.Contains(leaderMail[0], "task #1 stale: running") {
		t.Fatalf("leader reminder = %v, want one running-stale notice", leaderMail)
	}
	if !strings.Contains(leaderMail[0], "build the API") || !strings.Contains(leaderMail[0], "Suggested action") {
		t.Errorf("reminder lacks task details / suggested action:\n%s", leaderMail[0])
	}
	if userMail := unreadBodies(t, sp, "user"); len(userMail) != 1 {
		t.Errorf("operator copies = %d, want 1", len(userMail))
	}

	// Same stay → no second reminder, however often the sweep runs.
	sup.sweepStaleTasks(time.Now())
	sup.sweepStaleTasks(time.Now())
	if got := unreadBodies(t, sp, "leader"); len(got) != 1 {
		t.Fatalf("dedup failed: leader has %d reminders for one stay", len(got))
	}

	// Progressing to verifying is a NEW stay: after the threshold it reminds
	// again, with the verify-flavored suggested action.
	if err := sp.Store.TransitionTask(id, store.StatusVerifying, store.Actor{Name: "leader", Role: store.RoleLeader}, ""); err != nil {
		t.Fatalf("TransitionTask verifying: %v", err)
	}
	sup.sweepStaleTasks(time.Now())
	if got := unreadBodies(t, sp, "leader"); len(got) != 1 {
		t.Fatalf("verifying within threshold should not remind yet; got %d", len(got))
	}
	time.Sleep(40 * time.Millisecond)
	sup.sweepStaleTasks(time.Now())
	leaderMail = unreadBodies(t, sp, "leader")
	if len(leaderMail) != 2 || !strings.Contains(leaderMail[1], "verifying") || !strings.Contains(leaderMail[1], "task_verify") {
		t.Fatalf("verifying reminder missing or unflavored: %v", leaderMail)
	}

	if tasksStale, _ := sp.WorkflowStaleCounts(); tasksStale != 2 {
		t.Errorf("tasksStale counter = %d, want 2", tasksStale)
	}
}

func TestStaleTaskSuspendedAndDisabledExempt(t *testing.T) {
	sp, sup := watchSpace(t, 20*time.Millisecond, 0)
	id := runningTask(t, sp, "parked work")
	if err := sp.Store.TransitionTask(id, store.StatusSuspended, store.Actor{Name: "leader", Role: store.RoleLeader}, ""); err != nil {
		t.Fatalf("TransitionTask suspended: %v", err)
	}
	time.Sleep(40 * time.Millisecond)
	sup.sweepStaleTasks(time.Now())
	if got := unreadBodies(t, sp, "leader"); len(got) != 0 {
		t.Fatalf("suspended is deliberate parking — no reminder, got %v", got)
	}

	// Threshold "0" = off: a stale running task stays silent.
	spOff, supOff := watchSpace(t, 0, 0)
	runningTask(t, spOff, "ignored")
	time.Sleep(40 * time.Millisecond)
	supOff.sweepWorkflow(time.Now())
	if got := unreadBodies(t, spOff, "leader"); len(got) != 0 {
		t.Fatalf("disabled watchdog must change nothing, got %v", got)
	}
}

func TestStaleMailboxAlertsOncePerEpisode(t *testing.T) {
	sp, sup := watchSpace(t, 0, 20*time.Millisecond)
	sp.Roster.setMembership("w", MembershipFrozen)

	id, err := sp.Bus.Send(store.Message{Sender: "leader", Recipient: "w", Body: "are you alive?"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	time.Sleep(40 * time.Millisecond)
	sup.sweepStaleMailboxes(time.Now())

	userMail := unreadBodies(t, sp, "user")
	if len(userMail) != 1 || !strings.Contains(userMail[0], "mailbox backlog: w") {
		t.Fatalf("operator alert = %v, want one backlog notice for w", userMail)
	}
	if !strings.Contains(userMail[0], "FROZEN") {
		t.Errorf("alert should name the frozen state:\n%s", userMail[0])
	}
	leaderMail := unreadBodies(t, sp, "leader")
	if len(leaderMail) != 1 {
		t.Fatalf("leader CC = %d, want 1", len(leaderMail))
	}

	// Same episode → no duplicates.
	sup.sweepStaleMailboxes(time.Now())
	if got := unreadBodies(t, sp, "user"); len(got) != 1 {
		t.Fatalf("episode dedup failed: %d operator alerts", len(got))
	}

	// Backlog drains → mark clears; a NEW backlog alerts again. The leader's
	// CC must drain too — with no run loops in this test, an unread CC would
	// itself age into a (correct, but noisy-for-this-test) leader backlog.
	if err := sp.Store.MarkRead(id); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	drainUnread(t, sp, "leader")
	sup.sweepStaleMailboxes(time.Now())
	if _, err := sp.Bus.Send(store.Message{Sender: "leader", Recipient: "w", Body: "still there?"}); err != nil {
		t.Fatalf("Send #2: %v", err)
	}
	time.Sleep(40 * time.Millisecond)
	sup.sweepStaleMailboxes(time.Now())
	if got := unreadBodies(t, sp, "user"); len(got) != 2 {
		t.Fatalf("new episode should alert again; operator has %d", len(got))
	}

	if _, mailboxStale := sp.WorkflowStaleCounts(); mailboxStale != 2 {
		t.Errorf("mailboxStale counter = %d, want 2", mailboxStale)
	}
}

func TestStaleMailboxLeaderBacklogGoesToOperatorOnly(t *testing.T) {
	sp, sup := watchSpace(t, 0, 20*time.Millisecond)
	sp.Roster.setRun("leader", RunSuspended)

	if _, err := sp.Bus.Send(store.Message{Sender: "w", Recipient: "leader", Body: "report"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	time.Sleep(40 * time.Millisecond)
	sup.sweepStaleMailboxes(time.Now())

	userMail := unreadBodies(t, sp, "user")
	if len(userMail) != 1 || !strings.Contains(userMail[0], "mailbox backlog: leader") || !strings.Contains(userMail[0], "SUSPENDED") {
		t.Fatalf("operator alert = %v, want one suspended-leader backlog notice", userMail)
	}
	// The leader's own unread must hold only the original report — the alert
	// about its backlog must not be mailed back into the backlog.
	if got := unreadBodies(t, sp, "leader"); len(got) != 1 || !strings.Contains(got[0], "report") {
		t.Fatalf("leader unread = %v, want just the original report", got)
	}
}

func TestSweepWorkflowThrottle(t *testing.T) {
	sp, sup := watchSpace(t, 20*time.Millisecond, 0)
	sup.workflowSweepInterval = time.Hour

	runningTask(t, sp, "first")
	time.Sleep(40 * time.Millisecond)
	now := time.Now()
	sup.sweepWorkflow(now)
	if got := unreadBodies(t, sp, "leader"); len(got) != 1 {
		t.Fatalf("first sweep should remind; got %d", len(got))
	}

	// A second stale task appears, but the throttle holds the next pass.
	runningTask(t, sp, "second")
	time.Sleep(40 * time.Millisecond)
	sup.sweepWorkflow(time.Now())
	if got := unreadBodies(t, sp, "leader"); len(got) != 1 {
		t.Fatalf("throttled sweep must not scan; got %d", len(got))
	}

	// Past the interval the pass runs and catches up.
	sup.workflowSweepInterval = time.Millisecond
	sup.sweepWorkflow(time.Now())
	if got := unreadBodies(t, sp, "leader"); len(got) != 2 {
		t.Fatalf("post-throttle sweep should catch the second task; got %d", len(got))
	}
}

func TestHumanAge(t *testing.T) {
	cases := map[time.Duration]string{
		12 * time.Second:              "12s",
		45 * time.Minute:              "45m",
		26 * time.Hour:                "26h",
		50*time.Hour + 30*time.Minute: "2d2h",
		3 * 24 * time.Hour:            "3d0h",
		90 * time.Second:              "1m",
		time.Hour + 59*time.Minute:    "1h",
	}
	for in, want := range cases {
		if got := humanAge(in); got != want {
			t.Errorf("humanAge(%v) = %q, want %q", in, got, want)
		}
	}
}
