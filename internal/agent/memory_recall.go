package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/johnny1110/evva/internal/memdir"
	"github.com/johnny1110/evva/internal/memdir/recall"
	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/tools"
)

// recallMarkerOpen tags each surfaced memory inside the per-turn recall
// <system-reminder> with its dir-relative filename, e.g. "[memory: feedback/x.md]".
// surfacedMemoryPaths parses these back out of the transcript so the de-dup set
// is DERIVED from messages (and therefore reset by compaction), not held as
// persistent agent state (PRD §5.3).
const recallMarkerOpen = "[memory: "

// recallEffortName is the effort level the recall side-query runs at on the
// hosted providers (Anthropic / DeepSeek / OpenAI). Selecting relevant memories
// is a light judgment task, so medium is plenty and keeps the call cheap. Ollama
// mirrors the main agent's effort instead (see recallTarget).
const recallEffortName = "medium"

// runMemoryRecall runs the per-user-turn relevance side-query and returns a
// single <system-reminder> message carrying the bodies of the memories judged
// relevant to query (with freshness caveats when stale), or "" when recall is
// off, finds nothing, or fails. It never errors — a recall hiccup must not break
// the turn (PRD §5.4). The bodies ride in a MESSAGE, never the static system
// prompt, so the cached prompt prefix stays byte-stable (§5.3).
func (a *Agent) runMemoryRecall(ctx context.Context, query string) string {
	if a.IsSubagent() || a.cfg == nil {
		return ""
	}
	if !a.cfg.GetEnableAutoMemory() || !a.cfg.GetEnableMemoryRecall() {
		return ""
	}
	dir := a.memSnap.MemoryDir
	if dir == "" || strings.TrimSpace(query) == "" {
		return ""
	}
	client, model := a.recallClient()
	if client == nil {
		return ""
	}
	surfaced := a.surfacedMemoryPaths(dir)
	headers := recall.FindRelevant(ctx, client, model, query, dir, a.recentToolNames(), surfaced)
	if len(headers) == 0 {
		return ""
	}
	return composeRecallReminder(headers)
}

// recallTarget resolves the (provider, model, effort) for the recall side-query.
//
// An explicit, credentialed cfg.MemoryRecallModel wins. Otherwise the default is
// picked WITHIN the active provider — which is always credentialed, so recall
// never depends on a second provider's key:
//
//   - anthropic → claude-sonnet  (Sonnet-tier selection quality, ref parity), medium effort
//   - deepseek  → deepseek-v4-flash (the cheap tier),                         medium effort
//   - openai    → gpt-5.4-mini,                                               medium effort
//   - ollama / other → the active model + the main agent's effort (local / always reachable)
//
// The models are named explicitly (not derived from Models[0]) so the choice is
// self-documenting and stable if a provider's model list is later reordered.
func (a *Agent) recallTarget() (constant.LLMProvider, constant.Model, int) {
	medium := llm.ParseEffort(recallEffortName)

	// Explicit override: a known model whose provider is credentialed.
	if raw := a.cfg.GetMemoryRecallModel(); raw != "" {
		if m, ok := constant.GetModel(raw); ok {
			if p, ok := providerForModel(m); ok && a.providerConfigured(p.Name) {
				return p, m, medium
			}
		}
	}

	p := a.profile.LLMProvider
	switch p.Name {
	case constant.ANTHROPIC.Name:
		return p, constant.SONNET_4_6, medium
	case constant.DEEPSEEK.Name:
		return p, constant.DEEPSEEK_V4_FLASH, medium
	case constant.OPENAI.Name:
		return p, constant.GPT_5_4_MINI, medium
	default: // ollama + any custom provider: mirror the main agent exactly
		return p, a.profile.LLMModel, a.mainEffortLevel()
	}
}

// mainEffortLevel is the effort level the main agent runs at (medium when unset),
// so the ollama recall path can mirror it.
func (a *Agent) mainEffortLevel() int {
	if lvl := llm.ParseEffort(a.effort); lvl > 0 {
		return lvl
	}
	return llm.ParseEffort(recallEffortName)
}

// recallClient builds a DEDICATED client for the recall side-query (never a.llm
// — FindRelevant pins its own system prompt via Apply, which would clobber the
// main loop's prompt on the shared client). Model + effort are resolved per
// active provider by recallTarget. Returns (nil, "") if the build fails — recall
// then degrades to off for the turn.
func (a *Agent) recallClient() (llm.Client, constant.Model) {
	provider, model, effort := a.recallTarget()
	c, err := buildLLMClient(a.cfg, provider, model, []llm.Option{llm.WithEffort(effort)})
	if err != nil {
		a.logger.Debug("memory.recall.client_build_failed", "provider", provider.Name, "model", model, "err", err)
		return nil, ""
	}
	return c, model
}

