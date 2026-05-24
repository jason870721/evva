package overlays

import (
	"encoding/json"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/johnny1110/evva/pkg/event"
	"github.com/johnny1110/evva/pkg/ui"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
)

// approvalChoice is one of the three buttons in the approval prompt.
type approvalChoice int

const (
	choiceAllowOnce approvalChoice = iota
	choiceAllowSession
	choiceDeny
)

func (c approvalChoice) Label() string {
	switch c {
	case choiceAllowOnce:
		return "Allow once"
	case choiceAllowSession:
		return "Allow for this session"
	case choiceDeny:
		return "Deny"
	}
	return ""
}

// ApprovalRespondedMsg is fired after the overlay forwards the user's
// decision to the controller. The app's event handler uses it to pop the
// overlay and (if more approvals are queued) push the next one.
type ApprovalRespondedMsg struct {
	RequestID string
}

// Approval is the modal overlay rendered when the permission gate asks
// for user input. The user picks one of three buttons; on selection the
// overlay calls Controller.RespondPermission and closes itself.
//
// "Deny" optionally accepts a one-line reason — `r` collects it after the
// user hits Enter on the Deny button, then submits.
type Approval struct {
	ctrl ui.Controller

	req event.ApprovalNeededPayload

	sel approvalChoice

	// Once the user has picked Deny we enter the reason-collection mini-
	// state. Empty string is allowed; Enter submits.
	reasonMode bool
	reason     string

	errMsg string
}

// NewApproval builds an overlay for a single ApprovalNeeded event.
// Returns nil if the controller is not yet attached — defensive; the
// app pushes overlays only after Attach.
func NewApproval(ctrl ui.Controller, req event.ApprovalNeededPayload) *Approval {
	if ctrl == nil {
		return nil
	}
	return &Approval{ctrl: ctrl, req: req, sel: choiceAllowOnce}
}

func (a *Approval) Key() string { return "approval:" + a.req.RequestID }
func (a *Approval) Modal() bool { return true }
func (a *Approval) Hint() string {
	if a.reasonMode {
		return "[Enter] submit · [Esc] cancel"
	}
	return "[↑↓] choose · [Enter] confirm · [Esc] deny"
}

// Update consumes keyboard input. Returns close=true once the decision
// has been delivered to the controller; the app pops the overlay and
// the next queued approval (if any) is pushed.
func (a *Approval) Update(msg tea.Msg) (bool, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return false, nil
	}
	if a.reasonMode {
		return a.updateReason(key)
	}

	switch key.String() {
	case "esc":
		return a.respond(ui.PermissionDecision{Behavior: "deny", Reason: "user dismissed prompt"})
	case "ctrl+c":
		// Match the rest of the v2 overlays: Ctrl+C while a modal is open
		// pops it and quits the app. We need to respond first or the
		// blocked tool goroutine hangs. Treat as deny.
		_, cmd := a.respond(ui.PermissionDecision{Behavior: "deny", Reason: "user interrupted"})
		return true, cmd
	case "up", "k":
		if a.sel > choiceAllowOnce {
			a.sel--
		}
		return false, nil
	case "down", "j":
		if a.sel < choiceDeny {
			a.sel++
		}
		return false, nil
	case "1", "a":
		a.sel = choiceAllowOnce
		return a.respond(decisionFor(choiceAllowOnce, a.req, ""))
	case "2", "s":
		a.sel = choiceAllowSession
		return a.respond(decisionFor(choiceAllowSession, a.req, ""))
	case "3", "d":
		a.sel = choiceDeny
		a.reasonMode = true
		return false, nil
	case "enter":
		if a.sel == choiceDeny {
			a.reasonMode = true
			return false, nil
		}
		return a.respond(decisionFor(a.sel, a.req, ""))
	}
	return false, nil
}

// updateReason handles the deny-reason mini-input. Single-line; Enter
// submits, Backspace edits, Esc cancels the deny choice and goes back
// to the button selector.
func (a *Approval) updateReason(key tea.KeyMsg) (bool, tea.Cmd) {
	switch key.String() {
	case "enter":
		return a.respond(decisionFor(choiceDeny, a.req, a.reason))
	case "esc":
		a.reasonMode = false
		a.reason = ""
		return false, nil
	case "backspace":
		if len(a.reason) > 0 {
			a.reason = a.reason[:len(a.reason)-1]
		}
		return false, nil
	}
	if k := key.String(); len(k) == 1 {
		a.reason += k
	}
	return false, nil
}

