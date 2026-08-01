package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/johnny1110/evva/internal/memdir"
	"github.com/johnny1110/evva/internal/session"
	"github.com/johnny1110/evva/pkg/agent"
	"github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/evalharness"
	"github.com/johnny1110/evva/pkg/llm"
)

// defaultFixtureDir is where fixtures live by default — repo-relative, so a
// project's behavioral fixtures are version-controlled next to its code the
// same way its unit tests are.
const defaultFixtureDir = "testdata/evalfixtures"

const evalUsage = `evva eval — behavioral regression testing for prompt, tool and model changes.

  evva eval capture <session-id> [flags]   record a past session as a fixture
  evva eval run [flags]                    replay every fixture, score the results
  evva eval list [flags]                   show the fixture set

capture flags:
  -out <path>        fixture file to write (default <fixtures>/<session-id>.json)
  -name <name>       fixture name (default: the output file's base name)
  -desc <text>       what this fixture guards — write one, future you needs it
  -expect <text>     expected outcome in prose; enables judge scoring
  -update <name>     re-baseline an existing fixture after an intended change

run flags:
  -fixtures <dir>    fixture directory (default ` + defaultFixtureDir + `)
  -judge             also score prose expectations with an LLM (advisory)
  -provider <name>   replay against this provider instead of the default
  -model <id>        replay against this model instead of the default
  -persona <name>    replay under this persona

Exits non-zero on any structural divergence, so it drops straight into CI or a
release preflight. Judge results never affect the exit code.
`

// runEval is the `evva eval` entry point.
func runEval(args []string) {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, evalUsage)
		os.Exit(2)
	}
	switch args[0] {
	case "capture":
		evalCapture(args[1:])
	case "run":
		evalRun(args[1:])
	case "list":
		evalList(args[1:])
	case "-h", "--help", "help":
		fmt.Print(evalUsage)
	default:
		fmt.Fprintf(os.Stderr, "evva eval: unknown subcommand %q\n\n%s", args[0], evalUsage)
		os.Exit(2)
	}
}

func evalCapture(args []string) {
	fs := flag.NewFlagSet("eval capture", flag.ExitOnError)
	out := fs.String("out", "", "fixture file to write")
	name := fs.String("name", "", "fixture name")
	desc := fs.String("desc", "", "what this fixture guards")
	expect := fs.String("expect", "", "expected outcome (prose; enables judge scoring)")
	update := fs.String("update", "", "re-baseline an existing fixture by name")
	dir := fs.String("fixtures", defaultFixtureDir, "fixture directory")
	_ = fs.Parse(args)

	cfg := config.Get()

	// --update re-baselines a fixture whose behavior change was intentional.
	// Without this path a fixture that legitimately changed fails forever,
	// and "the eval always fails" is how a gate stops being read.
	if *update != "" {
		path := filepath.Join(*dir, *update+".json")
		f, err := evalharness.LoadFixture(path)
		if err != nil {
			exitf(1, "eval capture: %v", err)
		}
		fmt.Printf("Re-baselining %s against the current configuration…\n", f.Name)
		runner := &agentRunner{cfg: agent.Config{AppConfig: cfg}}
		trace, err := runner.Run(context.Background(), f.UserTurns)
		if err != nil {
			exitf(1, "eval capture: replay failed: %v", err)
		}
		before := len(f.Baseline)
		f.Baseline = trace.ToolCalls
		if err := f.Save(path); err != nil {
			exitf(1, "eval capture: %v", err)
		}
		fmt.Printf("Updated %s: baseline %d → %d call(s).\n", path, before, len(f.Baseline))
		return
	}

	rest := fs.Args()
	if len(rest) == 0 {
		exitf(2, "eval capture: need a session id (list them with `evva eval list -sessions`)")
	}
	sessionID := rest[0]

	slug := memdir.ProjectKey(cfg.WorkDir)
	snap, err := session.Load(cfg.AppHome, slug, sessionID)
	if err != nil {
		exitf(1, "eval capture: load session %s: %v", sessionID, err)
	}

	fixtureName := *name
	if fixtureName == "" && *out != "" {
		fixtureName = strings.TrimSuffix(filepath.Base(*out), ".json")
	}
	if fixtureName == "" {
		fixtureName = sessionID
	}

	description := *desc
	if description == "" {
		description = promptFor("What does this fixture guard? ")
	}

	f, err := evalharness.FromSnapshot(snap, fixtureName, description)
	if err != nil {
		exitf(1, "eval capture: %v", err)
	}
	f.ExpectedOutcome = *expect

	path := *out
	if path == "" {
		path = filepath.Join(*dir, fixtureName+".json")
	}
	if err := f.Save(path); err != nil {
		exitf(1, "eval capture: %v", err)
	}
	fmt.Printf("Wrote %s — %d user turn(s), %d baseline call(s).\n", path, len(f.UserTurns), len(f.Baseline))
	if len(f.Baseline) == 0 {
		fmt.Println("Note: this session made no tool calls, so the structural tier has nothing to compare.")
		fmt.Println("Add -expect \"...\" and run with -judge, or capture a session that exercises tools.")
	}
}

