// Minimal-host is a ~80 line program showing how to embed evva's agent
// runtime in a downstream Go app:
//
//   - Load runtime config from a custom AppHome (~/.minimal-host/ here).
//   - Register a custom LLM provider against pkg/llm.DefaultRegistry.
//   - Register a custom tool against the agent via WithCustomTool.
//   - Wire a stdout event sink that prints each agent event.
//   - Build the agent via pkg/agent.NewWithProfile and run one prompt.
//
// Run:
//
//	cd examples/minimal-host && go run .
//
// No internal/* imports — this file proves the Phase 13 public surface
// is sufficient on its own. Build it from outside the evva module and
// the compiler enforces that.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"github.com/johnny1110/evva/pkg/agent"
	"github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/event"
	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/tools"
)

// echoClient is a deterministic llm.Client stand-in. Replace with a real
// provider in a production downstream app.
type echoClient struct{ model string }

func (e *echoClient) Name() string  { return "echo" }
func (e *echoClient) Model() string { return e.model }
func (e *echoClient) Complete(_ context.Context, msgs []llm.Message, _ []tools.Tool) (llm.Response, error) {
	last := ""
	if len(msgs) > 0 {
		last = msgs[len(msgs)-1].Content
	}
	return llm.Response{Content: "echo: " + last}, nil
}
func (e *echoClient) Stream(ctx context.Context, msgs []llm.Message, ts []tools.Tool, _ llm.ChunkSink) (llm.Response, error) {
	return e.Complete(ctx, msgs, ts)
}
func (*echoClient) Apply(...llm.Option) {}

// pingTool answers any input with "pong". Custom tools satisfy the same
// pkg/tools.Tool interface evva's built-ins do.
type pingTool struct{}

func (pingTool) Name() string            { return "ping" }
func (pingTool) Description() string     { return "respond with pong" }
func (pingTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (pingTool) Execute(_ context.Context, _ *slog.Logger, _ json.RawMessage) (tools.Result, error) {
	return tools.Result{Content: "pong"}, nil
}

// stdoutSink prints each agent event in a compact one-line format.
type stdoutSink struct{}

func (stdoutSink) Emit(e event.Event) {
	switch e.Kind {
	case event.KindText:
		if e.Text != nil {
			fmt.Println("→ text:", e.Text.Text)
		}
	case event.KindRunEnd:
		if e.RunEnd != nil {
			fmt.Printf("→ run_end (iters=%d)\n", e.RunEnd.Iters)
		}
	}
}

func main() {
	// 1. Custom AppHome: writes config to a per-user dir of the host's
	//    choosing rather than the bundled ~/.evva/.
	home, _ := os.UserHomeDir()
	cfg, err := config.Load(config.LoadOptions{
		AppName: "minimal-host",
		AppHome: home + "/.minimal-host",
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "config.Load:", err)
		os.Exit(1)
	}

	// 2. Register the custom provider. Use a unique name; "echo" is the
	//    convention for "doesn't talk to a real backend."
	if !llm.DefaultRegistry().Has("echo") {
		_ = llm.DefaultRegistry().Register("echo", func(_ llm.APIConfig, model string, _ ...llm.Option) (llm.Client, error) {
			return &echoClient{model: model}, nil
		})
	}
	cfg.LLMProviderConfig["echo"] = config.APIConfig{ApiURL: "http://example", ApiSecret: "n/a"}

	// 3. Build a Profile pinned to the echo provider. The agent will
	//    expose the ping tool we register via WithCustomTool below.
	prof, err := agent.NewProfile("minimal", "you are an echo bot",
		nil, // ActiveTools — ping is added via WithCustomTool
		"echo", "echo-v1",
		agent.ProfileOptions{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "NewProfile:", err)
		os.Exit(1)
	}

	// 4. Build the agent with the public option API.
	ag, err := agent.NewWithProfile(prof,
		agent.WithConfig(cfg),
		agent.WithSink(stdoutSink{}),
		agent.WithMaxIterations(5),
		agent.WithCustomTool("ping", func(tools.State) (tools.Tool, error) {
			return pingTool{}, nil
		}),
		agent.WithPermissionMode("bypass"),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "NewWithProfile:", err)
		os.Exit(1)
	}

	// 5. Run one turn.
	resp, err := ag.Run(context.Background(), "hello, world")
	if err != nil {
		fmt.Fprintln(os.Stderr, "Run:", err)
		os.Exit(1)
	}
	fmt.Println("final:", resp)
}
