package ux

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/johnny1110/evva/internal/question"
	"github.com/johnny1110/evva/pkg/tools"
)

// brokerLookup returns the currently-installed question broker. Late-bound so
// the tool can be built before the agent wires its broker into ToolState.
type brokerLookup func() question.Broker

// AskQuestionTool implements the AskUserQuestion tool. It blocks the agent
// goroutine until the user answers all questions via the TUI overlay, then
// returns the answers as a JSON result.
type AskQuestionTool struct {
	lookup brokerLookup
}

// NewAskQuestion builds the tool with a late-bound broker lookup. The standard
// call site is: ux.NewAskQuestion(toolState.QuestionBroker).
func NewAskQuestion(lookup brokerLookup) *AskQuestionTool {
	return &AskQuestionTool{lookup: lookup}
}

func (t *AskQuestionTool) Name() string        { return string(tools.ASK_USER_QUESTION) }
func (t *AskQuestionTool) Description() string { return askQuestionDescription }
func (t *AskQuestionTool) Schema() json.RawMessage {
	return json.RawMessage(askQuestionSchema)
}

// questionInput is what the LLM sends.
type questionInput struct {
	Questions []questionItem `json:"questions"`
	Metadata  *struct {
		Source string `json:"source,omitempty"`
	} `json:"metadata,omitempty"`
}

type questionItem struct {
	Question    string         `json:"question"`
	Header      string         `json:"header"`
	MultiSelect bool           `json:"multiSelect"`
	Options     []questionOpt  `json:"options"`
}

type questionOpt struct {
	Label       string `json:"label"`
	Description string `json:"description"`
	Preview     string `json:"preview,omitempty"`
}

// questionOutput is what the tool returns to the LLM. Answers maps question text
// → the chosen option labels (one entry per selection; single-select yields a
// one-element slice; "Other" carries the typed text) — the native multi-select
// form.
type questionOutput struct {
	Questions   []questionItem            `json:"questions"`
	Answers     map[string][]string       `json:"answers"`
	Annotations map[string]annotationItem `json:"annotations,omitempty"`
}

type annotationItem struct {
	Notes   string `json:"notes,omitempty"`
	Preview string `json:"preview,omitempty"`
}

func (t *AskQuestionTool) Execute(ctx context.Context, logger *slog.Logger, input json.RawMessage) (tools.Result, error) {
	var inp questionInput
	if err := json.Unmarshal(input, &inp); err != nil {
		return tools.Result{IsError: true, Content: "ask_user_question: invalid input: " + err.Error()}, nil
	}

	if err := validateInput(inp); err != nil {
		return tools.Result{IsError: true, Content: "ask_user_question: " + err.Error()}, nil
	}

	b := t.lookup()
	if b == nil {
		return tools.Result{IsError: true, Content: "ask_user_question: no question broker installed (TUI not running?)"}, nil
	}

	req := question.Request{}
	for _, qi := range inp.Questions {
		opts := make([]question.Option, len(qi.Options))
		for i, o := range qi.Options {
			opts[i] = question.Option{Label: o.Label, Description: o.Description, Preview: o.Preview}
		}
		req.Questions = append(req.Questions, question.Question{
			Question:    qi.Question,
			Header:      qi.Header,
			MultiSelect: qi.MultiSelect,
			Options:     opts,
		})
	}

	resp, err := b.Request(ctx, req)
	if err != nil {
		return tools.Result{IsError: true, Content: "ask_user_question: cancelled: " + err.Error()}, nil
	}

	out := questionOutput{
		Questions:   inp.Questions,
		Answers:     resp.Answers,
	}
	if len(resp.Annotations) > 0 {
		out.Annotations = make(map[string]annotationItem, len(resp.Annotations))
		for k, v := range resp.Annotations {
			out.Annotations[k] = annotationItem{Notes: v.Notes, Preview: v.Preview}
		}
	}

	raw, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return tools.Result{IsError: true, Content: "ask_user_question: marshal error: " + err.Error()}, nil
	}
	return tools.Result{Content: string(raw)}, nil
}

func validateInput(inp questionInput) error {
	if len(inp.Questions) == 0 {
		return errors.New("questions must have at least 1 item")
	}
	if len(inp.Questions) > 4 {
		return errors.New("questions must have at most 4 items")
	}
	seen := make(map[string]bool, len(inp.Questions))
	for _, q := range inp.Questions {
		if seen[q.Question] {
			return fmt.Errorf("duplicate question text: %q", q.Question)
		}
		seen[q.Question] = true
		if len(q.Options) < 2 {
			return fmt.Errorf("question %q must have at least 2 options", q.Question)
		}
		if len(q.Options) > 4 {
			return fmt.Errorf("question %q must have at most 4 options", q.Question)
		}
		optSeen := make(map[string]bool, len(q.Options))
		for _, o := range q.Options {
			label := strings.TrimSpace(o.Label)
			if optSeen[label] {
				return fmt.Errorf("question %q has duplicate option label: %q", q.Question, label)
			}
			optSeen[label] = true
		}
	}
	return nil
}

