package swarm

import (
	"testing"

	"github.com/johnny1110/evva/internal/swarm/agentdef"
)

func TestTeamProtocolSuffix_MatchesInject(t *testing.T) {
	for _, role := range []agentdef.Role{agentdef.RoleLeader, agentdef.RoleWorker} {
		for _, canWrite := range []bool{true, false} {
			for _, checks := range []*agentdef.CheckSpec{nil, {Command: "go test ./..."}} {
				suffix := teamProtocolSuffix("alice", "team", role, canWrite, checks, worktreeGrounding{})
				full := injectTeamProtocol("PERSONA BODY", "alice", "team", role, canWrite, checks, worktreeGrounding{})
				if want := "PERSONA BODY\n\n" + suffix; full != want {
					t.Fatalf("inject(role=%s,write=%v) must be body + suffix", role, canWrite)
				}
				if got := injectTeamProtocol("", "alice", "team", role, canWrite, checks, worktreeGrounding{}); got != suffix {
					t.Fatalf("empty persona must yield the bare suffix")
				}
			}
		}
	}
}
