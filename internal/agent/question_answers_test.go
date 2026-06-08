package agent

import (
	"testing"

	"github.com/johnny1110/evva/pkg/ui"
)

// questionAnswers folds the public response (back-compat string Answers + the
// additive MultiAnswers) into the canonical multi-value map.
func TestQuestionAnswersMergesMultiAndSingle(t *testing.T) {
	resp := ui.QuestionResponse{
		Answers:      map[string]string{"q1": "a, b", "q2": "x"},
		MultiAnswers: map[string][]string{"q1": {"a", "b"}},
	}
	got := questionAnswers(resp)

	// q1 is present in MultiAnswers → it wins over the comma-joined Answers.
	if g := got["q1"]; len(g) != 2 || g[0] != "a" || g[1] != "b" {
		t.Fatalf("q1 = %v, want [a b]", got["q1"])
	}
	// q2 only in Answers → becomes a one-element slice.
	if g := got["q2"]; len(g) != 1 || g[0] != "x" {
		t.Fatalf("q2 = %v, want [x]", got["q2"])
	}
}

func TestQuestionAnswersEmpty(t *testing.T) {
	got := questionAnswers(ui.QuestionResponse{})
	if len(got) != 0 {
		t.Fatalf("empty response → %v, want empty map", got)
	}
}
