package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/johnny1110/evva/internal/toolset"
	"github.com/johnny1110/evva/pkg/agent"
	"github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/mcp"
	"github.com/johnny1110/evva/pkg/tools"
	pubtoolset "github.com/johnny1110/evva/pkg/toolset"
)

// mcpserve.go is `evva mcp-serve` (MCP-3): the inbound direction of evva's
// MCP integration, where an external client — Claude Desktop, an IDE, another
// evva, an A2A-aware orchestrator — calls into this installation.
//
// It mirrors service.go's subcommand shape but not its daemon lifecycle: a
// stdio server IS the foreground process (its parent launched it precisely to
// own its lifetime), so there is nothing to daemonize.

// runMCPServe dispatches `evva mcp-serve [--transport stdio]`.
//
// Exposure is governed entirely by the "mcpServe" block in settings.json —
// nothing is exposed by default, and an unknown tool or persona name in the
// allowlist stops startup rather than surfacing later as a confusing "unknown
// tool" to whoever connects.
//
// Diagnostics go to stderr, never stdout: on the stdio transport stdout IS the
// JSON-RPC channel, and one stray line corrupts the stream.
func runMCPServe(args []string) {
	fs := flag.NewFlagSet("mcp-serve", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	transport := fs.String("transport", "stdio", "transport to serve on: stdio")
	verbose := fs.Bool("v", false, "log tool calls to stderr")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if rest := fs.Args(); len(rest) > 0 {
		exitf(2, "evva mcp-serve: unexpected argument %q", rest[0])
	}

	cfg := config.Get()

	level := slog.LevelWarn
	if *verbose {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	serveCfg, warns := mcp.LoadServeConfig(cfg.WorkDir, cfg.AppHome)
	spawner, regWarns := agent.NewPersonaSpawner(agent.Config{AppConfig: cfg})
	for _, w := range warns {
		fmt.Fprintf(os.Stderr, "evva mcp-serve: warning: %v\n", w)
	}
	for _, w := range regWarns {
		fmt.Fprintf(os.Stderr, "evva mcp-serve: warning: %v\n", w)
	}

	srv, err := mcp.BuildServer(mcp.ServeOptions{
		Expose:   serveCfg.Expose,
		Spawner:  spawner,
		Provider: mcpToolProvider(cfg),
		Timeout:  serveCfg.Timeout,
		Version:  config.DisplayVersion(),
		Logger:   logger,
	})
	if err != nil {
		exitf(2, "evva mcp-serve: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	for _, e := range serveCfg.Expose {
		fmt.Fprintf(os.Stderr, "evva mcp-serve: exposing %s\n", e)
	}

	switch *transport {
	case "stdio":
		fmt.Fprintln(os.Stderr, "evva mcp-serve: serving on stdio")
		// ctx.Err() suppresses the error a clean Ctrl-C produces: the signal
		// is the intended way to stop a stdio server, not a failure.
		if err := srv.Run(ctx, &mcpsdk.StdioTransport{}); err != nil && ctx.Err() == nil {
			exitf(1, "evva mcp-serve: %v", err)
		}
	default:
		exitf(2, "evva mcp-serve: unknown --transport %q (want stdio)", *transport)
	}
}

// mcpToolProvider builds one exposed tool by name from evva's own registry.
//
// This closure is why pkg/mcp takes a ToolProvider callback rather than
// resolving tools itself: the built-in factories type-assert
// *internal/toolset.ToolState, so only a caller inside the runtime can build
// them. The ToolState here is deliberately bare — no daemon host, no subagent
// spawner, no checkpoint sink — which is sound because pkg/mcp only allows
// read-oriented tools to be exposed directly, and those read config + workdir
// and nothing else. A factory needing more will panic on the type assertion at
// startup, which is the right time to find out.
func mcpToolProvider(cfg *config.Config) mcp.ToolProvider {
	ts := toolset.NewToolState()
	ts.SetConfig(cfg)
	return func(name string) (tools.Tool, error) {
		return pubtoolset.DefaultRegistry().Build(tools.ToolName(name), ts)
	}
}
