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
	"strings"
	"syscall"

	config "github.com/johnny1110/evva/configs"
	"github.com/johnny1110/evva/internal/agent"
	"github.com/johnny1110/evva/internal/agent/profiles"
	"github.com/johnny1110/evva/internal/llm"
	"github.com/joho/godotenv"
)

// LLM smoke-test CLI.
//
// Usage:
//
//	evva [-provider deepseek|claude|ollama] [-model id] [-system "..."] [-temp 0.7] [prompt ...]
//
// If no positional prompt is given, the program reads from stdin so you can
// pipe a file or a heredoc. Ctrl+C cancels the in-flight call and exits with
// code 130 — the same cancellation path the TUI will drive with ESC.
func main() {
	_ = godotenv.Load()

	provider := flag.String("provider", "deepseek", "LLM provider: claude | deepseek | ollama")
	model := flag.String("model", "", "model id (empty → provider default)")
	profile := flag.String("profile", "main", "agent profile: main | explore | general")
	system := flag.String("system", "", "override the profile's system prompt")
	temp := flag.Float64("temp", -1, "sampling temperature (-1 → leave unset)")
	maxTokens := flag.Int("max-tokens", 1024, "max output tokens (0 → provider default)")
	flag.Parse()

	prompt, err := readPrompt(flag.Args())
	if err != nil {
		exitf(2, "evva: %v", err)
	}
	if prompt == "" {
		exitf(2, "usage: evva [-provider X] [-model Y] [-system ...] [-temp 0.7] <prompt>")
	}

	cfg := config.Get()
	client, err := buildClient(cfg, *provider, *model, buildOptions(*temp, *maxTokens)...)
	if err != nil {
		exitf(1, "evva: %v", err)
	}

	prof, err := pickProfile(*profile)
	if err != nil {
		exitf(2, "evva: %v", err)
	}
	if *system != "" {
		prof.SystemPrompt = *system
	}

	// Ctrl+C / SIGTERM cancels ctx, which the client converts to llm.ErrInterrupted.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ag, err := agent.New(client, prof)
	if err != nil {
		exitf(1, "evva: %v", err)
	}
	resp, err := ag.Send(ctx, prompt)
	if err != nil {
		if errors.Is(err, llm.ErrInterrupted) {
			fmt.Fprintln(os.Stderr, "interrupted")
			os.Exit(130)
		}
		exitf(1, "evva: %v", err)
	}

	if resp.Thinking != "" {
		fmt.Println("=== thinking ===")
		fmt.Println(resp.Thinking)
		fmt.Println("=== answer ===")
	}
	fmt.Println(resp.Content)
}

// readPrompt joins positional args, or falls back to stdin if none were given
// and stdin is a pipe / file. Returns "" only when both sources are empty.
func readPrompt(args []string) (string, error) {
	if joined := strings.TrimSpace(strings.Join(args, " ")); joined != "" {
		return joined, nil
	}
	info, err := os.Stdin.Stat()
	if err != nil {
		return "", err
	}
	// No piped input — don't block on the terminal.
	if info.Mode()&os.ModeCharDevice != 0 {
		return "", nil
	}
	raw, err := io.ReadAll(bufio.NewReader(os.Stdin))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
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

func pickProfile(name string) (agent.Profile, error) {
	switch strings.ToLower(name) {
	case "main":
		return profiles.Main(), nil
	case "explore":
		return profiles.Explore(), nil
	case "general":
		return profiles.General(), nil
	default:
		return agent.Profile{}, fmt.Errorf("unknown profile %q (want main | explore | general)", name)
	}
}

func exitf(code int, format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(code)
}
