package logger

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	config "github.com/johnny1110/evva/pkg/config"
)

// Config holds logger configuration.
// Output is optional — resolved at construction time based on LogDir + AgentID.
type Config struct {
	Level   string
	Format  string
	AgentID string    // required: identifies the agent, used in log filename
	LogDir  string    // optional: if empty, falls back to os.Stdout
	Output  io.Writer // optional: override resolved writer (useful in tests)
}

// New constructs a *slog.Logger.
// Writer resolution priority:
//  1. cfg.Output (explicit override — useful for tests)
//  2. file in cfg.LogDir named "{agentId}+{timestamp}.log"
//  3. os.Stdout
func New(cfg Config) (*slog.Logger, error) {
	writer, err := resolveWriter(cfg)
	if err != nil {
		return nil, fmt.Errorf("logger: resolve writer: %w", err)
	}

	opts := &slog.HandlerOptions{
		Level:     parseLevel(cfg.Level),
		AddSource: true,
	}

	var handler slog.Handler
	if strings.EqualFold(cfg.Format, "json") {
		handler = slog.NewJSONHandler(writer, opts)
	} else {
		handler = newPretty(writer, opts)
	}

	// Bind agentId as a permanent attribute on every log record.
	// slog.Logger.With() returns a child logger that prepends the
	// given attrs to every Handle() call — zero overhead at call sites.
	return slog.New(handler).With("AGENT_ID", cfg.AgentID), nil
}

// OfAgent constructs a logger using the supplied runtime configuration.
// cfg may be nil — the logger falls back to stdout in that case (useful
// for tests).
func OfAgent(cfg *config.Config, parentID, agentID string) (*slog.Logger, error) {
	if cfg == nil {
		return New(Config{AgentID: agentID})
	}

	logDir := ""
	if cfg.LogDir != nil {
		isMain := parentID == ""
		innerDir := parentID
		if isMain {
			innerDir = agentID
		} else {
			agentID = "sub_" + agentID
		}
		logDir = *cfg.LogDir + "/" + innerDir
	}

	return New(Config{
		Level:   cfg.LogLevel,
		Format:  cfg.LogFormat,
		LogDir:  logDir,
		AgentID: agentID,
	})
}

// resolveWriter decides the io.Writer for the logger.
// Separation of concerns: writer resolution is pure I/O policy,
// handler construction is pure formatting policy — keep them apart.
func resolveWriter(cfg Config) (io.Writer, error) {
	// Explicit override wins — supports testing with bytes.Buffer.
	if cfg.Output != nil {
		return cfg.Output, nil
	}

	// No log dir → stdout.
	if cfg.LogDir == "" {
		return os.Stdout, nil
	}

	// MkdirAll is idempotent: no-op if dir already exists,
	// creates the full path (including parents) otherwise.
	if err := os.MkdirAll(cfg.LogDir, 0o755); err != nil {
		return nil, fmt.Errorf("create log dir %q: %w", cfg.LogDir, err)
	}

	filename := buildFilename(cfg.AgentID)
	path := cfg.LogDir + "/" + filename

	// os.O_APPEND is critical: multiple processes with the same agentId
	// (e.g. after a restart) won't truncate the existing log.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open log file %q: %w", path, err)
	}

	// File-only when LOG_DIR is set: stdout is reserved for user-facing output
	// (e.g. the final answer from the CLI) so per-agent log files actually stay
	// separated. Drop LOG_DIR to get noisy stdout-only behavior during dev.
	return f, nil
}

// buildFilename produces "{agentId}+{timestamp}.log".
// UTC timestamp avoids timezone ambiguity across distributed nodes.
// Go's reference time: Mon Jan 2 15:04:05 UTC 2006 → layout "20060102T150405Z"
func buildFilename(agentID string) string {
	return agentID + ".log"
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
