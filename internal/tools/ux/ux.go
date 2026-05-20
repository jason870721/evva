// Package ux hosts user-interaction tools: AskUserQuestion, PushNotification.
package ux

import "github.com/johnny1110/evva/internal/tools"

// Names lists every tool name this package contributes.
func Names() []tools.ToolName {
	return []tools.ToolName{tools.ASK_USER_QUESTION, tools.PUSH_NOTIFICATION}
}

var (
	Notify tools.Tool = tools.NewStub(
		tools.PUSH_NOTIFICATION,
		"Send a desktop notification to the user's terminal (and to their phone if Remote Control is connected). "+
			"Pulls the user's attention away from whatever they're doing — use sparingly. "+
			"Send only when there's a real chance the user has walked away AND something worth coming back for has happened: "+
			"long task finished, build ready, blocker hit that needs their decision. "+
			"Never send for routine progress or a task that completed seconds after they asked. "+
			"Lead with the actionable detail (\"build failed: 2 auth tests\" beats \"task done\"). "+
			"Under 200 chars, one line, no markdown. "+
			"If the result says push wasn't sent, no action needed.",
		`{
			"type":"object",
			"additionalProperties":false,
			"required":["message","status"],
			"properties":{
				"message":{"type":"string","minLength":1,"description":"The notification body. Keep under 200 characters; mobile OSes truncate."},
				"status":{"type":"string","constant":"proactive","description":"Always the literal string \"proactive\"."}
			}
		}`,
	)
)