func evalRun(args []string) {
	fs := flag.NewFlagSet("eval run", flag.ExitOnError)
	dir := fs.String("fixtures", defaultFixtureDir, "fixture directory")
	useJudge := fs.Bool("judge", false, "also score prose expectations with an LLM (advisory)")
	provider := fs.String("provider", "", "replay against this provider")
	model := fs.String("model", "", "replay against this model")
	persona := fs.String("persona", "", "replay under this persona")
	_ = fs.Parse(args)

	fixtures, err := evalharness.LoadDir(*dir)
	if err != nil {
		exitf(1, "eval run: %v", err)
	}

	cfg := config.Get()
	acfg := agent.Config{AppConfig: cfg, Provider: *provider, Model: *model, Persona: *persona}

	opt := evalharness.Options{
		Progress: func(name string, i, total int) {
			fmt.Fprintf(os.Stderr, "[%d/%d] %s\n", i+1, total, name)
		},
	}
	if *useJudge {
		client, jerr := buildJudgeClient(cfg, *provider, *model)
		if jerr != nil {
			exitf(1, "eval run: -judge: %v", jerr)
		}
		opt.Judge = evalharness.NewLLMJudge(client)
	}

	rep, err := evalharness.Run(context.Background(), fixtures, &agentRunner{cfg: acfg}, opt)
	if err != nil {
		exitf(1, "eval run: %v", err)
	}
	fmt.Fprintln(os.Stderr)
	evalharness.WriteReport(os.Stdout, rep)
	os.Exit(evalharness.ExitCode(rep))
}

func evalList(args []string) {
	fs := flag.NewFlagSet("eval list", flag.ExitOnError)
	dir := fs.String("fixtures", defaultFixtureDir, "fixture directory")
	sessions := fs.Bool("sessions", false, "list capturable sessions instead of fixtures")
	_ = fs.Parse(args)

	cfg := config.Get()
	if *sessions {
		entries, _, err := session.List(cfg.AppHome, memdir.ProjectKey(cfg.WorkDir))
		if err != nil {
			exitf(1, "eval list: %v", err)
		}
		if len(entries) == 0 {
			fmt.Println("no sessions recorded for this workdir")
			return
		}
		for _, e := range entries {
			if e.Snapshot == nil {
				continue
			}
			fmt.Printf("%-38s %s  %s\n",
				e.Snapshot.SessionID,
				e.Snapshot.UpdatedAt.Format("2006-01-02 15:04"),
				truncateLine(e.Snapshot.FirstUserPrompt, 60))
		}
		return
	}

	fixtures, err := evalharness.LoadDir(*dir)
	if err != nil {
		exitf(1, "eval list: %v", err)
	}
	for _, f := range fixtures {
		fmt.Println(evalharness.FormatFixture(f))
	}
}

// buildJudgeClient resolves a client for the advisory judge tier.
func buildJudgeClient(cfg *config.Config, provider, model string) (llm.Client, error) {
	name := provider
	if name == "" {
		name = string(cfg.DefaultProvider.Name)
	}
	id := model
	if id == "" {
		id = string(cfg.DefaultModel)
	}
	pc, ok := cfg.LLMProviderConfig[name]
	if !ok {
		return nil, fmt.Errorf("no provider config for %q", name)
	}
	return llm.DefaultRegistry().Build(name, id, pc, nil)
}

func promptFor(label string) string {
	fmt.Print(label)
	r := bufio.NewReader(os.Stdin)
	line, _ := r.ReadString('\n')
	return strings.TrimSpace(line)
}

func truncateLine(s string, n int) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
