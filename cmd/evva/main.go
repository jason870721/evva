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
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/johnny1110/evva/pkg/agent"
	config "github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/event"
	"github.com/johnny1110/evva/pkg/llm"
	_ "github.com/johnny1110/evva/pkg/llm/builtins" // register anthropic/deepseek/ollama into llm.DefaultRegistry()
	"github.com/johnny1110/evva/pkg/tools/daemon"
	"github.com/johnny1110/evva/pkg/tools/fs"
	"github.com/johnny1110/evva/pkg/tools/todo"
	"github.com/johnny1110/evva/pkg/ui"
	_ "github.com/johnny1110/evva/pkg/ui/bubbletea" // register the "bubbletea" UI into ui's registry
	_ "github.com/johnny1110/evva/pkg/ui/lp"        // register the "lp" (low-profile) UI
	"github.com/johnny1110/evva/pkg/update"
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
	noTUI := flag.Bool("no-tui", false, "disable the interactive TUI; read a prompt and run once with plain CLI output")
	tuiName := flag.String("tui", "bubbletea", "interactive UI to use (available: "+strings.Join(ui.Names(), ", ")+")")
	permModeFlag := flag.String("permission-mode", "", "permission stance: default|accept_edits|plan|bypass (overrides YAML)")
	flag.Parse()

	// First-session notice for auto-memory: no MEMORY.md index yet. Quiet
	// thereafter — once the index exists the user has seen it (or opted in by
	// their own writes). agent.New ensures the memory dir, loads EVVA.md + the
	// index into the prompt itself, and logs any load warnings, so the host no
	// longer reads the memory files directly.
	if cfg.GetEnableAutoMemory() {
		memDir := filepath.Join(cfg.AppHome, "memory")
		if _, err := os.Stat(filepath.Join(memDir, "MEMORY.md")); errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "evva: auto-memory is enabled — the agent saves persistent, typed notes under %s and maintains a MEMORY.md index there. Disable with /config.\n", memDir)
		}
	}

	// One declarative Config drives the whole bootstrap: agent.New resolves
	// the persona from cfg.DefaultProfile (evva fallback), builds the persona
	// registry from <AppHome>/agents/, auto-loads memory + skills, loads the
	// permission store, resolves the mode (CLI flag > YAML > default), and
	// owns the approval + question brokers. Skills/memory/registry load
	// warnings surface on the agent's logger.
	acfg := agent.Config{
		AppConfig:      cfg,
		PermissionMode: *permModeFlag,
		MaxIters:       *maxIters,
		LLMOptions:     buildOptions(*temp, *maxTokens),
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	useTUI := !*noTUI && isTTY(os.Stdout)
	if useTUI {
		runTUI(ctx, acfg, cfg.AppHome, *tuiName)
		return
	}
	runCLI(ctx, acfg)
}

// runTUI is the interactive path. The selected UI owns the screen; the
// agent emits events into it; the user drives prompts from the textarea.
// evvaHome lets the UI resolve banner.txt (and any future user config).
//
// tuiName picks the UI from ui's registry (default "bubbletea"); built-in
// and third-party UIs register themselves via a blank import. An unknown
// name is a clean exit, not a panic.
//
// With a sink installed the agent emits KindApprovalNeeded /
// KindQuestionNeeded to the UI, which renders the overlay and resolves via
// Controller.RespondPermission / RespondQuestion — no host broker wiring.
func runTUI(ctx context.Context, acfg agent.Config, evvaHome, tuiName string) {
	factory, ok := ui.Lookup(tuiName)
	if !ok {
		exitf(2, "evva: unknown -tui %q (available: %s)", tuiName, strings.Join(ui.Names(), ", "))
	}
	tui := factory(evvaHome)

	ag, err := agent.New(acfg,
		agent.WithSink(tui),
		agent.WithRootContext(ctx), // signal pump + bg tasks track the TUI ctx
	)
	if err != nil {
		exitf(1, "evva: %v", err)
	}
	defer ag.Shutdown()
	tui.Attach(ag.Controller())
	if err := tui.Run(ctx); err != nil {
		exitf(1, "evva: %v", err)
	}
}

// runCLI is the headless one-shot path used by `-no-tui` and by pipes.
// Preserves the original behavior: read prompt → run → stream events as
// plain text → exit. ErrIterLimit triggers a synchronous "press Enter to
// continue" prompt on stderr.
func runCLI(ctx context.Context, acfg agent.Config) {
	prompt, err := readPrompt(flag.Args())
	if err != nil {
		exitf(2, "evva: %v", err)
	}
	if prompt == "" {
		exitf(2, "usage: evva [-temp 0.7] [-max-tokens N] [-max-iters N] [-no-tui] [-permission-mode default|accept_edits|plan|bypass] <prompt>")
	}

	// CLI mode has no interactive surface. The agent emits approval /
	// question requests to the sink; cliSink resolves them headlessly
	// (deny / empty) through the Controller so scripts fail fast instead
	// of hanging on a phantom prompt.
	sink := &cliSink{out: os.Stdout}
	ag, err := agent.New(acfg,
		agent.WithSink(sink),
		agent.WithRootContext(ctx),
	)
	if err != nil {
		exitf(1, "evva: %v", err)
	}
	sink.ctrl = ag.Controller()
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
//
// ctrl is the agent, set right after construction. Because `-no-tui` has no
// interactive surface, the sink resolves the agent's approval / question
// prompts itself (deny / empty) through the Controller — the broker's reply
// channel is buffered, so responding inside Emit doesn't deadlock the parked
// tool goroutine.
type cliSink struct {
	out  io.Writer
	ctrl ui.Controller
}

func (s *cliSink) Emit(e event.Event) {
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
	case event.KindApprovalNeeded:
		if e.ApprovalNeeded != nil && s.ctrl != nil {
			fmt.Fprintf(os.Stderr, "evva: -no-tui denied %s — pass -permission-mode=bypass or add a rule to permissions.json\n", e.ApprovalNeeded.ToolName)
			_ = s.ctrl.RespondPermission(e.ApprovalNeeded.RequestID, ui.PermissionDecision{
				Behavior: "deny",
				Reason:   "no interactive approval surface in -no-tui mode",
			})
		}
	case event.KindQuestionNeeded:
		if e.QuestionNeeded != nil && s.ctrl != nil {
			fmt.Fprintln(os.Stderr, "evva: -no-tui cannot display AskUserQuestion — tool call will fail")
			_ = s.ctrl.RespondQuestion(e.QuestionNeeded.RequestID, ui.QuestionResponse{})
		}
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

func (s *cliSink) printStoreUpdate(p *event.StoreUpdatePayload) {
	switch p.Domain {
	case todo.Domain:
		if list, ok := p.Payload.([]todo.Todo); ok {
			fmt.Fprintf(s.out, "[todo:%s] %d entries\n", p.Op, len(list))
			for i, t := range list {
				fmt.Fprintf(s.out, "  %d. [%s] %s\n", i+1, t.Status, t.Content)
			}
		}
	case daemon.Domain:
		if sn, ok := p.Payload.(daemon.DaemonSnapshot); ok {
			fmt.Fprintf(s.out, "[daemon:%s] %s [%s/%s] %s\n", p.Op, sn.ID, sn.Kind, sn.Status, sn.Description)
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