// respond forwards the decision through the controller and returns
// close=true so the focus stack pops the overlay. The ApprovalRespondedMsg
// gives the app a chance to push the next queued approval (if any).
func (a *Approval) respond(d ui.PermissionDecision) (bool, tea.Cmd) {
	if err := a.ctrl.RespondPermission(a.req.RequestID, d); err != nil {
		a.errMsg = err.Error()
		// Keep the overlay open so the user sees the error. But the
		// goroutine on the other side is already done waiting (probably
		// timed out) — best we can do is let the user dismiss the
		// stale prompt with Esc.
		return false, nil
	}
	id := a.req.RequestID
	return true, func() tea.Msg { return ApprovalRespondedMsg{RequestID: id} }
}

// decisionFor builds the ui.PermissionDecision for a chosen button.
// "Allow for this session" gets an AddRule keyed by the tool's natural
// matcher content (Bash: first token; Read/Write/Edit: file path; others:
// tool-wide). v1 favors safe-by-default — Bash exposing only "git" as the
// session-allow content means the user can't accidentally widen to
// arbitrary commands by clicking "yes."
func decisionFor(c approvalChoice, req event.ApprovalNeededPayload, reason string) ui.PermissionDecision {
	switch c {
	case choiceAllowOnce:
		return ui.PermissionDecision{Behavior: "allow", Reason: "user allowed once"}
	case choiceAllowSession:
		return ui.PermissionDecision{
			Behavior: "allow",
			Reason:   "user allowed for session",
			AddRule:  buildRuleSeed(req),
		}
	case choiceDeny:
		r := strings.TrimSpace(reason)
		if r == "" {
			r = "user denied"
		}
		return ui.PermissionDecision{Behavior: "deny", Reason: r}
	}
	return ui.PermissionDecision{Behavior: "deny", Reason: "unknown choice"}
}

// buildRuleSeed picks a sensible content for a session-allow rule. For
// Bash the first non-env token is used (allowing future "git" calls,
// not arbitrary commands); for file tools the explicit path; for
// everything else, tool-wide.
func buildRuleSeed(req event.ApprovalNeededPayload) *ui.PermissionRuleSeed {
	seed := &ui.PermissionRuleSeed{ToolName: req.ToolName}
	switch req.ToolName {
	case "bash":
		if cmd := readJSONString(req.ToolInput, "command"); cmd != "" {
			seed.Content = firstToken(cmd)
		}
	case "read", "write", "edit", "notebook_edit":
		if p := readJSONString(req.ToolInput, "file_path"); p != "" {
			seed.Content = p
		}
	}
	return seed
}

func firstToken(cmd string) string {
	for _, f := range strings.Fields(cmd) {
		if !strings.Contains(f, "=") {
			return f
		}
	}
	return ""
}

// readJSONString does a minimal scan for a top-level string field. The
// input has already been validated by the LLM provider's tool-call schema
// before reaching the gate, so we trust the shape.
func readJSONString(raw json.RawMessage, field string) string {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	if v, ok := m[field].(string); ok {
		return v
	}
	return ""
}

