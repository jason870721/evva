// Package kits exposes pre-composed tool-name lists a downstream
// consumer can hand to agent.NewProfile without re-typing the canonical
// evva tool selections from scratch.
//
// Every consumer that wanted a "general coding agent" used to assemble
// the same `append(fs.Names(), shell.Names()...)` chain by hand
// (see friday/internal/bootstrap/bootstrap.go for the historical
// example). Phase 19d collapses that chain into named functions so
// callers can pick the kit that matches their intent at a glance.
//
// All kits are pure data — they return fresh slices, so callers can
// `append` more tool names without affecting other consumers. The kit
// authors maintain the canonical "what's in each kit" decision in one
// place; downstream copy-paste drift goes away.
//
//   - GeneralPurposeKit — fs + shell + todo + util + tool_search active,
//     web deferred. The friday baseline.
//   - ReadOnlyKit — read + grep + glob + tree + web + json_query.
//     Audit/explore agents.
//   - CodingKit — GeneralPurpose + notebook + monitor. Heavier coding
//     workflows.
//   - ResearchKit — read + grep + glob + web + json_query + calc + todo.
//     Web-research / fact-finding agents.
package kits

import (
	"github.com/johnny1110/evva/pkg/tools"
	"github.com/johnny1110/evva/pkg/tools/fs"
	"github.com/johnny1110/evva/pkg/tools/monitor"
	"github.com/johnny1110/evva/pkg/tools/notebook"
	"github.com/johnny1110/evva/pkg/tools/shell"
	"github.com/johnny1110/evva/pkg/tools/todo"
	"github.com/johnny1110/evva/pkg/tools/util"
	"github.com/johnny1110/evva/pkg/tools/web"
)

// GeneralPurposeKit returns the canonical evva general-purpose tool
// kit — equivalent to what evva's General subagent runs with:
//
//	active   = fs (read, write, edit, glob) + shell (bash, grep, tree)
//	         + todo (todo_write) + util (json_query, calc) + tool_search
//	deferred = web (web_search, web_fetch)
//
// The active list includes tool_search because the model needs it to
// discover the deferred companions. If your downstream profile passes
// active WITHOUT deferred (no lazy-loadable tools), use
// GeneralPurposeActive instead — that variant omits tool_search since
// there would be nothing for it to find.
//
// Pass the returned slices to agent.ProfileOptions / agent.NewProfile:
//
//	active, deferred := kits.GeneralPurposeKit()
//	prof, _ := agent.NewProfile("friday", systemPrompt, active,
//	    "deepseek", constant.DEEPSEEK_V4_PRO,
//	    agent.ProfileOptions{DeferredTools: deferred})
func GeneralPurposeKit() (active, deferred []tools.ToolName) {
	active = GeneralPurposeActive()
	active = append(active, tools.TOOL_SEARCH)
	deferred = append([]tools.ToolName{}, web.Names()...)
	return active, deferred
}

// GeneralPurposeActive returns the active half of GeneralPurposeKit
// WITHOUT tool_search. Use this when your profile has no deferred
// tools — tool_search active with nothing deferred is pure overhead
// (the model has nothing to discover).
//
// Mirrors GeneralPurposeKit's active membership minus tool_search:
//
//	fs (read, write, edit, glob) + shell (bash, grep, tree)
//	  + todo (todo_write) + util (json_query, calc)
func GeneralPurposeActive() []tools.ToolName {
	active := make([]tools.ToolName, 0, 16)
	active = append(active, fs.Names()...)
	active = append(active, shell.Names()...)
	active = append(active, todo.Names()...)
	active = append(active, util.Names()...)
	return active
}

// ReadOnlyKit returns a read-only tool list for audit / exploration
// agents: read, grep, glob, tree, web_search, web_fetch, json_query.
// No bash, no edit/write, no todo (todo_write mutates session state
// but doesn't touch the filesystem — kept out for purity; add it back
// manually if you want it).
//
// Returned as a single slice (no deferred companion) because the kit
// is small enough to expose every tool eagerly.
func ReadOnlyKit() []tools.ToolName {
	return []tools.ToolName{
		tools.READ_FILE,
		tools.GREP,
		tools.GLOB,
		tools.TREE,
		tools.WEB_SEARCH,
		tools.WEB_FETCH,
		tools.JSON_QUERY,
	}
}

// CodingKit extends GeneralPurposeKit with notebook + monitor. Useful
// for agents that work on Jupyter notebooks or need background-process
// monitoring on top of the standard coding loop.
//
// Returns the same shape as GeneralPurposeKit: (active, deferred).
func CodingKit() (active, deferred []tools.ToolName) {
	active, deferred = GeneralPurposeKit()
	active = append(active, notebook.Names()...)
	active = append(active, monitor.Names()...)
	return active, deferred
}

// ResearchKit returns a research-flavoured tool list: read + grep +
// glob + todo + web (search + fetch) + json_query + calc. No bash, no
// edit/write — the agent investigates and summarises, but doesn't
// mutate the filesystem.
//
// Returned as a single active slice; web tools are part of the active
// kit (not deferred) because the agent leans on them heavily.
func ResearchKit() []tools.ToolName {
	return []tools.ToolName{
		tools.READ_FILE,
		tools.GREP,
		tools.GLOB,
		tools.TREE,
		tools.WEB_SEARCH,
		tools.WEB_FETCH,
		tools.JSON_QUERY,
		tools.CALC,
		tools.TODO_WRITE,
	}
}
