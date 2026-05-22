package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	config "github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/internal/agent"
	"github.com/johnny1110/evva/pkg/event"
	"github.com/johnny1110/evva/pkg/llm"
	_ "github.com/johnny1110/evva/pkg/llm/builtins" // register anthropic/deepseek/ollama into llm.DefaultRegistry()
	"github.com/johnny1110/evva/internal/memdir"
	"github.com/johnny1110/evva/internal/permission"
	"github.com/johnny1110/evva/internal/question"
	"github.com/johnny1110/evva/pkg/tools/fs"
	"github.com/johnny1110/evva/internal/tools/meta"
	"github.com/johnny1110/evva/pkg/tools/todo"
	"github.com/johnny1110/evva/internal/update"
	"github.com/johnny1110/evva/pkg/ui"
	bubbleteav2 "github.com/johnny1110/evva/internal/ui/bubbletea_v2"
	"github.com/joho/godotenv"
)

// CLI driver for the agent loop.
//
// Two UI modes:
//
//   - Interactive TUI (default, when stdout is a TTY): bubbletea.UI takes
//     over the screen, transcript scrolls, panels collapse when empty,
//     status bar shows tokens + state. The user types prompts in the
//     bottom input.
//
//   - Plain CLI sink (`-no-tui`, or when stdout is piped): the original
//     one-shot flow. Read a prompt from args/stdin, run the agent once,
//     stream events as plain-text lines, exit. Useful for scripting and CI.
func main() {
	// Handle -version / --version before anything else so the agent
	// doesn't need to boot just to print the version.
	for _, a := range os.Args[1:] {
		if a == "-version" || a == "--version" {
			fmt.Println("evva version", config.DisplayVersion())
			return
		}
	}

	// "evva update" — self-update from GitHub Releases, no Go required.
	if len(os.Args) > 1 && os.Args[1] == "update" {
		runUpdate()
		return
	}

	_ = godotenv.Load()
	cfg := config.Get()

	temp := flag.Float64("temp", -1, "sampling temperature (-1 → leave unset)")
	maxTokens := flag.Int("max-tokens", cfg.DefaultMaxTokens, "max output tokens (0 → provider default)")
	maxIters := flag.Int("max-iters", cfg.DefaultMaxIterations, "max loop iterations before pausing for Continue")
	noTUI := flag.Bool("no-tui", false, "disable the bubbletea TUI; read a prompt and run once with plain CLI output")
	uiKind := flag.String("ui", "v2", "TUI implementation: v1 | v2 (v2 is in active development)")
	permModeFlag := flag.String("permission-mode", "", "permission stance: default|accept_edits|plan|bypass (overrides YAML)")
	flag.Parse()

	// Skill registry is auto-loaded inside agent.New from cfg.AppHomeSkillsDir
	// + cfg.WorkDirSkillsDir. Hosts that want a programmatic catalog pre-build
	// one and pass agent.WithSkillRegistry; this binary uses the default.

	// Load project memory (<workdir>/EVVA.md) and user memory
	// (<EVVA_HOME>/USER_PROFILE.md) once at startup; the snapshot threads
	// into the main agent's prompt. Missing files are silent; oversize /
	// permission warnings are surfaced on stderr like skill warnings.
	memSnap := memdir.Load(cfg.WorkDir, cfg.AppHome, cfg.GetEnableAutoMemory())
	for _, w := range memSnap.Warnings {
		fmt.Fprintln(os.Stderr, "evva:", w)
	}
	// First-session notice for auto-memory: no USER_PROFILE.md yet AND the
	// feature is on by default. Quiet thereafter — once the file exists,
	// the user has already seen it (or has opted in by their own writes).
	if cfg.GetEnableAutoMemory() && memSnap.UserProfile == "" {
		if _, err := os.Stat(memdir.UserProfilePath(cfg.AppHome)); errors.Is(err, os.ErrNotExist) {
			fmt.Fprintln(os.Stderr, "evva: auto-memory is enabled — the agent will save persistent notes to USER_PROFILE.md and projects/<key>/MEMORY.md. Disable with /config.")
		}
	}

	// Build the agent registry first: ResolveMainProfile reads from it to
	// pick the right persona (built-in evva or a disk-loaded persona under
	// <EVVA_HOME>/agents/). Bad disk agents degrade gracefully — they're
	// skipped with a warning, the session continues without them.
	agentReg, agentWarns := agent.BuildAgentRegistry(cfg.AppHome)
	for _, w := range agentWarns {
		fmt.Fprintln(os.Stderr, "evva:", w.Error())
	}

	profName := cfg.DefaultProfile
	if profName == "" {
		profName = "evva"
	}
	// nil skills → ResolveMainProfile auto-loads from cfg's skill dirs so
	// the system prompt's # Skills section matches whatever agent.New
	// installs on the toolState.
	prof, profErr := agent.ResolveMainProfile(cfg, agentReg, profName, nil, memSnap, buildOptions(*temp, *maxTokens))
	if profErr != nil {
		fmt.Fprintln(os.Stderr, "evva:", profErr, "— falling back to evva")
		prof, _ = agent.ResolveMainProfile(cfg, agentReg, "evva", nil, memSnap, buildOptions(*temp, *maxTokens))
		profName = "evva"
	}

	// Permission system: load project + user rules, build the approval
	// broker, resolve the active mode (CLI > YAML > "default"). One Store
	// and one Broker per process — subagents inherit them via spawn.go.
	permStore, permWarns := permission.Load(cfg.WorkDir, cfg.AppHome)
	for _, w := range permWarns {
		fmt.Fprintln(os.Stderr, "evva:", w.Error())
	}
	permBroker := permission.NewBroker()
	permMode := resolvePermissionMode(*permModeFlag, cfg.PermissionMode)
	qBroker := question.NewBroker()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	useTUI := !*noTUI && isTTY(os.Stdout)
	if useTUI {
		runTUI(ctx, prof, profName, memSnap, *maxIters, cfg.AppName, cfg.AppHome, agentReg, permStore, permBroker, permMode, qBroker, *uiKind)
		return
	}
	runCLI(ctx, prof, profName, memSnap, *maxIters, cfg.AppName, agentReg, permStore, permBroker, permMode, qBroker)
}

