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

	config "github.com/johnny1110/evva/configs"
	"github.com/johnny1110/evva/internal/agent"
	"github.com/johnny1110/evva/internal/agent/event"
	"github.com/johnny1110/evva/internal/agent/sysprompt"
	"github.com/johnny1110/evva/internal/llm"
	"github.com/johnny1110/evva/internal/memdir"
	"github.com/johnny1110/evva/internal/tools/fs"
	"github.com/johnny1110/evva/internal/tools/meta"
	"github.com/johnny1110/evva/internal/tools/skill"
	"github.com/johnny1110/evva/internal/tools/task"
	"github.com/johnny1110/evva/internal/ui"
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
	_ = godotenv.Load()
	cfg := config.Get()

	temp := flag.Float64("temp", -1, "sampling temperature (-1 → leave unset)")
	maxTokens := flag.Int("max-tokens", cfg.DefaultMaxTokens, "max output tokens (0 → provider default)")
	maxIters := flag.Int("max-iters", cfg.DefaultMaxIterations, "max loop iterations before pausing for Continue")
	noTUI := flag.Bool("no-tui", false, "disable the bubbletea TUI; read a prompt and run once with plain CLI output")
	uiKind := flag.String("ui", "v2", "TUI implementation: v1 | v2 (v2 is in active development)")
	flag.Parse()

	registry, _ := skill.LoadRegistry(cfg.EvvaHomeSkillsDir, cfg.WorkDirSkillsDir)
	for _, w := range registry.Warnings {
		fmt.Fprintln(os.Stderr, "evva:", w)
	}
	skillRefs := skillRefsFromRegistry(registry)

	// Load project memory (<workdir>/EVVA.md) and user memory
	// (<EVVA_HOME>/USER_PROFILE.md) once at startup; the snapshot threads
	// into the main agent's prompt. Missing files are silent; oversize /
	// permission warnings are surfaced on stderr like skill warnings.
	memSnap := memdir.Load(cfg.WorkDir, cfg.EvvaHome)
	for _, w := range memSnap.Warnings {
		fmt.Fprintln(os.Stderr, "evva:", w)
	}

	prof := agent.Main(cfg, cfg.DefaultProvider, cfg.DefaultModel, skillRefs, memSnap, buildOptions(*temp, *maxTokens))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	useTUI := !*noTUI && isTTY(os.Stdout)
	if useTUI {
		runTUI(ctx, prof, *maxIters, cfg.AppName, cfg.EvvaHome, registry, *uiKind)
		return
	}
	runCLI(ctx, prof, *maxIters, cfg.AppName, registry)
}

// skillRefsFromRegistry flattens the merged skill catalog into the
// sysprompt-facing struct list used by Profile constructors. The sysprompt
// package intentionally does not depend on internal/tools/skill, so the
// caller does the conversion.
func skillRefsFromRegistry(r *skill.Registry) []sysprompt.SkillRef {
	if r == nil {
		return nil
	}
	list := r.List()
	out := make([]sysprompt.SkillRef, 0, len(list))
	for _, m := range list {
		out = append(out, sysprompt.SkillRef{Name: m.Name, Description: m.Description})
	}
	return out
}

// runTUI is the interactive path. The bubbletea UI owns the screen; the
// agent emits events into it; the user drives prompts from the textarea.
// evvaHome lets the TUI resolve banner.txt (and any future user config).
//
// kind selects the TUI implementation: "v1" (default; current reference)
// or "v2" (clean-architecture rewrite, in active development). Both
// satisfy the same ui.UI contract, so the agent-side wiring is
// identical.
func runTUI(ctx context.Context, prof agent.Profile, maxIters int, name, evvaHome string, skills *skill.Registry, kind string) {
	var tui ui.UI
	switch kind {
	default:
		tui = bubbleteav2.New(evvaHome)
	}
	ag, err := agent.New(nil, prof,
		agent.WithName(name),
		agent.WithSink(tui),
		agent.WithMaxIterations(maxIters),
		agent.WithSkillRegistry(skills),
	)
	if err != nil {
		exitf(1, "evva: %v", err)
	}
	tui.Attach(ag)
	if err := tui.Run(ctx); err != nil {
		exitf(1, "evva: %v", err)
	}
}

// runCLI is the headless one-shot path used by `-no-tui` and by pipes.
// Preserves the original behavior: read prompt → run → stream events as
// plain text → exit. ErrIterLimit triggers a synchronous "press Enter to
// continue" prompt on stderr.
func runCLI(ctx context.Context, prof agent.Profile, maxIters int, name string, skills *skill.Registry) {
	prompt, err := readPrompt(flag.Args())
	if err != nil {
		exitf(2, "evva: %v", err)
	}
	if prompt == "" {
		exitf(2, "usage: evva [-temp 0.7] [-max-tokens N] [-max-iters N] [-no-tui] <prompt>")
	}

	ag, err := agent.New(nil, prof,
		agent.WithName(name),
		agent.WithSink(cliSink{out: os.Stdout}),
		agent.WithMaxIterations(maxIters),
		agent.WithSkillRegistry(skills),
	)
	if err != nil {
		exitf(1, "evva: %v", err)
	}

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
			fmt.Fprintf(s.out, "\n[iter-limit] reached %d iterations\n", e.IterLimit.Reached)
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
	case task.Domain:
		if t, ok := p.Payload.(task.Summary); ok {
			fmt.Fprintf(s.out, "[task:%s] %s [%s] %s\n", p.Op, p.ID, t.Status, t.Subject)
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
