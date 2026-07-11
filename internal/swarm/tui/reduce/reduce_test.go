package reduce

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnny1110/evva/internal/swarm/tui/wire"
)

// foldFixture replays a JSONL fixture (one wire event per line, the /chatlog
// element shape) through Fold — the recorded-space harness the PRD's TUI-1
// golden contract asks for.
func foldFixture(t *testing.T, name string) []*Turn {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var turns []*Turn
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		ev, _ := wire.ParseEvent([]byte(line))
		turns = Fold(turns, ev)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return turns
}

// render serializes folded turns into the golden's stable text form: one
// line per turn — type, agent/target, open marker, and the content fields
// that matter. Any semantic change to Fold changes this text and fails the
// byte-for-byte golden compare.
func render(turns []*Turn) string {
	var b strings.Builder
	for _, t := range turns {
		open := ""
		if t.Open {
			open = " open"
		}
		switch t.Type {
		case TurnUser:
			fmt.Fprintf(&b, "user →%s%s | %s\n", t.Target, open, t.Text)
		case TurnTool:
			fmt.Fprintf(&b, "tool %s [%s] %s%s | in=%s out=%s\n", t.AgentID, t.ToolID, t.Tool, open, string(t.Input), t.Result)
			b.WriteString("  status=" + string(t.Status) + "\n")
		default:
			fmt.Fprintf(&b, "%s %s%s | %s\n", t.Type, t.AgentID, open, t.Text)
		}
	}
	return b.String()
}

const golden = `user →lead | kickoff — ship v2
thinking ag-lead | plan: split into two tasks
assistant ag-lead | Creating the task graph.
assistant ag-qa | reading the spec
tool ag-lead [t1] task_create | in={"title":"build"} out=task #42 created
  status=done
system qa | task #42 "build" auto-dispatched → qa
tool ag-qa [t2] bash | in={"command":"go test ./..."} out=FAIL: TestX (exit 1)
  status=error
assistant ag-dev | done with the refactor
user →all | standup in 5
system lead | blackboard updated by lead (120 bytes)
error ag-dev | provider 500
assistant ag-qa |  — retrying
`

// The golden pins the Go reducer to events.ts semantics on a recorded space:
// chunk accumulation per agent (interleaved members never merge), the
// thinking→text block switch, tool cards resolving by ToolID (error status
// included), user_message subject—body merge + broadcast, engine system
// lines, error turns, turn_end/run_end closure, and unknown-kind tolerance
// (store_update folds to nothing). Regenerating from the same input must be
// byte-identical.
func TestFoldGolden(t *testing.T) {
	got := render(foldFixture(t, "space.jsonl"))
	if got != golden {
		t.Errorf("fold drifted from the events.ts contract:\n--- got ---\n%s--- want ---\n%s", got, golden)
	}
	// Determinism: the same input folds to the same bytes.
	if again := render(foldFixture(t, "space.jsonl")); again != got {
		t.Error("fold is not deterministic across replays")
	}
}

// Chunk-boundary details the golden can't isolate: same-agent text after a
// closed turn opens a NEW turn (not appended), and a chunk arriving after
// turn_end reopens.
func TestFoldChunkBoundaries(t *testing.T) {
	ev := func(kind, agent, text string) *wire.Event {
		raw := fmt.Sprintf(`{"Kind":%q,"AgentID":%q,"Text":{"Text":%q}}`, kind, agent, text)
		e, _ := wire.ParseEvent([]byte(raw))
		return e
	}
	var turns []*Turn
	turns = Fold(turns, ev("text_chunk", "a", "one"))
	turns = Fold(turns, ev("turn_end", "a", ""))
	turns = Fold(turns, ev("text_chunk", "a", "two"))
	if len(turns) != 2 || turns[0].Text != "one" || turns[0].Open || turns[1].Text != "two" || !turns[1].Open {
		t.Fatalf("turn_end must close the block and a later chunk must open a new one: %+v", render(turns))
	}

	// A whole-block text (buffered provider) appends into an open chunked turn
	// of the same kind — events.ts routes text and text_chunk identically.
	turns = Fold(turns, ev("text", "a", " more"))
	if len(turns) != 2 || turns[1].Text != "two more" {
		t.Fatalf("whole-block text should coalesce like a chunk: %s", render(turns))
	}
}

