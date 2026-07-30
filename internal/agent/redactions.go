package agent

import "github.com/johnny1110/evva/pkg/ui"

// redactions.go is the operator's side of secret redaction (SEC-3).
//
// The model gets placeholders; the operator needs the originals back. Not
// out of curiosity — a redactor with no way to inspect it is undebuggable.
// When a rule fires on something that is not a secret, "why did my build
// output turn into [REDACTED:high-entropy:4f2a]" has to have an answer, and
// the answer has to come with enough detail (which rule, what value) to
// write an allowlist entry or disable the rule.
//
// This is the ONLY path by which a masked value re-enters the world, and it
// terminates in the TUI's own renderer. It never touches the session.

// Redactions implements ui.Controller. Subagents report their parent's view
// because they share its redactor — the run has one redaction ledger, not
// one per agent.
func (a *Agent) Redactions() []ui.RedactionInfo {
	fs := a.redactor.Findings()
	if len(fs) == 0 {
		return nil
	}
	out := make([]ui.RedactionInfo, 0, len(fs))
	for _, f := range fs {
		out = append(out, ui.RedactionInfo{
			Placeholder: f.Placeholder,
			RuleID:      f.RuleID,
			Why:         f.Why,
			Value:       f.Value,
			Count:       f.Count,
		})
	}
	return out
}