// resolvePermissionMode picks the active mode using CLI > YAML > "default"
// precedence. An unknown value (typo) silently falls back to default —
// matches how -temp / -max-tokens degrade.
func resolvePermissionMode(cliFlag, yamlValue string) permission.Mode {
	for _, candidate := range []string{cliFlag, yamlValue} {
		if candidate == "" {
			continue
		}
		if m, ok := permission.ParseMode(candidate); ok {
			return m
		}
		fmt.Fprintf(os.Stderr, "evva: unknown permission mode %q; falling back to default\n", candidate)
	}
	return permission.ModeDefault
}

// runTUI is the interactive path. The bubbletea UI owns the screen; the
// agent emits events into it; the user drives prompts from the textarea.
// evvaHome lets the TUI resolve banner.txt (and any future user config).
//
// kind selects the TUI implementation: "v1" (default; current reference)
// or "v2" (clean-architecture rewrite, in active development). Both
// satisfy the same ui.UI contract, so the agent-side wiring is
// identical.
func runTUI(ctx context.Context, prof agent.Profile, profName string, memSnap memdir.Snapshot, maxIters int, name, evvaHome string, agents *agent.AgentRegistry, permStore *permission.Store, permBroker permission.Broker, permMode permission.Mode, qBroker question.Broker, kind string) {
	var tui ui.UI
	switch kind {
	default:
		tui = bubbleteav2.New(evvaHome)
	}

	// Register the broker's approval callback to emit KindApprovalNeeded
	// through the TUI sink. The broker keeps the goroutine parked until
	// the TUI calls Broker.Respond with the user's choice.
	permission.SetOnRequest(permBroker, func(req permission.ApprovalRequest) {
		tui.Emit(buildApprovalEvent(req))
	})

	// Register the question broker callback to emit KindQuestionNeeded
	// through the TUI sink. The tool goroutine parks until the TUI calls
	// Controller.RespondQuestion with the user's answers.
	question.SetOnRequest(qBroker, func(req question.Request) {
		tui.Emit(buildQuestionEvent(req))
	})

	ag, err := agent.New(nil, prof,
		agent.WithName(name),
		agent.WithSink(tui),
		agent.WithMaxIterations(maxIters),
		agent.WithAgentRegistry(agents),
		agent.WithPersona(profName),
		agent.WithMemorySnapshot(memSnap),
		agent.WithPermissionStore(permStore),
		agent.WithPermissionBroker(permBroker),
		agent.WithPermissionMode(permMode),
		agent.WithQuestionBroker(qBroker),
		agent.WithRootContext(ctx), // signal pump + bg tasks track the TUI ctx
	)
	if err != nil {
		exitf(1, "evva: %v", err)
	}
	defer ag.Shutdown()
	tui.Attach(ag)
	if err := tui.Run(ctx); err != nil {
		exitf(1, "evva: %v", err)
	}
}

