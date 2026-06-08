package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/johnny1110/evva/internal/question"
	"github.com/johnny1110/evva/pkg/mcp"
)

// fakeBroker answers the first question with a fixed label (or returns a
// fixed error), letting us drive mcpPromptViaQuestion deterministically.
type fakeBroker struct {
	answer string
	err    error
}

func (b fakeBroker) Request(_ context.Context, req question.Request) (question.Response, error) {
	if b.err != nil {
		return question.Response{}, b.err
	}
	answers := map[string][]string{}
	if len(req.Questions) > 0 {
		answers[req.Questions[0].Question] = []string{b.answer}
	}
	return question.Response{Answers: answers}, nil
}

func (b fakeBroker) Respond(string, question.Response) error { return nil }

func TestMcpPromptViaQuestion(t *testing.T) {
	prompt := mcp.OAuthPrompt{Server: "github", AuthURL: "https://auth/x"}

	t.Run("im done -> completed", func(t *testing.T) {
		fn := mcpPromptViaQuestion(func() question.Broker { return fakeBroker{answer: "I'm done"} })
		res, err := fn(context.Background(), prompt)
		if err != nil || res != mcp.OAuthCompleted {
			t.Fatalf("got (%v,%v), want (Completed,nil)", res, err)
		}
	})

	t.Run("cancel -> cancelled", func(t *testing.T) {
		fn := mcpPromptViaQuestion(func() question.Broker { return fakeBroker{answer: "Cancel"} })
		res, err := fn(context.Background(), prompt)
		if err != nil || res != mcp.OAuthCancelled {
			t.Fatalf("got (%v,%v), want (Cancelled,nil)", res, err)
		}
	})

	t.Run("broker error propagates", func(t *testing.T) {
		sentinel := errors.New("broker down")
		fn := mcpPromptViaQuestion(func() question.Broker { return fakeBroker{err: sentinel} })
		res, err := fn(context.Background(), prompt)
		if !errors.Is(err, sentinel) || res != mcp.OAuthCancelled {
			t.Fatalf("got (%v,%v), want (Cancelled, sentinel)", res, err)
		}
	})

	t.Run("nil broker -> cancelled with error", func(t *testing.T) {
		fn := mcpPromptViaQuestion(func() question.Broker { return nil })
		res, err := fn(context.Background(), prompt)
		if err == nil || res != mcp.OAuthCancelled {
			t.Fatalf("nil broker should yield (Cancelled, error); got (%v,%v)", res, err)
		}
	})
}
