// Package cron hosts scheduling tools: CronCreate, CronList, CronDelete,
// RemoteTrigger.
//
// Singletons for stubbing. Real implementations will share a cron service
// via constructor injection.
package cron

import (
	"github.com/johnny1110/evva/pkg/common"
	"github.com/johnny1110/evva/pkg/tools"
)

// Names lists every tool name this package contributes.
func Names() []tools.ToolName {
	return []tools.ToolName{
		tools.CRON_CREATE, tools.CRON_LIST, tools.CRON_DELETE, tools.REMOTE_TRIGGER,
	}
}

var (
	Create tools.Tool = tools.NewStub(
		tools.CRON_CREATE,
		"Schedule a prompt to be enqueued at a future time. Supports recurring (default) and one-shot jobs. "+
			"Uses standard 5-field cron in the user's local timezone ("+common.ZoneLabel()+"): \"M H DoM Mon DoW\". "+
			"Avoid :00 and :30 minute marks when possible — pick off-minutes like 7 or 57 to spread load. "+
			"Recurring jobs auto-expire after 7 days. Jobs only fire while the REPL is idle. "+
			"Session-only by default (use `durable: true` to persist).",
		`{
			"type":"object",
			"additionalProperties":false,
			"required":["cron","prompt"],
			"properties":{
				"cron":{"type":"string","description":"Standard 5-field cron expression in local time: \"M H DoM Mon DoW\" (e.g. \"*/5 * * * *\" = every 5 minutes, \"30 14 28 2 *\" = Feb 28 at 2:30pm local once)."},
				"prompt":{"type":"string","description":"The prompt to enqueue at each fire time."},
				"recurring":{"type":"boolean","description":"true (default) = fire on every cron match until deleted or auto-expired after 7 days. false = fire once at the next match, then auto-delete."},
				"durable":{"type":"boolean","description":"true = persist to .evva/scheduled_tasks.json and survive restarts. false (default) = in-memory only, dies when this session ends."}
			}
		}`,
	)

	List tools.Tool = tools.NewStub(
		tools.CRON_LIST,
		"List all cron jobs scheduled via CronCreate in this session.",
		`{
			"type":"object",
			"additionalProperties":false,
			"properties":{}
		}`,
	)

	Delete tools.Tool = tools.NewStub(
		tools.CRON_DELETE,
		"Cancel a cron job previously scheduled with CronCreate. Removes it from the in-memory session store.",
		`{
			"type":"object",
			"additionalProperties":false,
			"required":["id"],
			"properties":{
				"id":{"type":"string","description":"Job ID returned by CronCreate."}
			}
		}`,
	)

	Trigger tools.Tool = tools.NewStub(
		tools.REMOTE_TRIGGER,
		"Call the remote-trigger API. Use this instead of curl — the OAuth token is added automatically in-process and never exposed. "+
			"Actions: list (GET all), get (GET one), create (POST new — requires body), "+
			"update (POST partial update — requires body), run (POST /run — optional body). "+
			"Returns raw JSON from the API.",
		`{
			"type":"object",
			"additionalProperties":false,
			"required":["action"],
			"properties":{
				"action":{"type":"string","enum":["list","get","create","update","run"],"description":"API operation to perform."},
				"trigger_id":{"type":"string","pattern":"^[\\w-]+$","description":"Required for get, update, and run."},
				"body":{"type":"object","additionalProperties":{},"propertyNames":{"type":"string"},"description":"Required for create and update; optional for run."}
			}
		}`,
	)
)
