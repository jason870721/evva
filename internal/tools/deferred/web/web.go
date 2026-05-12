// Package web hosts web tools: WebFetch, WebSearch.
//
// Both stateless so safe as singletons. Real implementations will share an
// http.Client at the package level.
package web

import "github.com/johnny1110/evva/internal/tools"

func init() {
	tools.Register(tools.WEB_FETCH, Fetch)
	tools.Register(tools.WEB_SEARCH, Search)
}

// Names lists every tool name this package contributes.
func Names() []tools.ToolName {
	return []tools.ToolName{tools.WEB_FETCH, tools.WEB_SEARCH}
}

var (
	Fetch tools.Tool = tools.NewStub(
		tools.WEB_FETCH,
		"Fetch a URL, convert HTML to markdown, then run a small fast model over the content with a user-supplied prompt. "+
			"Read-only. HTTP auto-upgrades to HTTPS. 15-minute self-cleaning cache. "+
			"WILL FAIL on authenticated URLs (Google Docs, Confluence, Jira, GitHub private) — use a specialized MCP tool for those. "+
			"For GitHub URLs, prefer `gh` CLI via Bash. "+
			"On cross-host redirect, the tool returns the new URL and you must re-call WebFetch with it.",
		`{
			"type":"object",
			"additionalProperties":false,
			"required":["url","prompt"],
			"properties":{
				"url":{"type":"string","format":"uri","description":"The URL to fetch content from. Must be fully-formed and valid."},
				"prompt":{"type":"string","description":"The prompt to run on the fetched (markdown-converted) content."}
			}
		}`,
	)

	Search tools.Tool = tools.NewStub(
		tools.WEB_SEARCH,
		"Search the web and return results as markdown-formatted blocks with links. "+
			"Use for information beyond the model's knowledge cutoff or current events. "+
			"Supports domain include/block lists. "+
			"You MUST include a \"Sources:\" section listing the search-result URLs as markdown hyperlinks "+
			"at the end of any response that uses this tool. "+
			"When searching for current info, use the current year in the query.",
		`{
			"type":"object",
			"additionalProperties":false,
			"required":["query"],
			"properties":{
				"query":{"type":"string","minLength":2,"description":"The search query to use."},
				"allowed_domains":{"type":"array","items":{"type":"string"},"description":"Only include search results from these domains."},
				"blocked_domains":{"type":"array","items":{"type":"string"},"description":"Never include search results from these domains."}
			}
		}`,
	)
)
