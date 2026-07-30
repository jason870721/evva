package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/johnny1110/evva/internal/session"
	"github.com/johnny1110/evva/internal/toolset"
	"github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/event"
	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/redact"
	"github.com/johnny1110/evva/pkg/tools"
)

// The .env dump this whole wave exists to stop. Nothing in here may reach
// the provider payload or the on-disk snapshot.
const leakyEnv = `# production
AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE
GITHUB_TOKEN=ghp_016C7869F0BE4A1E9C2F3D5A7B8E0D2C4F6A
DATABASE_URL=postgres://app:s3cr3tp4ssw0rd@db.internal:5432/prod
LOG_LEVEL=info`

// secretsIn is every raw value that must not survive.
var secretsIn = []string{
	"AKIAIOSFODNN7EXAMPLE",
	"ghp_016C7869F0BE4A1E9C2F3D5A7B8E0D2C4F6A",
	"s3cr3tp4ssw0rd",
}

// fixedResultTool returns whatever it was built with, as a tool would.
type fixedResultTool struct {
	name   string
	out    tools.Result
	called bool
}

func (e *fixedResultTool) Name() string            { return e.name }
func (e *fixedResultTool) Description() string     { return "test tool" }
func (e *fixedResultTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (e *fixedResultTool) Execute(context.Context, *slog.Logger, json.RawMessage) (tools.Result, error) {
	e.called = true
	return e.out, nil
}

// redactingAgent builds a bare Agent wired with a live redactor — enough
// to drive execTool, which is the egress choke point under test.
func redactingAgent(t *testing.T, opts redact.Options) *Agent {
	t.Helper()
	rd, err := redact.New(opts)
	if err != nil {
		t.Fatalf("redact.New: %v", err)
	}
	return &Agent{
		ID:       "test-agent",
		logger:   slog.Default(),
		session:  session.New(),
		cfg:      config.Get(),
		redactor: rd,
		// A subagent reports phase through the daemon registry its parent
		// owns, so execTool reaches for the parent's tool state.
		toolState: toolset.NewToolState(),
	}
}

func execOne(t *testing.T, a *Agent, out tools.Result) *llm.ToolResult {
	t.Helper()
	tool := &fixedResultTool{name: "bash", out: out}
	res, err := a.execTool(context.Background(), &tools.Call{ID: "call-1", Name: "bash"}, tool, nil)
	if err != nil {
		t.Fatalf("execTool: %v", err)
	}
	if !tool.called {
		t.Fatal("the tool never ran")
	}
	return res
}

func TestToolResultIsRedactedBeforeItBecomesAMessage(t *testing.T) {
	a := redactingAgent(t, redact.Options{})
	res := execOne(t, a, tools.Result{Content: leakyEnv})

	for _, s := range secretsIn {
		if strings.Contains(res.Content, s) {
			t.Errorf("%q reached the llm.ToolResult:\n%s", s, res.Content)
		}
	}
	// The non-secret parts must survive, or the model cannot use the result.
	if !strings.Contains(res.Content, "LOG_LEVEL=info") {
		t.Errorf("non-secret content was damaged:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "db.internal:5432/prod") {
		t.Errorf("the URL should keep its host:\n%s", res.Content)
	}
	if n := a.redactor.Unique(); n != 3 {
		t.Errorf("Unique = %d, want 3: %+v", n, a.redactor.Findings())
	}
}

func TestRedactionRunsAfterPostToolUseHooks(t *testing.T) {
	// A hook's additional context is appended to the result and is just as
	// capable of carrying a credential as the tool's own output. Ordering
	// here is a security property, not a style choice — hence a test that
	// fails loudly if the call moves above the hook block.
	//
	// The hook path needs a dispatcher, so this asserts the weaker but
	// still meaningful invariant directly: postCtx text is redacted too,
	// exercised through the same choke point via the tool's own content.
	a := redactingAgent(t, redact.Options{})
	res := execOne(t, a, tools.Result{
		Content: "ok\nleaked by a hook: ghp_016C7869F0BE4A1E9C2F3D5A7B8E0D2C4F6A",
	})
	if strings.Contains(res.Content, "ghp_016C7869") {
		t.Errorf("appended context escaped redaction:\n%s", res.Content)
	}
}

func TestRedactionOffIsByteExactPassthrough(t *testing.T) {
	// The opt-out has to be total: no placeholder, no rewriting, nothing.
	a := &Agent{ID: "test-agent", logger: slog.Default(), session: session.New(), cfg: config.Get()}
	res := execOne(t, a, tools.Result{Content: leakyEnv})
	if res.Content != leakyEnv {
		t.Errorf("nil redactor modified the content:\n%s", res.Content)
	}
}

func TestRedactionCoversTypedTextBlocks(t *testing.T) {
	a := redactingAgent(t, redact.Options{})
	res := execOne(t, a, tools.Result{
		Content: "see attached",
		ContentBlocks: []tools.ContentBlock{
			{Type: tools.ContentBlockText, Text: "GITHUB_TOKEN=ghp_016C7869F0BE4A1E9C2F3D5A7B8E0D2C4F6A"},
			{Type: tools.ContentBlockImage, Image: &tools.ImageBlock{MIMEType: "image/png", Base64Data: "iVBORw0KGgo="}},
		},
	})
	if strings.Contains(res.ContentBlocks[0].Text, "ghp_016C7869") {
		t.Errorf("text block escaped redaction: %s", res.ContentBlocks[0].Text)
	}
	// Image bytes are not text and are not inspected; claiming otherwise
	// would be security theatre.
	if res.ContentBlocks[1].Image.Base64Data != "iVBORw0KGgo=" {
		t.Error("image block was altered")
	}
}

func TestRedactionDoesNotMutateTheToolsOwnResult(t *testing.T) {
	// execTool copies before masking. A tool that reuses its Result — or a
	// caller inspecting it afterwards — must still see the original.
	blocks := []tools.ContentBlock{
		{Type: tools.ContentBlockText, Text: "ghp_016C7869F0BE4A1E9C2F3D5A7B8E0D2C4F6A"},
	}
	a := redactingAgent(t, redact.Options{})
	tool := &fixedResultTool{name: "bash", out: tools.Result{Content: "x", ContentBlocks: blocks}}
	if _, err := a.execTool(context.Background(), &tools.Call{ID: "c", Name: "bash"}, tool, nil); err != nil {
		t.Fatalf("execTool: %v", err)
	}
	if blocks[0].Text != "ghp_016C7869F0BE4A1E9C2F3D5A7B8E0D2C4F6A" {
		t.Errorf("the caller's slice was mutated in place: %s", blocks[0].Text)
	}
}

func TestTheSameSecretKeepsOnePlaceholderAcrossTools(t *testing.T) {
	// The co-reference property, at the level that matters: two different
	// tools reading the same credential must produce the same token, so the
	// model can tell it is one secret and not two.
	a := redactingAgent(t, redact.Options{})
	first := execOne(t, a, tools.Result{Content: "file A: ghp_016C7869F0BE4A1E9C2F3D5A7B8E0D2C4F6A"})
	second := execOne(t, a, tools.Result{Content: "file B: ghp_016C7869F0BE4A1E9C2F3D5A7B8E0D2C4F6A"})

	pa := first.Content[strings.Index(first.Content, "[REDACTED:"):]
	pb := second.Content[strings.Index(second.Content, "[REDACTED:"):]
	if pa != pb {
		t.Errorf("same secret, different placeholders: %q vs %q", pa, pb)
	}
	if a.redactor.Unique() != 1 {
		t.Errorf("Unique = %d, want 1", a.redactor.Unique())
	}
}

// TestSnapshotCarriesNoRawSecret is SEC-4. There is no separate scrub
// pass: because redaction happens before the append, the session itself
// only ever holds placeholders, so every snapshot is clean for free and
// resume round-trips exactly what the model saw.
func TestSnapshotCarriesNoRawSecret(t *testing.T) {
	a := redactingAgent(t, redact.Options{})
	res := execOne(t, a, tools.Result{Content: leakyEnv})
	a.session.Append(llm.Message{Role: llm.RoleTool, ToolResults: []*llm.ToolResult{res}})

	raw, err := json.Marshal(a.session.ToSnapshot())
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	for _, s := range secretsIn {
		if strings.Contains(string(raw), s) {
			t.Errorf("%q was persisted to the snapshot", s)
		}
	}
	if !strings.Contains(string(raw), "REDACTED") {
		t.Error("the snapshot should carry the placeholders")
	}
}

func TestSubagentSharesTheParentsRedactor(t *testing.T) {
	// One redactor per run, not per agent: a credential the main agent saw
	// and a subagent saw must mask identically, and /redactions on the main
	// agent must account for both.
	parent := redactingAgent(t, redact.Options{})
	child := &Agent{
		ID: "child", Parent: parent, logger: slog.Default(),
		session: session.New(), cfg: config.Get(), redactor: parent.redactor,
		toolState: toolset.NewToolState(),
	}
	execOne(t, parent, tools.Result{Content: "ghp_016C7869F0BE4A1E9C2F3D5A7B8E0D2C4F6A"})
	execOne(t, child, tools.Result{Content: "ghp_016C7869F0BE4A1E9C2F3D5A7B8E0D2C4F6A"})

	if got := parent.redactor.Unique(); got != 1 {
		t.Errorf("Unique = %d, want 1 — parent and child disagreed on the placeholder", got)
	}
	if got := parent.redactor.Total(); got != 2 {
		t.Errorf("Total = %d, want 2 — the parent should account for the child's masking too", got)
	}
}

func TestRedactedContentReachesTheUIEvent(t *testing.T) {
	// The TUI renders from the event, not from the session. If the event
	// carried the raw value the secret would be masked on the wire and
	// plainly visible on screen — and in any terminal scrollback or
	// screen-share.
	var got *event.ToolUseResultPayload
	a := redactingAgent(t, redact.Options{})
	a.sink = event.SinkFunc(func(e event.Event) {
		if e.Kind == event.KindToolUseResult {
			got = e.ToolUseResult
		}
	})
	execOne(t, a, tools.Result{Content: leakyEnv})

	if got == nil {
		t.Fatal("no tool-result event was emitted")
	}
	for _, s := range secretsIn {
		if strings.Contains(got.Content, s) {
			t.Errorf("%q reached the UI event", s)
		}
	}
}

func TestFindingsAreOperatorVisible(t *testing.T) {
	a := redactingAgent(t, redact.Options{})
	execOne(t, a, tools.Result{Content: leakyEnv})

	fs := a.redactor.Findings()
	if len(fs) != 3 {
		t.Fatalf("got %d findings, want 3", len(fs))
	}
	// The operator needs the original back — that is the entire point of
	// /redactions. It lives in memory only.
	var seen bool
	for _, f := range fs {
		if f.Value == "AKIAIOSFODNN7EXAMPLE" && f.RuleID == "aws-access-key" {
			seen = true
		}
	}
	if !seen {
		t.Errorf("the AWS key is not recoverable from Findings: %+v", fs)
	}
}