// providerConfigured reports whether the named provider has a non-empty API key
// in the loaded config. Used so an explicit cross-provider MemoryRecallModel
// override only fires when its provider is actually credentialed.
func (a *Agent) providerConfigured(name string) bool {
	if a.cfg == nil {
		return false
	}
	api, ok := a.cfg.LLMProviderConfig[name]
	return ok && strings.TrimSpace(api.ApiSecret) != ""
}

// providerForModel returns the provider that lists model m, if any.
func providerForModel(m constant.Model) (constant.LLMProvider, bool) {
	for _, p := range constant.GetAllProviders() {
		for _, mm := range p.Models {
			if mm == m {
				return p, true
			}
		}
	}
	return constant.LLMProvider{}, false
}

// surfacedMemoryPaths derives the set of memory files already in this session's
// context — keyed on the dir-relative filename — by scanning the live
// transcript: prior recall reminders (the [memory: …] markers) PLUS files the
// model read directly via the read tool (ref's readFileState guard). Deriving it
// from messages means compaction resets the de-dup for free (PRD §5.3).
func (a *Agent) surfacedMemoryPaths(dir string) map[string]bool {
	out := map[string]bool{}
	workdir := a.Workdir()
	for _, m := range a.session.Messages {
		switch m.Role {
		case llm.RoleUser:
			for _, name := range parseRecallMarkers(m.Content) {
				out[name] = true
			}
		case llm.RoleAssistant:
			for _, c := range m.ToolCalls {
				if c == nil || c.Name != string(tools.READ_FILE) {
					continue
				}
				if rel, ok := memRelPath(dir, workdir, readFilePath(c.Input)); ok {
					out[rel] = true
				}
			}
		}
	}
	return out
}

// recentToolNames collects the distinct tool names from the most recent
// assistant turns — the "recently used tools" signal the recall selector uses to
// avoid re-surfacing usage docs for tools already in play (gotchas still count).
func (a *Agent) recentToolNames() []string {
	seen := map[string]bool{}
	var out []string
	msgs := a.session.Messages
	scanned := 0
	for i := len(msgs) - 1; i >= 0 && scanned < 8; i-- {
		scanned++
		if msgs[i].Role != llm.RoleAssistant {
			continue
		}
		for _, c := range msgs[i].ToolCalls {
			if c == nil || seen[c.Name] {
				continue
			}
			seen[c.Name] = true
			out = append(out, c.Name)
		}
	}
	return out
}

// composeRecallReminder renders the relevant memories as one <system-reminder>
// message: a short framing line, then each memory tagged with its [memory: …]
// marker, its freshness caveat when stale (plain text — the block is already
// wrapped, so FreshnessText not FreshnessNote), and its body. An unreadable file
// is skipped.
func composeRecallReminder(headers []memdir.MemoryHeader) string {
	var b strings.Builder
	b.WriteString("<system-reminder>\n")
	b.WriteString("Relevant memories for this turn, recalled from your memory directory. They are point-in-time notes — verify before relying on them, and ignore any that don't apply to the current request.\n")
	wrote := 0
	for _, h := range headers {
		body, err := os.ReadFile(h.Path)
		if err != nil {
			continue
		}
		b.WriteString("\n")
		fmt.Fprintf(&b, "%s%s]\n", recallMarkerOpen, h.Filename)
		if note := memdir.FreshnessText(h.ModTime); note != "" {
			b.WriteString(note)
			b.WriteString("\n")
		}
		b.WriteString(strings.TrimSpace(string(body)))
		b.WriteString("\n")
		wrote++
	}
	b.WriteString("</system-reminder>")
	if wrote == 0 {
		return ""
	}
	return b.String()
}

// parseRecallMarkers extracts every dir-relative filename tagged with the
// recallMarkerOpen prefix from a message body. Returns nil when none present.
func parseRecallMarkers(content string) []string {
	if !strings.Contains(content, recallMarkerOpen) {
		return nil
	}
	var out []string
	rest := content
	for {
		i := strings.Index(rest, recallMarkerOpen)
		if i < 0 {
			break
		}
		rest = rest[i+len(recallMarkerOpen):]
		j := strings.IndexByte(rest, ']')
		if j < 0 {
			break
		}
		if name := strings.TrimSpace(rest[:j]); name != "" {
			out = append(out, name)
		}
		rest = rest[j+1:]
	}
	return out
}

// memRelPath resolves a read-tool file_path to a memory-dir-relative filename
// (the key surfacedMemoryPaths de-dups on), or (false) when the path is outside
// the memory dir. Relative paths resolve against the workdir, matching how the
// read tool interprets them.
func memRelPath(dir, workdir, p string) (string, bool) {
	if dir == "" || p == "" {
		return "", false
	}
	abs := p
	if !filepath.IsAbs(abs) && workdir != "" {
		abs = filepath.Join(workdir, abs)
	}
	rel, err := filepath.Rel(dir, abs)
	if err != nil || rel == "." || rel == "" || strings.HasPrefix(rel, "..") {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// readFilePath pulls "file_path" out of a read tool call's raw JSON input.
func readFilePath(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var m struct {
		FilePath string `json:"file_path"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	return m.FilePath
}
