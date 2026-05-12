// Package shell hosts the active Bash tool. Process-monitoring lives under
// deferred/monitor instead — see docs/claude-tool/claude-code-tool-summary.md.
package shell

import "github.com/johnny1110/evva/internal/tools"

func init() { tools.Register(tools.BASH, Bash) }

// Names lists every tool name this package contributes.
// Monitor is intentionally elsewhere — under deferred/monitor.
func Names() []tools.ToolName { return []tools.ToolName{tools.BASH} }

var Bash tools.Tool = tools.NewStub(
	tools.BASH,
	"Executes a given bash command and returns its output.\n\n"+
		"The working directory persists between commands, but shell state does not. "+
		"The shell environment is initialized from the user's profile (bash or zsh).\n\n"+
		"Prefer dedicated tools when one fits: Read for known paths, Edit for edits, Write for new files. "+
		"Reserve Bash for shell-only operations.\n\n"+
		"Supports: optional timeout (max 600000 ms), background execution via run_in_background, "+
		"and a dangerouslyDisableSandbox escape hatch.\n\n"+
		"Includes detailed protocols for safe git commits, PR creation via gh, "+
		"and avoiding destructive operations without explicit user consent.",
	`{
		"type":"object",
		"additionalProperties":false,
		"required":["command"],
		"properties":{
			"command":{"type":"string","description":"The command to execute"},
			"description":{"type":"string","description":"Clear, concise description of what this command does in active voice."},
			"timeout":{"type":"number","description":"Optional timeout in milliseconds (max 600000)"},
			"run_in_background":{"type":"boolean","description":"Set to true to run this command in the background. Use Read to read the output later."},
			"dangerouslyDisableSandbox":{"type":"boolean","description":"Set this to true to dangerously override sandbox mode and run commands without sandboxing."}
		}
	}`,
)
