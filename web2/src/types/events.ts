// Event types — mirror pkg/event/event.go Kind constants (event.go:34-155) and
// the marshalled wire shape (PascalCase fields + one populated payload pointer
// named after its kind). The service wraps each as {spaceId, event}; ws.ts
// unwraps to the inner WireEvent.

export type EventKind =
  | 'idle'
  | 'run_start'
  | 'run_resume'
  | 'run_end'
  | 'run_cancelled'
  | 'iter_limit'
  | 'turn_start'
  | 'turn_end'
  | 'thinking'
  | 'thinking_chunk'
  | 'text'
  | 'text_chunk'
  | 'tool_use_start'
  | 'tool_use_result'
  | 'approval_needed'
  | 'question_needed'
  | 'compacting'
  | 'compacting_end'
  | 'error'
  | 'store_update'
  | 'usage'
  | 'mode_changed'
  | 'bg_result'
  | 'monitor_event'
  | 'drain_background_task'
  | 'drain_monitor_events'
  | 'drain_inbox'

export interface QuestionOption {
  Label: string
  Description?: string
}
export interface QuestionItem {
  Question: string
  Header?: string
  Options?: QuestionOption[]
  MultiSelect?: boolean
}

export interface ApprovalPayload {
  RequestID?: string
  ToolName?: string
  InputDescription?: string
  Reason?: string
  RiskHint?: string
  PlanContent?: string
  ToolInput?: unknown
}
export interface QuestionPayload {
  RequestID?: string
  AgentID?: string
  Questions?: QuestionItem[]
}

// WireEvent — the inner event. Payload pointers are optional; only the one
// matching Kind is populated. Kept permissive (all fields optional bar Kind) so
// the reducers can switch on Kind and read just the payload they need.
export interface WireEvent {
  Kind: EventKind
  AgentID?: string
  Time?: string
  Text?: { Text?: string } | null
  Thinking?: { Text?: string } | null
  ToolUseStart?: { Name?: string; ToolID?: string; Input?: unknown } | null
  ToolUseResult?: { ToolID?: string; IsError?: boolean; Content?: string } | null
  Error?: { Message?: string } | null
  ApprovalNeeded?: ApprovalPayload | null
  QuestionNeeded?: QuestionPayload | null
}

// CommandErrorFrame — service-layer frame (NOT an event) pushed back when an
// inbound WS command fails to route (api.go:586). type distinguishes it from the
// event envelope so the reducers ignore it.
export interface CommandErrorFrame {
  type: 'command_error'
  reqId?: string
  message?: string
}