const askQuestionDescription = `Asks the user multiple choice questions to gather information, clarify ambiguity, understand preferences, make decisions or offer them choices.

Use this tool when you need to ask the user questions during execution. This allows you to:
1. Gather user preferences or requirements
2. Clarify ambiguous instructions
3. Get decisions on implementation choices as you work
4. Offer choices to the user about what direction to take.

Usage notes:
- Users will always be able to select "Other" to provide custom text input
- Use multiSelect: true to allow multiple answers to be selected for a question
- If you recommend a specific option, make that the first option in the list and add "(Recommended)" at the end of the label

Plan mode note: In plan mode, use this tool to clarify requirements or choose between approaches BEFORE finalizing your plan. Do NOT use this tool to ask "Is my plan ready?" or "Should I proceed?" - use exit_plan_mode for plan approval. IMPORTANT: Do not reference "the plan" in your questions (e.g., "Do you have feedback about the plan?", "Does the plan look good?") because the user cannot see the plan in the UI until you call exit_plan_mode. If you need plan approval, use exit_plan_mode instead.

Preview feature:
Use the optional ` + "`" + `preview` + "`" + ` field on options when presenting concrete artifacts that users need to visually compare:
- ASCII mockups of UI layouts or components
- Code snippets showing different implementations
- Diagram variations
- Configuration examples

Preview content is rendered as markdown in a monospace box. Multi-line text with newlines is supported. When any option has a preview, the UI switches to a side-by-side layout with a vertical option list on the left and preview on the right. Do not use previews for simple preference questions where labels and descriptions suffice. Note: previews are only supported for single-select questions (not multiSelect).`

const askQuestionSchema = `{
	"type": "object",
	"additionalProperties": false,
	"required": ["questions"],
	"properties": {
		"questions": {
			"type": "array",
			"minItems": 1,
			"maxItems": 4,
			"description": "Questions to ask the user (1-4 questions)",
			"items": {
				"type": "object",
				"additionalProperties": false,
				"required": ["question", "header", "options", "multiSelect"],
				"properties": {
					"question": {"type": "string", "description": "The complete question to ask the user. Should be clear, specific, and end with a question mark. Example: \"Which library should we use for date formatting?\" If multiSelect is true, phrase it accordingly, e.g. \"Which features do you want to enable?\""},
					"header": {"type": "string", "description": "Very short label displayed as a chip/tag (max 12 chars). Examples: \"Auth method\", \"Library\", \"Approach\"."},
					"multiSelect": {"type": "boolean", "default": false, "description": "Set to true to allow the user to select multiple options instead of just one. Use when choices are not mutually exclusive."},
					"options": {
						"type": "array",
						"minItems": 2,
						"maxItems": 4,
						"description": "The available choices for this question. Must have 2-4 options. Each option should be a distinct, mutually exclusive choice (unless multiSelect is enabled). There should be no 'Other' option, that will be provided automatically.",
						"items": {
							"type": "object",
							"additionalProperties": false,
							"required": ["label", "description"],
							"properties": {
								"label": {"type": "string", "description": "The display text for this option that the user will see and select. Should be concise (1-5 words) and clearly describe the choice."},
								"description": {"type": "string", "description": "Explanation of what this option means or what will happen if chosen. Useful for providing context about trade-offs or implications."},
								"preview": {"type": "string", "description": "Optional preview content rendered when this option is focused. Use for mockups, code snippets, or visual comparisons that help users compare options. See the tool description for the expected content format."}
							}
						}
					}
				}
			}
		},
		"answers": {
			"type": "object",
			"additionalProperties": {"type": "array", "items": {"type": "string"}},
			"description": "User answers collected by the runtime: question text → chosen option labels (one entry per selection; a single-select question yields a one-element array)."
		},
		"annotations": {
			"type": "object",
			"description": "Optional per-question annotations from the user (e.g., notes on preview selections). Keyed by question text.",
			"additionalProperties": {
				"type": "object",
				"additionalProperties": false,
				"properties": {
					"notes": {"type": "string", "description": "Free-text notes the user added to their selection."},
					"preview": {"type": "string", "description": "The preview content of the selected option, if the question used previews."}
				}
			}
		},
		"metadata": {
			"type": "object",
			"additionalProperties": false,
			"description": "Optional metadata for tracking and analytics purposes. Not displayed to user.",
			"properties": {
				"source": {"type": "string", "description": "Optional identifier for the source of this question (e.g., \"remember\" for /remember command). Used for analytics tracking."}
			}
		}
	}
}`
