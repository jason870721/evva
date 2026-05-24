// Full-host is the canonical example of embedding the *complete* evva
// experience — interactive TUI, persona catalog + /profile switching,
// permission prompts, /resume, /compact, background tasks — in a downstream
// Go app, built on pkg/* alone. It mirrors cmd/evva at a fraction of the
// size: one declarative agent.Config plus two options.
//
// This is a SEPARATE Go module (its own go.mod with a replace pointing at
// the parent). Go's internal-visibility rule therefore makes any
// `internal/` import a compile error — so the fact that this program builds
// is the proof that evva's public pkg/* surface is sufficient to host the
// flagship app. A third party can do exactly this.
//
// Run:
//
//	cd examples/full-host && go run .
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/johnny1110/evva/pkg/agent"
	"github.com/johnny1110/evva/pkg/config"
	_ "github.com/johnny1110/evva/pkg/llm/builtins" // register anthropic/deepseek/ollama
	"github.com/johnny1110/evva/pkg/ui/bubbletea"
)

func main() {
	cfg := config.Get()

	// Bind the agent's lifetime to the process signal context so Ctrl-C tears
	// down the pump and every background worker.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 1. Construct the UI first (it is the agent's event sink).
	tui := bubbletea.New(cfg.AppHome)

	// 2. One call wires everything: persona resolution (evva fallback),
	//    memory + skills, the permission store/mode, and the approval +
	//    question brokers — all from config. WithSink routes approval /
	//    question events to the TUI overlay.
	ag, err := agent.New(agent.Config{AppConfig: cfg},
		agent.WithSink(tui),
		agent.WithRootContext(ctx),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "full-host:", err)
		os.Exit(1)
	}
	defer ag.Shutdown()

	// 3. Hand the UI the controller view of the agent, then run the loop.
	tui.Attach(ag.Controller())
	if err := tui.Run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "full-host:", err)
		os.Exit(1)
	}
}
