// Package monitor hosts the deferred Monitor tool — a background process
// watcher that streams stdout lines as notifications.
package monitor

import "github.com/johnny1110/evva/internal/tools"

func init() { tools.Register(tools.MONITOR, Monitor) }

// Names lists every tool name this package contributes.
func Names() []tools.ToolName { return []tools.ToolName{tools.MONITOR} }

var Monitor tools.Tool = tools.NewStub(
	tools.MONITOR,
	"Start a background monitor that streams events from a long-running script. "+
		"Each stdout line becomes a notification. Use for per-occurrence events "+
		"(log watchers, file change watchers, poll loops). "+
		"For a single \"tell me when X is done\" notification, prefer Bash with run_in_background + an `until` loop instead. "+
		"Use `grep --line-buffered` in pipes. "+
		"Filter must cover terminal failure states, not just success — silence ≠ success.",
	`{
		"type":"object",
		"additionalProperties":false,
		"required":["description","timeout_ms","persistent","command"],
		"properties":{
			"command":{"type":"string","description":"Shell command or script. Each stdout line is an event; exit ends the watch."},
			"description":{"type":"string","description":"Short human-readable description of what you are monitoring (shown in notifications)."},
			"persistent":{"type":"boolean","default":false,"description":"Run for the lifetime of the session (no timeout). Stop with TaskStop."},
			"timeout_ms":{"type":"number","default":300000,"minimum":1000,"description":"Kill the monitor after this deadline. Default 300000ms, max 3600000ms. Ignored when persistent is true."}
		}
	}`,
)