// user_message with an empty body and no subject folds to nothing; a
// tool_use_result with an unknown ToolID is a no-op (never panics).
func TestFoldDegenerateInputs(t *testing.T) {
	var turns []*Turn
	e, _ := wire.ParseEvent([]byte(`{"Kind":"user_message","UserMessage":{"Sender":"user","Recipient":"x","Body":""}}`))
	turns = Fold(turns, e)
	e, _ = wire.ParseEvent([]byte(`{"Kind":"tool_use_result","AgentID":"a","ToolUseResult":{"ToolID":"ghost","Content":"x"}}`))
	turns = Fold(turns, e)
	e, _ = wire.ParseEvent([]byte(`{"Kind":"totally_unknown_kind","AgentID":"a"}`))
	turns = Fold(turns, e)
	if len(turns) != 0 {
		t.Fatalf("degenerate inputs must fold to nothing: %s", render(turns))
	}
	if Fold(nil, nil) != nil {
		t.Fatal("nil event must be a no-op")
	}
}

func TestConsoleTurns(t *testing.T) {
	turns := foldFixture(t, "space.jsonl")
	qa := ConsoleTurns(turns, "ag-qa", "qa")
	for _, tr := range qa {
		if tr.Type == TurnUser {
			t.Errorf("qa console must not carry mail addressed elsewhere: %+v", tr)
			continue
		}
		if tr.AgentID != "ag-qa" {
			t.Errorf("qa console leaked turn from %q", tr.AgentID)
		}
	}
	lead := ConsoleTurns(turns, "ag-lead", "lead")
	if len(lead) == 0 || lead[0].Type != TurnUser || lead[0].Target != "lead" {
		t.Fatalf("lead console should open with the operator kickoff mail: %+v", lead)
	}
}

