package configtool

import "strings"

const description = "Get or set evva configuration settings."

const usage = `## Usage
- Get current value: omit the "value" parameter.
- Set new value: include the "value" parameter.

To change the model, ask the user to type /model — that's the supported swap
path. To change permission mode, ask the user to press Shift+Tab.

## Examples
- Get: {"setting":"display_thinking"}
- Set bool: {"setting":"display_thinking","value":false}
- Set int: {"setting":"max_iterations","value":40}
- Set string: {"setting":"default_effort","value":"high"}
- Set provider key: {"setting":"openai.api_key","value":"sk-..."}
`

// generatePrompt returns the body the tool exposes via Description(). It
// walks the registry in sorted key order so the output is deterministic
// across calls — identical bytes every time, which keeps the provider's
// prompt-prefix cache warm. Do not introduce any per-call variation here
// (timestamps, map iteration order, …) or caching breaks.
func generatePrompt() string {
	var b strings.Builder
	b.WriteString(description)
	b.WriteString("\n\nUse when the user requests a configuration change, asks about a current setting, or when changing a setting would benefit them.\n\n")
	b.WriteString(usage)
	b.WriteString("\n## Configurable settings\n\n")

	for _, key := range AllKeys() {
		sc := SUPPORTED_SETTINGS[key]
		line := "- " + key
		if len(sc.Options) > 0 {
			opts := make([]string, len(sc.Options))
			for i, o := range sc.Options {
				opts[i] = `"` + o + `"`
			}
			line += ": " + strings.Join(opts, ", ")
		} else {
			switch sc.Type {
			case TypeBool:
				line += ": true/false"
			case TypeInt:
				line += ": <integer>"
			case TypeFloat:
				line += ": <float>"
			case TypeSecret:
				line += ": <secret string>"
			case TypeString:
				line += ": <string>"
			}
		}
		line += " — " + sc.Description
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}
