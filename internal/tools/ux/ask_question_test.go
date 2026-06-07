package ux

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/johnny1110/evva/internal/question"
)

type fakeBroker struct{ resp question.Response }

func (b fakeBroker) Request(context.Context, question.Request) (question.Response, error) {
	return b.resp, nil
}
func (b fakeBroker) Respond(string, question.Response) error { return nil }

// The tool returns answers as native arrays: multi-select carries every chosen
// label; single-select is a one-element array (FE-6 backend follow-up).
func TestAskQuestionReturnsMultiSelectArrays(t *testing.T) {
	br := fakeBroker{resp: question.Response{
		Answers: map[string][]string{"Pick features?": {"A", "B"}, "Mode?": {"fast"}},
	}}
	tool := NewAskQuestion(func() question.Broker { return br })

	input := []byte(`{"questions":[` +
		`{"question":"Pick features?","header":"feat","multiSelect":true,"options":[{"label":"A","description":"a"},{"label":"B","description":"b"}]},` +
		`{"question":"Mode?","header":"mode","multiSelect":false,"options":[{"label":"fast","description":"f"},{"label":"slow","description":"s"}]}` +
		`]}`)

	res, err := tool.Execute(context.Background(), slog.Default(), input)
	if err != nil || res.IsError {
		t.Fatalf("execute: err=%v isErr=%v content=%s", err, res.IsError, res.Content)
	}

	var out struct {
		Answers map[string][]string `json:"answers"`
	}
	if err := json.Unmarshal([]byte(res.Content), &out); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, res.Content)
	}
	if g := out.Answers["Pick features?"]; len(g) != 2 || g[0] != "A" || g[1] != "B" {
		t.Fatalf("multi-select answers = %v, want [A B]", g)
	}
	if g := out.Answers["Mode?"]; len(g) != 1 || g[0] != "fast" {
		t.Fatalf("single-select answers = %v, want [fast]", g)
	}
}
