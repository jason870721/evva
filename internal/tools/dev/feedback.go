package dev

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/johnny1110/evva/internal/tools"
	"github.com/johnny1110/evva/pkg/config"
)

// FeedbackTool lets evva report bugs, suggest improvements, flag unnecessary
// tool-result patterns, or request new tools. Only available in dev environment.
//
// Writes feedback markdown into <AppHome>/feedbacks/. cfg.AppHome is
// captured at construction so multi-agent processes targeting different
// home dirs route feedback correctly.
type FeedbackTool struct {
	cfg *config.Config
}

// NewFeedback builds a feedback tool bound to cfg. cfg may be nil — Execute
// surfaces a clear error in that case.
func NewFeedback(cfg *config.Config) *FeedbackTool {
	return &FeedbackTool{cfg: cfg}
}

const feedbackDesc = "Send feedback to the evva developers. " +
	"Choose a category: " +
	`"bug" for a tool or behavior that is broken, ` +
	`"improvement" for something that works but could be better, ` +
	`"unnecessary-result" for a tool result that was confusing or wasted tokens, ` +
	`"new-tool" for a tool you wish existed.`

func (t *FeedbackTool) Name() string { return string(tools.FEEDBACK) }

func (t *FeedbackTool) Description() string { return feedbackDesc }

func (t *FeedbackTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"required":["feedback","category"],
		"properties":{
			"category":{"type":"string","enum":["bug","improvement","unnecessary-result","new-tool"],"description":"Type of feedback"},
			"feedback":{"type":"string","description":"feedback content to evva developers (markdown format)"}
		}
	}`)
}

type feedbackInput struct {
	Category string `json:"category"`
	Feedback string `json:"feedback"`
}

func (t *FeedbackTool) Execute(_ context.Context, logger *slog.Logger, input json.RawMessage) (tools.Result, error) {
	var in feedbackInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("feedback: bad input: %v", err)}, nil
	}
	logger.Debug("feedback.dispatch", "category", in.Category)

	if t.cfg == nil {
		return tools.Result{IsError: true, Content: "feedback: no config installed"}, nil
	}
	dir := filepath.Join(t.cfg.AppHome, "feedbacks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		logger.Warn("feedback.fail", "stage", "mkdir", "dir", dir, "err", err)
		return tools.Result{IsError: true, Content: fmt.Sprintf("feedback: cannot create directory: %v", err)}, nil
	}

	ts := time.Now().Format("2006-01-02T150405")
	filename := fmt.Sprintf("feedback_%s_%s.md", in.Category, ts)
	path := filepath.Join(dir, filename)

	body := fmt.Sprintf("> category: %s\n\n%s", in.Category, in.Feedback)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		logger.Warn("feedback.fail", "stage", "write", "path", path, "err", err)
		return tools.Result{IsError: true, Content: fmt.Sprintf("feedback: cannot write file: %v", err)}, nil
	}

	return tools.Result{Content: fmt.Sprintf("Feedback saved to %s.", path)}, nil
}
