package session

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/tools"
)

func exportFixture() *Snapshot {
	snap := newSnapshot("s1", "-tmp", "deploy the thing")
	snap.Session.Usage = llm.Usage{InputTokens: 1200, OutputTokens: 300}
	snap.Session.Messages = []llm.Message{
		{Role: llm.RoleSystem, Content: "SYSTEM PROMPT: you are evva and here are your secret instructions"},
		{Role: llm.RoleUser, Content: "deploy the thing"},
		{
			Role:    llm.RoleAssistant,
			Content: "Reading the config first.",
			ToolCalls: []*tools.Call{{
				ID:    "c1",
				Name:  "read",
				Input: json.RawMessage(`{"file_path":"/srv/app/.env"}`),
			}},
		},
		{Role: llm.RoleTool, ToolResults: []*llm.ToolResult{{
			ID:      "c1",
			Content: "GITHUB_TOKEN=ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789\nPORT=8080",
		}}},
		{Role: llm.RoleTool, ToolResults: []*llm.ToolResult{{
			ID: "c2", IsError: true, Content: "connection refused",
		}}},
	}
	return snap
}

func exportString(t *testing.T, snap *Snapshot, opt ExportOptions) (string, int) {
	t.Helper()
	var b strings.Builder
	masked, err := ExportHTML(&b, snap, opt)
	if err != nil {
		t.Fatalf("ExportHTML: %v", err)
	}
	return b.String(), masked
}

// The export's headline promise: the file works with the network off.
// Any absolute URL, script tag, or remote asset reference breaks it.
func TestExportIsSelfContained(t *testing.T) {
	out, _ := exportString(t, exportFixture(), ExportOptions{})

	for _, forbidden := range []string{"<script", "src=", "@import", "<link"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("export must fetch nothing, found %q", forbidden)
		}
	}
	// href= is allowed in principle but nothing should emit one today, and
	// an absolute URL anywhere is a fetch waiting to happen.
	if m := regexp.MustCompile(`https?://`).FindString(out); m != "" {
		t.Errorf("export contains an absolute URL (%q) — it must be readable offline", m)
	}
	if !strings.Contains(out, "<style>") {
		t.Error("stylesheet should be inlined")
	}
	if !strings.HasPrefix(out, "<!doctype html>") {
		t.Error("export should be a complete document")
	}
}

// Redaction is unconditional: the caller cannot turn it off, because
// export is the moment a transcript leaves the machine.
func TestExportScrubsSecretsRegardlessOfConfig(t *testing.T) {
	out, masked := exportString(t, exportFixture(), ExportOptions{Full: true})

	if strings.Contains(out, "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789") {
		t.Fatal("the github token survived export")
	}
	if masked < 1 {
		t.Errorf("expected at least one masked value, got %d", masked)
	}
	if !strings.Contains(out, "PORT=8080") {
		t.Error("redaction should mask the secret, not the surrounding output")
	}
	if !strings.Contains(out, "secrets masked") {
		t.Error("the footer should tell the operator scrubbing happened")
	}
}

// The system prompt is not conversation and would leak a persona's full
// instructions into a shared file.
func TestExportOmitsSystemPrompt(t *testing.T) {
	out, _ := exportString(t, exportFixture(), ExportOptions{})
	if strings.Contains(out, "secret instructions") {
		t.Error("the system prompt must not be exported")
	}
}

func TestExportEscapesMarkup(t *testing.T) {
	snap := newSnapshot("s1", "-tmp", "x")
	snap.Session.Messages = []llm.Message{
		{Role: llm.RoleUser, Content: `<img onerror="alert(1)"> & <b>bold</b>`},
	}
	out, _ := exportString(t, snap, ExportOptions{})
	if strings.Contains(out, "<img onerror") {
		t.Error("user content must be escaped, not rendered as markup")
	}
	if !strings.Contains(out, "&lt;img onerror") {
		t.Error("expected the escaped form in the output")
	}
}

func TestExportTruncatesUnlessFull(t *testing.T) {
	snap := newSnapshot("s1", "-tmp", "x")
	long := strings.Repeat("a", exportResultCap*2)
	snap.Session.Messages = []llm.Message{
		{Role: llm.RoleTool, ToolResults: []*llm.ToolResult{{ID: "c1", Content: long}}},
	}

	short, _ := exportString(t, snap, ExportOptions{})
	if !strings.Contains(short, "more bytes (re-export with -full)") {
		t.Error("the default render should say what it elided")
	}
	if strings.Count(short, "a") > exportResultCap+200 {
		t.Error("the default render should not carry the whole result")
	}

	full, _ := exportString(t, snap, ExportOptions{Full: true})
	if strings.Contains(full, "more bytes") {
		t.Error("-full should elide nothing")
	}
	if strings.Count(full, "a") < exportResultCap*2 {
		t.Error("-full should carry the whole result")
	}
}

func TestExportMarksErrors(t *testing.T) {
	out, _ := exportString(t, exportFixture(), ExportOptions{})
	if !strings.Contains(out, "connection refused") {
		t.Error("a failed tool call belongs in the record")
	}
	if !strings.Contains(out, `class="result error"`) {
		t.Error("errors should be visually distinguishable from results")
	}
}

func TestExportNilSnapshotErrors(t *testing.T) {
	var b strings.Builder
	if _, err := ExportHTML(&b, nil, ExportOptions{}); err == nil {
		t.Error("exporting nothing should be an error, not an empty file")
	}
}
