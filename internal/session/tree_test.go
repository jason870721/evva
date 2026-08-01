package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/johnny1110/evva/pkg/llm"
)

// The header decode must count messages without materializing them —
// that is the whole reason listing can afford to walk every workdir.
func TestListCountsMessagesWithoutDecodingBodies(t *testing.T) {
	home := t.TempDir()
	snap := newSnapshot("s1", "-tmp", "hello")
	snap.Session.Messages = []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "hi"},
		{Role: llm.RoleUser, Content: "again"},
	}
	if err := Save(home, snap); err != nil {
		t.Fatal(err)
	}
	rows, _, err := List(home, "-tmp")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].MessageCount != 3 {
		t.Fatalf("MessageCount: got %+v, want one row with 3", rows)
	}
	if rows[0].FirstUserPrompt != "hello" {
		t.Errorf("envelope did not survive the header decode: %q", rows[0].FirstUserPrompt)
	}
}

func TestListAllSpansWorkdirs(t *testing.T) {
	home := t.TempDir()
	if err := Save(home, newSnapshot("a", "-proj-one", "one")); err != nil {
		t.Fatal(err)
	}
	if err := Save(home, newSnapshot("b", "-proj-two", "two")); err != nil {
		t.Fatal(err)
	}
	rows, warnings, err := ListAll(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
	if len(rows) != 2 {
		t.Fatalf("ListAll should see both workdirs; got %d rows", len(rows))
	}
	slugs := map[string]bool{rows[0].WorkdirSlug: true, rows[1].WorkdirSlug: true}
	if !slugs["-proj-one"] || !slugs["-proj-two"] {
		t.Errorf("rows lost their workdir slug: %v", slugs)
	}
}

func TestListAllMissingRootIsNotAnError(t *testing.T) {
	rows, _, err := ListAll(t.TempDir())
	if err != nil || len(rows) != 0 {
		t.Errorf("a machine with no sessions is the normal empty state; got %d rows, err=%v", len(rows), err)
	}
}

// A snapshot written before v1.19 has no title / parent_id / pinned keys.
// It must load with those fields zeroed rather than failing — which is why
// they were added without bumping SnapshotVersion.
func TestPreV119SnapshotLoads(t *testing.T) {
	home := t.TempDir()
	dir := SessionsDir(home, "-legacy")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{
  "version": 1,
  "session_id": "old-one",
  "workdir": "/tmp/proj",
  "workdir_slug": "-legacy",
  "profile": "evva",
  "provider": "anthropic",
  "model": "claude-opus-4-8",
  "created_at": "2026-01-02T03:04:05Z",
  "updated_at": "2026-01-02T03:14:05Z",
  "first_user_prompt": "port the parser",
  "session": {
    "messages": [{"Role":"user","Content":"port the parser"}],
    "usage": {"InputTokens": 10, "OutputTokens": 5},
    "last_turn_input_tokens": 10,
    "micro_compacted": true,
    "full_compact_count": 1
  }
}`
	if err := os.WriteFile(filepath.Join(dir, "old-one.json"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	snap, err := Load(home, "-legacy", "old-one")
	if err != nil {
		t.Fatalf("a pre-v1.19 snapshot must still load: %v", err)
	}
	if snap.FirstUserPrompt != "port the parser" || len(snap.Session.Messages) != 1 {
		t.Errorf("legacy decode lost content: %+v", snap.Meta)
	}
	if snap.Title != "" || snap.ParentID != "" || snap.Pinned {
		t.Errorf("absent fields should decode to zero, got title=%q parent=%q pinned=%v",
			snap.Title, snap.ParentID, snap.Pinned)
	}
	if snap.Label() != "port the parser" {
		t.Errorf("an untitled session labels itself by its first prompt; got %q", snap.Label())
	}
	// micro_compacted is the pre-v1.17 key for span compaction; still read.
	if !snap.Session.MicroCompacted {
		t.Error("span-compaction flag lost on legacy decode")
	}

	rows, _, err := List(home, "-legacy")
	if err != nil || len(rows) != 1 || rows[0].MessageCount != 1 {
		t.Errorf("legacy file must list too; got %+v err=%v", rows, err)
	}
}

// The Meta split is an embedding, so the JSON stays flat. If it ever
// nested under a "meta" key every existing session on every machine would
// silently stop loading.
func TestSnapshotJSONStaysFlat(t *testing.T) {
	data, err := json.Marshal(newSnapshot("s", "-tmp", "hi"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, nested := raw["meta"]; nested {
		t.Fatal("Meta must be embedded, not a nested object — that would orphan every existing snapshot")
	}
	for _, k := range []string{"version", "session_id", "workdir_slug", "first_user_prompt", "session"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("top-level key %q missing from the encoded snapshot", k)
		}
	}
	// Never-set optional fields stay out of the file entirely.
	for _, k := range []string{"title", "parent_id", "pinned", "forked_at_len"} {
		if _, ok := raw[k]; ok {
			t.Errorf("unset optional field %q should be omitted, not written", k)
		}
	}
}

func TestForkMetaBranchesWithoutInheritingCuration(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	parent := Meta{
		Version:         SnapshotVersion,
		SessionID:       "parent-id",
		Workdir:         "/tmp/proj",
		WorkdirSlug:     "-tmp-proj",
		Profile:         "evva",
		Provider:        "anthropic",
		Model:           "claude-opus-4-8",
		FirstUserPrompt: "port the parser",
		Title:           "the big port",
		Pinned:          true,
		CreatedAt:       now.Add(-time.Hour),
	}
	child := ForkMeta(parent, "child-id", 42, now)

	if child.ParentID != "parent-id" || child.SessionID != "child-id" {
		t.Errorf("lineage wrong: id=%q parent=%q", child.SessionID, child.ParentID)
	}
	if child.ForkedAtLen != 42 {
		t.Errorf("ForkedAtLen: got %d, want 42", child.ForkedAtLen)
	}
	if child.Workdir != parent.Workdir || child.WorkdirSlug != parent.WorkdirSlug || child.Model != parent.Model {
		t.Error("a fork must inherit the parent's setup")
	}
	if child.Pinned {
		t.Error("a fork is an experiment: it must not inherit the parent's pin")
	}
	if child.Title != "" {
		t.Errorf("a fork must not inherit the parent's title, or every branch reads alike; got %q", child.Title)
	}
	if !child.CreatedAt.Equal(now) {
		t.Errorf("CreatedAt should be the fork moment; got %v", child.CreatedAt)
	}
	// The parent value must be untouched — ForkMeta takes a copy.
	if parent.ParentID != "" || parent.SessionID != "parent-id" || !parent.Pinned {
		t.Error("ForkMeta mutated its input")
	}
}

func TestLabelPrefersTitleThenPrompt(t *testing.T) {
	m := Meta{}
	if got := m.Label(); !strings.Contains(got, "no user prompt") {
		t.Errorf("an empty session still needs a row label; got %q", got)
	}
	m.FirstUserPrompt = "fix the flake"
	if m.Label() != "fix the flake" {
		t.Errorf("got %q", m.Label())
	}
	m.Title = "flaky CI"
	if m.Label() != "flaky CI" {
		t.Errorf("an explicit title wins; got %q", m.Label())
	}
}

// A byte-sliced preview splits multi-byte characters, and the damage is
// permanent: the truncated form is what gets written to the snapshot.
func TestFirstUserPromptPreviewCutsOnARuneBoundary(t *testing.T) {
	long := strings.Repeat("我需要把這個做完", 40) // 3 bytes/rune, no boundary at 200
	got := FirstUserPromptPreview([]llm.Message{{Role: llm.RoleUser, Content: long}})
	if got == "" {
		t.Fatal("expected a preview")
	}
	if len(got) > PreviewMaxBytes {
		t.Errorf("preview exceeded the byte cap: %d", len(got))
	}
	if !utf8.ValidString(got) {
		t.Errorf("preview is not valid UTF-8: %q", got)
	}
	if strings.ContainsRune(got, utf8.RuneError) {
		t.Errorf("preview contains a replacement char: %q", got)
	}
}
