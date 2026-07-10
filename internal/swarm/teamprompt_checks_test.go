package swarm

import (
	"strings"
	"testing"

	"github.com/johnny1110/evva/internal/swarm/agentdef"
)

// TestChecksProtocolSection: a space with verify_checks teaches every member
// the command that judges verify-time state; only the leader learns the
// policy levers. Without the knob the prompt carries no trace of the wave.
func TestChecksProtocolSection(t *testing.T) {
	spec := &agentdef.CheckSpec{Command: "go build ./... && go test ./..."}
	leader := injectTeamProtocol("p", "lead", "s", agentdef.RoleLeader, true, spec)
	worker := injectTeamProtocol("p", "friday", "s", agentdef.RoleWorker, true, spec)

	for name, prompt := range map[string]string{"leader": leader, "worker": worker} {
		if !strings.Contains(prompt, "## Machine checks at verify time") {
			t.Fatalf("%s prompt lacks the checks section", name)
		}
		if !strings.Contains(prompt, spec.Command) {
			t.Fatalf("%s prompt never names the command", name)
		}
		if !strings.Contains(prompt, "BEFORE `task_done`") {
			t.Fatalf("%s prompt lacks the make-it-pass discipline", name)
		}
	}
	for _, lever := range []string{`verify: "checks"`, `check: "off"`, "overrule"} {
		if !strings.Contains(leader, lever) {
			t.Fatalf("leader prompt lacks the %q lever", lever)
		}
		if strings.Contains(worker, lever) {
			t.Fatalf("worker prompt teaches the leader lever %q", lever)
		}
	}

	// checks off → no trace (the pre-CHK prompt, byte-identical).
	off := injectTeamProtocol("p", "lead", "s", agentdef.RoleLeader, true, nil)
	if strings.Contains(off, "Machine checks") || strings.Contains(off, "verify_checks") {
		t.Fatal("checks-off prompt mentions the feature")
	}
}