// The phase reducer follows the same fixture: after the replay, qa sits in
// ready (run_end), lead in running (turn_end), dev in error.
func TestReducePhaseOnFixture(t *testing.T) {
	f, err := os.Open(filepath.Join("testdata", "space.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	m := PhaseMap{}
	now := time.Date(2026, 7, 11, 10, 31, 0, 0, time.UTC)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		ev, _ := wire.ParseEvent([]byte(sc.Text()))
		ReducePhase(m, ev, now)
	}
	if got := m["ag-qa"].Phase; got != "ready" {
		t.Errorf("qa phase = %q, want ready", got)
	}
	if got := m["ag-lead"].Phase; got != "running" {
		t.Errorf("lead phase = %q, want running", got)
	}
	if got := m["ag-dev"].Phase; got != "error" {
		t.Errorf("dev phase = %q, want error", got)
	}
}

// Since restamps only on a PHASE change: a tool-only change keeps the clock
// (roster.go setPhase semantics), and an identical event is a no-op.
func TestReducePhaseSinceSemantics(t *testing.T) {
	m := PhaseMap{}
	t0 := time.Date(2026, 7, 11, 10, 0, 0, 0, time.UTC)
	mk := func(tool string) *wire.Event {
		e, _ := wire.ParseEvent(fmt.Appendf(nil,
			`{"Kind":"tool_use_start","AgentID":"a","ToolUseStart":{"Name":%q,"ToolID":"x"}}`, tool))
		return e
	}
	ReducePhase(m, mk("bash"), t0)
	ReducePhase(m, mk("bash"), t0.Add(time.Minute)) // identical → no-op
	if m["a"].Since != t0 {
		t.Fatalf("identical phase must keep since, got %v", m["a"].Since)
	}
	ReducePhase(m, mk("grep"), t0.Add(2*time.Minute)) // tool-only change keeps clock
	if m["a"].Tool != "grep" || m["a"].Since != t0 {
		t.Fatalf("tool-only change must keep since: %+v", m["a"])
	}
	e, _ := wire.ParseEvent([]byte(`{"Kind":"run_end","AgentID":"a"}`))
	ReducePhase(m, e, t0.Add(3*time.Minute)) // phase change restamps
	if m["a"].Phase != "ready" || m["a"].Since != t0.Add(3*time.Minute) {
		t.Fatalf("phase change must restamp since: %+v", m["a"])
	}
}

// Attention ordering matches events.ts on the same roster: act before warn,
// longest-waiting first within a kind; stalls promote to warn.
func TestAttentionItemsOrdering(t *testing.T) {
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) time.Time { return now.Add(-d) }
	roster := []Member{
		{Name: "fine", Phase: "running", PhaseSince: at(time.Minute)},
		{Name: "err", Phase: "error", PhaseSince: at(2 * time.Minute)},
		{Name: "gate-new", Phase: "waiting-approval", Tool: "bash", PhaseSince: at(30 * time.Second)},
		{Name: "gate-old", Phase: "waiting-input", PhaseSince: at(10 * time.Minute)},
		{Name: "stalled", Phase: "executing", Tool: "bash", PhaseSince: at(6 * time.Minute)},
		{Name: "thinking-ok", Phase: "thinking", PhaseSince: at(time.Minute)},
	}
	items := AttentionItems(roster, now)
	var names []string
	for _, it := range items {
		names = append(names, it.Name)
	}
	want := []string{"gate-old", "gate-new", "stalled", "err"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("attention order = %v, want %v", names, want)
	}
	for _, it := range items {
		if it.Name == "stalled" && (!it.Stalled || it.Kind != "warn") {
			t.Errorf("stalled member must surface as warn+stalled: %+v", it)
		}
	}
}

func TestOrderRoster(t *testing.T) {
	members := []Member{
		{Name: "zeta", Run: "idle"},
		{Name: "frozen-one", Membership: "frozen"},
		{Name: "busy-b", Run: "busy"},
		{Name: "lead", Role: "leader", Run: "idle"},
		{Name: "gate", Run: "busy", Phase: "waiting-approval"},
		{Name: "susp", Run: "suspended"},
		{Name: "busy-a", Run: "busy"},
	}
	got := OrderRoster(members, []string{"gate"})
	var names []string
	for _, m := range got {
		names = append(names, m.Name)
	}
	want := "lead,gate,busy-a,busy-b,zeta,susp,frozen-one"
	if strings.Join(names, ",") != want {
		t.Fatalf("roster order = %s, want %s", strings.Join(names, ","), want)
	}
}

func TestDisplayPhaseAndClockAndElapsed(t *testing.T) {
	if got := DisplayPhase(Member{Run: "suspended", Phase: "executing"}); got != "suspended" {
		t.Errorf("suspended wins: %q", got)
	}
	if got := DisplayPhase(Member{Run: "busy", Phase: "texting"}); got != "thinking" {
		t.Errorf("thinking collapse: %q", got)
	}
	if got := DisplayPhase(Member{Run: "busy", Phase: "executing", Tool: "bash"}); got != "executing:bash" {
		t.Errorf("tool suffix: %q", got)
	}
	if got := DisplayPhase(Member{Run: "idle"}); got != "idle" {
		t.Errorf("bare run: %q", got)
	}
	if Clock(time.Time{}) != "" {
		t.Error("zero instant renders no clock")
	}
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	if got := Elapsed(now.Add(-90*time.Second), now); got != "1:30" {
		t.Errorf("elapsed = %q, want 1:30", got)
	}
	if got := Elapsed(now.Add(-2*time.Second), now); got != "2s" {
		t.Errorf("elapsed = %q, want 2s", got)
	}
}
