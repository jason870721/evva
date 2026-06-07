// buildTimeline merges the available timestamped sources into one cross-member
// activity feed (FE-5), newest first. Pure + framework-free (node --test).
//
// Sources wired now: inter-agent/operator messages, and task lifecycle (created +
// the latest transition) derived from the board snapshot. Gate / membership /
// schedule / external-event entries are owned by FE-6/FE-7 and can append to this
// shape as those features land — the TimelineItem union is the seam.
import type { MessageInfo, TaskInfo } from './../types/wire'

export interface TimelineItem {
  id: string
  kind: 'message' | 'task'
  time: number
  sender: string
  recipient?: string
  title: string
  taskId?: number
  status?: string
  refTask?: number
}

export function buildTimeline(messages: MessageInfo[], tasks: TaskInfo[], limit = 80): TimelineItem[] {
  const items: TimelineItem[] = []
  for (const m of messages || []) {
    items.push({
      id: 'm' + m.id,
      kind: 'message',
      time: m.createdAt,
      sender: m.sender,
      recipient: m.recipient,
      title: m.subject || m.body,
      refTask: m.refTask,
    })
  }
  for (const t of tasks || []) {
    items.push({
      id: 'tc' + t.id,
      kind: 'task',
      time: t.createdAt,
      sender: t.createdBy,
      title: `created #${t.id} ${t.title}`,
      taskId: t.id,
      status: 'pending',
    })
    if (t.updatedAt && t.updatedAt !== t.createdAt) {
      items.push({
        id: 'tu' + t.id,
        kind: 'task',
        time: t.updatedAt,
        sender: t.assignee || t.createdBy,
        title: `#${t.id} → ${t.status}`,
        taskId: t.id,
        status: t.status,
      })
    }
  }
  items.sort((a, b) => b.time - a.time)
  return items.slice(0, limit)
}