// buildQuestionEvent converts a question.Request into the KindQuestionNeeded
// event the TUI subscribes to.
func buildQuestionEvent(req question.Request) event.Event {
	items := make([]event.QuestionItem, len(req.Questions))
	for i, q := range req.Questions {
		opts := make([]event.QuestionOption, len(q.Options))
		for j, o := range q.Options {
			opts[j] = event.QuestionOption{Label: o.Label, Description: o.Description, Preview: o.Preview}
		}
		items[i] = event.QuestionItem{
			Question:    q.Question,
			Header:      q.Header,
			MultiSelect: q.MultiSelect,
			Options:     opts,
		}
	}
	return event.Event{
		Kind:    event.KindQuestionNeeded,
		AgentID: req.AgentID,
		QuestionNeeded: &event.QuestionNeededPayload{
			RequestID: req.ID,
			AgentID:   req.AgentID,
			Questions: items,
		},
	}
}

// buildApprovalEvent converts a permission.ApprovalRequest into the
// KindApprovalNeeded event the TUI subscribes to.
func buildApprovalEvent(req permission.ApprovalRequest) event.Event {
	riskHint := ""
	switch {
	case req.Hint.IsDangerous:
		riskHint = "dangerous"
	case req.Hint.IsReadOnly:
		riskHint = "read-only"
	}
	return event.Event{
		Kind:    event.KindApprovalNeeded,
		AgentID: req.AgentID,
		ApprovalNeeded: &event.ApprovalNeededPayload{
			RequestID:        req.ID,
			ToolName:         req.ToolName,
			ToolInput:        req.ToolInput,
			InputDescription: req.InputDescription,
			Mode:             string(req.Mode),
			Reason:           req.Reason,
			RiskHint:         riskHint,
			Matched:          req.Hint.Matched,
			PlanContent:      req.PlanContent,
		},
	}
}

// runCLI is the headless one-shot path used by `-no-tui` and by pipes.
// Preserves the original behavior: read prompt → run → stream events as
// plain text → exit. ErrIterLimit triggers a synchronous "press Enter to
// continue" prompt on stderr.
func runCLI(ctx context.Context, prof agent.Profile, profName string, memSnap memdir.Snapshot, maxIters int, name string, agents *agent.AgentRegistry, permStore *permission.Store, permBroker permission.Broker, permMode permission.Mode, qBroker question.Broker) {
	prompt, err := readPrompt(flag.Args())
	if err != nil {
		exitf(2, "evva: %v", err)
	}
	if prompt == "" {
		exitf(2, "usage: evva [-temp 0.7] [-max-tokens N] [-max-iters N] [-no-tui] [-permission-mode default|accept_edits|plan|bypass] <prompt>")
	}

	// CLI mode has no interactive approval or question surface — every Ask
	// becomes a deny with a clear message so scripts fail fast instead of
	// hanging on a phantom prompt.
	permission.SetOnRequest(permBroker, func(req permission.ApprovalRequest) {
		fmt.Fprintf(os.Stderr, "evva: -no-tui denied %s — pass -permission-mode=bypass or add a rule to permissions.json\n", req.ToolName)
		_ = permBroker.Respond(req.ID, permission.Decision{
			Behavior: permission.BehaviorDeny,
			Reason:   "no interactive approval surface in -no-tui mode",
		})
	})
	question.SetOnRequest(qBroker, func(req question.Request) {
		fmt.Fprintf(os.Stderr, "evva: -no-tui cannot display AskUserQuestion — tool call will fail\n")
		_ = qBroker.Respond(req.ID, question.Response{})
	})

	ag, err := agent.New(nil, prof,
		agent.WithName(name),
		agent.WithSink(cliSink{out: os.Stdout}),
		agent.WithMaxIterations(maxIters),
		agent.WithAgentRegistry(agents),
		agent.WithPersona(profName),
		agent.WithMemorySnapshot(memSnap),
		agent.WithPermissionStore(permStore),
		agent.WithPermissionBroker(permBroker),
		agent.WithPermissionMode(permMode),
		agent.WithQuestionBroker(qBroker),
		agent.WithRootContext(ctx),
	)
	if err != nil {
		exitf(1, "evva: %v", err)
	}
	defer ag.Shutdown()

	resp, err := ag.Run(ctx, prompt)
	for errors.Is(err, agent.ErrIterLimit) {
		fmt.Fprint(os.Stderr, "\n[paused at iter limit — press Enter to continue, Ctrl+C to stop] ")
		if !waitEnter(ctx) {
			err = ctx.Err()
			break
		}
		resp, err = ag.Continue(ctx)
	}
	if err != nil {
		if errors.Is(err, llm.ErrInterrupted) {
			fmt.Fprintln(os.Stderr, "\ninterrupted")
			os.Exit(130)
		}
		exitf(1, "evva: %v", err)
	}

	_ = resp
}

