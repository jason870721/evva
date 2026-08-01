// Self-contained HTML transcript export.
//
// The deliverable is one file the operator can mail to a colleague: no
// stylesheet URL, no font CDN, no script tag, no image host — a strict
// offline reader must see exactly what the author saw. Everything is
// inlined and everything is escaped.
//
// Secrets are scrubbed unconditionally, NOT according to the live
// `redaction` config. Export is the moment a transcript stops being local,
// and an operator who turned masking off for their own terminal has said
// nothing about what they want leaving the machine. Making that structural
// — the redactor is built here, not passed in — means no call site can get
// it wrong.
package session

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"strings"
	"time"

	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/redact"
)

// exportResultCap truncates tool output in the default (non-full) render.
// Enough to see what a call returned; short enough that a session with two
// hundred file reads is still a document rather than a data dump.
const exportResultCap = 2000

// ExportOptions tunes the render. The zero value is the shareable default:
// tool results present, collapsed, and truncated.
type ExportOptions struct {
	// Full keeps tool results at their original length. The archival
	// setting — bigger file, nothing elided.
	Full bool
}

// ExportHTML writes snap as one self-contained HTML document.
//
// Returns the number of distinct secrets masked, so the caller can tell the
// operator what was scrubbed on the way out.
func ExportHTML(w io.Writer, snap *Snapshot, opt ExportOptions) (int, error) {
	if snap == nil {
		return 0, fmt.Errorf("session: cannot export a nil snapshot")
	}
	r, err := redact.New(redact.Options{})
	if err != nil {
		return 0, fmt.Errorf("session: build redactor: %w", err)
	}
	esc := func(s string) string { return html.EscapeString(r.Redact(s)) }

	var b strings.Builder
	b.WriteString("<!doctype html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
	fmt.Fprintf(&b, "<title>%s — evva transcript</title>\n", esc(snap.Label()))
	b.WriteString("<style>\n")
	b.WriteString(exportCSS)
	b.WriteString("</style>\n</head>\n<body>\n")

	writeExportHeader(&b, snap, esc)

	b.WriteString("<main>\n")
	for _, m := range snap.Session.Messages {
		writeExportMessage(&b, m, opt, esc)
	}
	b.WriteString("</main>\n")

	writeExportFooter(&b, snap, r.Unique())

	b.WriteString("</body>\n</html>\n")
	if _, err := io.WriteString(w, b.String()); err != nil {
		return 0, err
	}
	return r.Unique(), nil
}

func writeExportHeader(b *strings.Builder, snap *Snapshot, esc func(string) string) {
	b.WriteString("<header>\n")
	fmt.Fprintf(b, "<h1>%s</h1>\n", esc(snap.Label()))
	b.WriteString("<dl class=\"meta\">\n")
	row := func(k, v string) {
		if v == "" {
			return
		}
		fmt.Fprintf(b, "<dt>%s</dt><dd>%s</dd>\n", html.EscapeString(k), esc(v))
	}
	row("session", snap.SessionID)
	row("workdir", snap.Workdir)
	row("persona", snap.Profile)
	row("model", snap.Provider+" / "+snap.Model)
	row("started", snap.CreatedAt.Local().Format("2006-01-02 15:04:05"))
	row("last active", snap.UpdatedAt.Local().Format("2006-01-02 15:04:05"))
	if snap.ParentID != "" {
		row("forked from", fmt.Sprintf("%s (at %d messages)", snap.ParentID, snap.ForkedAtLen))
	}
	row("messages", fmt.Sprintf("%d", len(snap.Session.Messages)))
	b.WriteString("</dl>\n</header>\n")
}

func writeExportMessage(b *strings.Builder, m llm.Message, opt ExportOptions, esc func(string) string) {
	switch m.Role {
	case llm.RoleUser:
		// Tool results ride on RoleTool, but a user message can also carry
		// injected reminders; both render as the user's side of the turn.
		if strings.TrimSpace(m.Content) == "" && len(m.ToolResults) == 0 {
			return
		}
		if s := strings.TrimSpace(m.Content); s != "" {
			fmt.Fprintf(b, "<section class=\"turn user\"><div class=\"who\">user</div><div class=\"body\">%s</div></section>\n", esc(s))
		}
		writeExportResults(b, m.ToolResults, opt, esc)
	case llm.RoleAssistant:
		fmt.Fprintf(b, "<section class=\"turn assistant\"><div class=\"who\">evva</div>")
		if s := strings.TrimSpace(m.Thinking); s != "" {
			fmt.Fprintf(b, "<details class=\"thinking\"><summary>thinking</summary><pre>%s</pre></details>", esc(s))
		}
		if s := strings.TrimSpace(m.Content); s != "" {
			fmt.Fprintf(b, "<div class=\"body\">%s</div>", esc(s))
		}
		for _, c := range m.ToolCalls {
			if c == nil {
				continue
			}
			fmt.Fprintf(b, "<details class=\"call\"><summary><span class=\"tool\">%s</span> %s</summary><pre>%s</pre></details>",
				html.EscapeString(c.Name), esc(callSummary(c.Input)), esc(prettyJSON(c.Input)))
		}
		b.WriteString("</section>\n")
	case llm.RoleTool:
		writeExportResults(b, m.ToolResults, opt, esc)
	case llm.RoleSystem:
		// The system prompt is not part of the conversation and is the one
		// place a persona's full instructions would leak verbatim into a
		// shared file. Omitted deliberately.
	}
}

func writeExportResults(b *strings.Builder, results []*llm.ToolResult, opt ExportOptions, esc func(string) string) {
	for _, tr := range results {
		if tr == nil {
			continue
		}
		body := tr.Content
		if body == "" && len(tr.ContentBlocks) > 0 {
			body = llm.RenderContentBlocksAsText(tr.ContentBlocks)
		}
		note := ""
		if !opt.Full && len(body) > exportResultCap {
			note = fmt.Sprintf("\n\n… %d more bytes (re-export with -full)", len(body)-exportResultCap)
			body = body[:exportResultCap]
		}
		class := "result"
		label := "result"
		if tr.IsError {
			class = "result error"
			label = "error"
		}
		fmt.Fprintf(b, "<section class=\"turn tool\"><details class=\"%s\"><summary>%s</summary><pre>%s</pre></details></section>\n",
			class, label, esc(body+note))
	}
}

func writeExportFooter(b *strings.Builder, snap *Snapshot, masked int) {
	u := snap.Session.Usage
	b.WriteString("<footer>\n<dl class=\"meta\">\n")
	fmt.Fprintf(b, "<dt>input tokens</dt><dd>%d</dd>\n", u.InputTokens)
	fmt.Fprintf(b, "<dt>output tokens</dt><dd>%d</dd>\n", u.OutputTokens)
	if u.CacheReadTokens > 0 || u.CacheCreationTokens > 0 {
		fmt.Fprintf(b, "<dt>cache read / write</dt><dd>%d / %d</dd>\n", u.CacheReadTokens, u.CacheCreationTokens)
	}
	if u.ReasoningTokens > 0 {
		fmt.Fprintf(b, "<dt>reasoning tokens</dt><dd>%d</dd>\n", u.ReasoningTokens)
	}
	fmt.Fprintf(b, "<dt>secrets masked</dt><dd>%d</dd>\n", masked)
	fmt.Fprintf(b, "<dt>exported</dt><dd>%s</dd>\n", time.Now().Local().Format("2006-01-02 15:04:05"))
	b.WriteString("</dl>\n<p class=\"note\">Exported by evva. Secrets were scrubbed on the way out regardless of the session's redaction setting; verify before sharing.</p>\n</footer>\n")
}

// callSummary renders a one-line hint of a tool call's arguments for the
// <summary> line — the same "which file / which command" question the TUI's
// tool rows answer.
func callSummary(input json.RawMessage) string {
	var m map[string]any
	if json.Unmarshal(input, &m) != nil {
		return ""
	}
	// Ordered by which key actually identifies the call: grep carries both
	// `pattern` and `path`, and the pattern is what the reader wants.
	for _, k := range []string{"command", "pattern", "query", "file_path", "url", "path", "description"} {
		if v, ok := m[k].(string); ok && v != "" {
			return truncateRunes(strings.ReplaceAll(v, "\n", " "), 90)
		}
	}
	return ""
}

func prettyJSON(raw json.RawMessage) string {
	var out bytes.Buffer
	if err := json.Indent(&out, raw, "", "  "); err != nil {
		return string(raw)
	}
	return out.String()
}

// truncateRunes cuts at a rune boundary so a multi-byte character is never
// split into mojibake in the middle of a summary line.
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// exportCSS is the whole stylesheet. Inline because a shared file must not
// fetch anything; system fonts because a font CDN is a fetch.
const exportCSS = `
:root { color-scheme: light dark;
  --bg:#fbfbfa; --fg:#23211e; --dim:#6b6660; --line:#e4e0da;
  --user:#f0ede7; --tool:#f6f5f2; --err:#b3261e; --accent:#7a5c3e; }
@media (prefers-color-scheme: dark) { :root {
  --bg:#16150f; --fg:#e8e4dc; --dim:#948d82; --line:#2d2a22;
  --user:#211f18; --tool:#1c1a14; --err:#f2b8b5; --accent:#d0a875; } }
* { box-sizing:border-box }
body { margin:0 auto; max-width:52rem; padding:2rem 1rem 4rem;
  background:var(--bg); color:var(--fg); line-height:1.6;
  font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Helvetica,Arial,sans-serif; }
h1 { font-size:1.35rem; margin:0 0 .75rem; font-weight:600; overflow-wrap:anywhere }
header, footer { border-bottom:1px solid var(--line); padding-bottom:1rem; margin-bottom:2rem }
footer { border-bottom:0; border-top:1px solid var(--line); padding-top:1rem; margin:2.5rem 0 0 }
dl.meta { display:grid; grid-template-columns:max-content 1fr; gap:.15rem .75rem;
  margin:0; font-size:.8rem; color:var(--dim) }
dl.meta dt { font-weight:600 } dl.meta dd { margin:0; overflow-wrap:anywhere }
.turn { margin:0 0 1.1rem }
.who { font-size:.7rem; text-transform:uppercase; letter-spacing:.09em;
  color:var(--dim); margin-bottom:.3rem }
.assistant .who { color:var(--accent) }
.body { white-space:pre-wrap; overflow-wrap:anywhere }
.user .body { background:var(--user); padding:.6rem .8rem; border-radius:.4rem }
details { margin:.4rem 0; background:var(--tool); border:1px solid var(--line);
  border-radius:.4rem; padding:.35rem .6rem; font-size:.85rem }
summary { cursor:pointer; color:var(--dim) }
summary .tool { color:var(--accent); font-weight:600 }
details.error summary { color:var(--err) }
pre { margin:.5rem 0 .2rem; padding:.5rem; overflow-x:auto; background:var(--bg);
  border-radius:.3rem; font-size:.8rem; line-height:1.45; white-space:pre-wrap;
  overflow-wrap:anywhere; font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace }
.note { font-size:.75rem; color:var(--dim); margin:.8rem 0 0 }
`
