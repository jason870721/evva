package constant

type AgentStatus string

const (
	INIT         AgentStatus = "init"
	DRAINING     AgentStatus = "draining"
	THINKING     AgentStatus = "thinking"
	TEXTING      AgentStatus = "texting"
	EXECUTING    AgentStatus = "executing"
	INTERRUPTED  AgentStatus = "interrupted"
	IDLE         AgentStatus = "idle"
	MAX_ITERS    AgentStatus = "max_iters"
	SAVING       AgentStatus = "saving"
	COMPACTING   AgentStatus = "compacting"
	READY_REPORT AgentStatus = "ready_report"
	SHUTDOWN     AgentStatus = "shutdown"
	CRUSHED      AgentStatus = "crushed"
)

func (a AgentStatus) String() string {
	return string(a)
}