func (a *Approval) View(width int, th *theme.Theme) string {
	innerWidth := width - 4
	if innerWidth < 40 {
		innerWidth = 40
	}

	var b strings.Builder
	header := "▰ APPROVAL"
	if a.req.PlanContent != "" {
		header = "▰ PLAN APPROVAL"
	}
	b.WriteString(th.PanelHeader.Render(header))
	b.WriteByte('\n')

	b.WriteString(th.StatusKey.Render("tool: "))
	b.WriteString(th.StatusValue.Render(a.req.ToolName))
	b.WriteByte('\n')

	// Model-supplied per-call description. Today only Bash carries this
	// in its input schema ({"description": "..."} alongside command). Any
	// future tool whose input has a top-level `description` string gets
	// the same line for free — extractInputDescription is tool-agnostic.
	if a.req.InputDescription != "" {
		b.WriteString(th.DimText.Render(a.req.InputDescription))
		b.WriteByte('\n')
	}

	b.WriteString(th.StatusKey.Render("mode: "))
	b.WriteString(th.StatusValue.Render(a.req.Mode))
	if a.req.RiskHint != "" {
		b.WriteString(th.StatusKey.Render("  risk: "))
		b.WriteString(riskColor(a.req.RiskHint).Render(a.req.RiskHint))
	}
	if a.req.Matched != "" {
		b.WriteString(th.DimText.Render(" (" + a.req.Matched + ")"))
	}
	b.WriteByte('\n')

	if a.req.Reason != "" {
		b.WriteString(th.DimText.Render("reason: " + a.req.Reason))
		b.WriteByte('\n')
	}

	if a.req.PlanContent != "" {
		b.WriteString("\n")
		b.WriteString(th.StatusKey.Render("plan:"))
		b.WriteByte('\n')
		b.WriteString(renderPlanContent(a.req.PlanContent, innerWidth, th))
		b.WriteByte('\n')
	} else if summary := summarizeInput(a.req.ToolInput); summary != "" {
		b.WriteString("\n")
		b.WriteString(th.DimText.Render("input: "))
		b.WriteString(th.StatusValue.Render(truncateOneLine(summary, innerWidth-9)))
		b.WriteByte('\n')
	}

	b.WriteString("\n")
	sel := lipgloss.NewStyle().Foreground(extractFg(th.ContextFill)).Bold(true)
	dim := th.DimText
	for c := choiceAllowOnce; c <= choiceDeny; c++ {
		marker := "  "
		style := dim
		if c == a.sel {
			marker = "▶ "
			style = sel
		}
		hotkey := []string{"[1]", "[2]", "[3]"}[c]
		b.WriteString(style.Render(marker + hotkey + " " + c.Label()))
		b.WriteByte('\n')
	}

	if a.reasonMode {
		b.WriteString("\n")
		b.WriteString(th.StatusKey.Render("deny reason: "))
		b.WriteString(th.StatusValue.Render(a.reason + "▌"))
		b.WriteByte('\n')
	}

	if a.errMsg != "" {
		b.WriteByte('\n')
		b.WriteString(th.ErrorBanner.Render("✗ " + a.errMsg))
		b.WriteByte('\n')
	}

	b.WriteByte('\n')
	b.WriteString(th.FooterHint.Render(a.Hint()))
	return th.InputBorder.Render(strings.TrimRight(b.String(), "\n"))
}

// renderPlanContent draws the markdown plan body inside the approval
// overlay. v1 keeps the rendering simple — line-wrap to innerWidth, dim
// the body, cap at planPreviewLines so a giant plan doesn't blow out the
// terminal. The user can read the file directly for the full version.
func renderPlanContent(body string, innerWidth int, th *theme.Theme) string {
	const planPreviewLines = 30
	if innerWidth < 20 {
		innerWidth = 20
	}
	lines := strings.Split(strings.TrimSpace(body), "\n")
	var out []string
	for _, ln := range lines {
		out = append(out, wrapPlanLine(ln, innerWidth-2)...)
		if len(out) >= planPreviewLines {
			break
		}
	}
	truncated := false
	if len(out) > planPreviewLines {
		out = out[:planPreviewLines]
		truncated = true
	} else if len(lines) > 0 && len(out) == planPreviewLines {
		// Reached the cap mid-source — flag truncation.
		// Rough check: more raw lines than rendered rows.
		consumed := 0
		for _, ln := range lines {
			consumed += len(wrapPlanLine(ln, innerWidth-2))
			if consumed >= planPreviewLines {
				break
			}
		}
		if consumed > planPreviewLines || len(lines) > planPreviewLines {
			truncated = true
		}
	}
	var b strings.Builder
	for _, ln := range out {
		b.WriteString("  ")
		b.WriteString(th.StatusValue.Render(ln))
		b.WriteByte('\n')
	}
	if truncated {
		b.WriteString(th.DimText.Render("  … plan truncated; full content in the plan file"))
	}
	return strings.TrimRight(b.String(), "\n")
}

func wrapPlanLine(line string, width int) []string {
	if width <= 0 {
		return []string{line}
	}
	if len(line) <= width {
		return []string{line}
	}
	var out []string
	for len(line) > width {
		out = append(out, line[:width])
		line = line[width:]
	}
	if line != "" {
		out = append(out, line)
	}
	return out
}

func riskColor(hint string) lipgloss.Style {
	switch hint {
	case "dangerous":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#FF003C")).Bold(true)
	case "read-only":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#39FF14"))
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("#FAFC4E"))
}

// summarizeInput returns a one-line human summary of the tool's input.
// For Bash this is the command; for file tools the path; for others a
// compacted JSON.
func summarizeInput(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return string(raw)
	}
	if v, ok := m["command"].(string); ok {
		return v
	}
	if v, ok := m["file_path"].(string); ok {
		return v
	}
	if v, ok := m["query"].(string); ok {
		return v
	}
	if v, ok := m["url"].(string); ok {
		return v
	}
	// Fallback: re-marshal as compact one-liner.
	out, err := json.Marshal(m)
	if err != nil {
		return ""
	}
	return string(out)
}

func truncateOneLine(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if max <= 0 || len(s) <= max {
		return s
	}
	if max < 4 {
		return s[:max]
	}
	return s[:max-1] + "…"
}