// isTTY reports whether f is connected to a terminal. We use stat() because
// adding go-isatty as a direct dep just for this is overkill — the
// ModeCharDevice bit is set on a /dev/tty character device and clear when
// stdout is a pipe / file.
func isTTY(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// --- CLI event sink -------------------------------------------------------

// cliSink writes a human-readable trace of the agent's events to out. It is
// the fallback event.Sink for `-no-tui` mode — a stable, scriptable text
// stream that pipes well into other tools.
type cliSink struct {
	out io.Writer
}

func (s cliSink) Emit(e event.Event) {
	switch e.Kind {
	case event.KindRunStart:
		// stay quiet — the user already typed the prompt
	case event.KindRunResume:
		fmt.Fprintln(s.out, "[resume]")
	case event.KindThinking:
		if e.Thinking != nil {
			fmt.Fprintf(s.out, "\n--- thinking ---\n%s\n", e.Thinking.Text)
		}
	case event.KindText:
		if e.Text != nil && e.Text.Text != "" {
			fmt.Fprintf(s.out, "\n%s\n", e.Text.Text)
		}
	case event.KindToolUseStart:
		if e.ToolUseStart != nil {
			fmt.Fprintf(s.out, "\n→ %s %s\n", e.ToolUseStart.Name, compactJSON(e.ToolUseStart.Input))
		}
	case event.KindToolUseResult:
		if e.ToolUseResult == nil {
			return
		}
		prefix := "✓"
		if e.ToolUseResult.IsError {
			prefix = "✗"
		}
		body := truncate(e.ToolUseResult.Content, 600)
		fmt.Fprintf(s.out, "%s %s\n", prefix, body)
		if diff, ok := e.ToolUseResult.Metadata.(*fs.FileDiff); ok && diff != nil {
			renderFileDiff(s.out, diff)
		}
	case event.KindError:
		if e.Error != nil {
			fmt.Fprintf(s.out, "\n[error:%s] %v\n", e.Error.Stage, e.Error.Err)
		}
	case event.KindIterLimit:
		if e.IterLimit != nil {
			fmt.Fprintf(s.out, "\n[iter-limit] reached %d iterations\n", e.IterLimit.Iters)
		}
	case event.KindRunCancelled:
		fmt.Fprintln(s.out, "\n[cancelled]")
	case event.KindRunEnd:
		// final text already printed via KindText
	case event.KindStoreUpdate:
		if e.StoreUpdate != nil {
			s.printStoreUpdate(e.StoreUpdate)
		}
	case event.KindUsage:
		if e.Usage != nil {
			fmt.Fprintf(s.out, "[tok] in=%d out=%d (cum in=%d out=%d)\n",
				e.Usage.Turn.InputTokens, e.Usage.Turn.OutputTokens,
				e.Usage.Cumulative.InputTokens, e.Usage.Cumulative.OutputTokens,
			)
		}
	case event.KindTurnStart, event.KindTurnEnd:
		// quiet — too chatty for the CLI; the structured log captures these
	}
}

func (s cliSink) printStoreUpdate(p *event.StoreUpdatePayload) {
	switch p.Domain {
	case todo.Domain:
		if list, ok := p.Payload.([]todo.Todo); ok {
			fmt.Fprintf(s.out, "[todo:%s] %d entries\n", p.Op, len(list))
			for i, t := range list {
				fmt.Fprintf(s.out, "  %d. [%s] %s\n", i+1, t.Status, t.Content)
			}
		}
	case meta.SpawnGroupDomain:
		if sn, ok := p.Payload.(meta.SubagentSnapshot); ok {
			fmt.Fprintf(s.out, "[subagent:%s] %s (%s) phase=%s\n", p.Op, sn.ID, sn.Type, sn.Status)
		}
	default:
		fmt.Fprintf(s.out, "[%s:%s] %s\n", p.Domain, p.Op, p.ID)
	}
}

// ANSI escapes for the CLI sink's diff renderer. The TUI uses lipgloss
// styles instead; this is the plain-text fallback for `-no-tui`.
const (
	ansiReset = "\x1b[0m"
	ansiRed   = "\x1b[31m"
	ansiGreen = "\x1b[32m"
	ansiDim   = "\x1b[2m"
)

func renderFileDiff(out io.Writer, d *fs.FileDiff) {
	fmt.Fprintf(out, "%sdiff %s a/%s b/%s%s\n", ansiDim, d.Op, d.Path, d.Path, ansiReset)
	for _, h := range d.Hunks {
		fmt.Fprintf(out, "%s@@ -%d,%d +%d,%d @@%s\n",
			ansiDim, h.OldStart, h.OldCount, h.NewStart, h.NewCount, ansiReset)
		for _, ln := range h.Lines {
			oldCol := blankIfZero(ln.Old)
			newCol := blankIfZero(ln.New)
			switch ln.Kind {
			case fs.LineAdd:
				fmt.Fprintf(out, "%s%4s %4s + %s%s\n", ansiGreen, oldCol, newCol, ln.Text, ansiReset)
			case fs.LineRemove:
				fmt.Fprintf(out, "%s%4s %4s - %s%s\n", ansiRed, oldCol, newCol, ln.Text, ansiReset)
			default:
				fmt.Fprintf(out, "%s%4s %4s   %s%s\n", ansiDim, oldCol, newCol, ln.Text, ansiReset)
			}
		}
	}
}

func blankIfZero(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}

func compactJSON(raw []byte) string {
	if len(raw) == 0 {
		return "{}"
	}
	out := truncate(string(raw), 200)
	out = strings.Join(strings.Fields(out), " ")
	return out
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// --- input plumbing -------------------------------------------------------

func readPrompt(args []string) (string, error) {
	if joined := strings.TrimSpace(strings.Join(args, " ")); joined != "" {
		return joined, nil
	}
	info, err := os.Stdin.Stat()
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeCharDevice != 0 {
		return "", nil
	}
	raw, err := io.ReadAll(bufio.NewReader(os.Stdin))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}

// waitEnter blocks until the user presses Enter on stdin, or ctx is cancelled.
// Returns true if Enter was pressed, false if cancelled.
func waitEnter(ctx context.Context) bool {
	ch := make(chan struct{}, 1)
	go func() {
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		ch <- struct{}{}
	}()
	select {
	case <-ch:
		return true
	case <-ctx.Done():
		return false
	}
}

func buildOptions(temp float64, maxTokens int) []llm.Option {
	var out []llm.Option
	if temp >= 0 {
		out = append(out, llm.WithTemperature(temp))
	}
	if maxTokens > 0 {
		out = append(out, llm.WithMaxTokens(maxTokens))
	}
	return out
}

func exitf(code int, format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(code)
}

// --- self-update -----------------------------------------------------------

func runUpdate() {
	ctx := context.Background()
	current := update.CurrentVersion()

	fmt.Printf("evva %s — checking for updates...\n", current)

	release, err := update.Check(ctx, update.DefaultOwner, update.DefaultRepo)
	if err != nil {
		exitf(1, "evva: update check failed: %v", err)
	}

	if release.Version == "" {
		exitf(1, "evva: no release found on GitHub")
	}

	latest := strings.TrimPrefix(release.Version, "v")
	cur := strings.TrimPrefix(current, "v")

	if latest == cur {
		fmt.Printf("evva is already up-to-date (%s)\n", current)
		return
	}

	fmt.Printf("New version available: %s → %s\n", current, release.Version)
	fmt.Printf("Release: %s\n", release.URL)
	fmt.Print("Update now? [y/N] ")

	var answer string
	fmt.Scanln(&answer)
	if answer != "y" && answer != "Y" {
		fmt.Println("Update cancelled.")
		return
	}

	fmt.Println("Downloading update...")
	exe, err := update.Apply(ctx, release)
	if err != nil {
		exitf(1, "evva: update failed: %v", err)
	}

	fmt.Printf("Updated to %s (%s)\n", release.Version, exe)
}
