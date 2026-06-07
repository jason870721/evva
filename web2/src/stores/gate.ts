import { defineStore } from 'pinia'
import { isApproval, isQuestion, approvalOf, questionOf, type ApprovalVM, type QuestionVM } from '../lib/events'
import { api } from '../lib/apiClient'
import { useConnectionStore } from './connection'

// Reply shapes the gate UI (FE-6) emits.
export interface PermissionReply {
  agent: string
  reqId: string
  behavior: string
  reason?: string
  ruleTool?: string
}
// answers is map[question]→chosen labels, matching the backend wire shape
// (webapi wsCommand.Answers map[string][]string). Single-select is a one-element
// array; multi-select carries each chosen label; "Other" carries the typed text.
export interface QuestionReply {
  agent: string
  reqId: string
  answers: Record<string, string[]>
}

// gate holds the pending approval/question QUEUES (not single slots — several
// members can block at once, RP-2 §3.2). Enqueue is de-duped by (agentId,
// requestId). `errors` maps a requestId → the last send failure, so a reply that
// failed to route shows on THAT card (RP-2 §3.3 / RP-4 H14) instead of a global
// red line.
export const useGateStore = defineStore('gate', {
  state: () => ({
    approvals: [] as ApprovalVM[],
    questions: [] as QuestionVM[],
    errors: {} as Record<string, string>,
  }),
  getters: {
    pendingCount: (s): number => s.approvals.length + s.questions.length,
    headApproval: (s): ApprovalVM | null => s.approvals[0] || null,
    headQuestion: (s): QuestionVM | null => s.questions[0] || null,
  },
  actions: {
    enqueue(kind: 'approval' | 'question', g: ApprovalVM | QuestionVM) {
      if (kind === 'approval') {
        const a = g as ApprovalVM
        if (!this.approvals.some((x) => x.agentId === a.agentId && x.requestId === a.requestId)) {
          this.approvals = [...this.approvals, a]
        }
      } else {
        const q = g as QuestionVM
        if (!this.questions.some((x) => x.agentId === q.agentId && x.requestId === q.requestId)) {
          this.questions = [...this.questions, q]
        }
      }
    },
    noteError(reqId: string, msg: string) {
      if (reqId) this.errors = { ...this.errors, [reqId]: msg }
    },
    clearError(reqId: string) {
      if (this.errors[reqId]) {
        const e = { ...this.errors }
        delete e[reqId]
        this.errors = e
      }
    },
    // Move the matching member's gate to the head of its queue (Attention "act"
    // chip → deal with this one now). Returns whether a gate was found.
    bringToFront(agentId: string): boolean {
      const ai = this.approvals.findIndex((x) => x.agentId === agentId)
      if (ai > 0) {
        const [g] = this.approvals.splice(ai, 1)
        this.approvals = [g, ...this.approvals]
      }
      const qi = this.questions.findIndex((x) => x.agentId === agentId)
      if (qi > 0) {
        const [g] = this.questions.splice(qi, 1)
        this.questions = [g, ...this.questions]
      }
      return ai >= 0 || qi >= 0
    },
    respondPermission(d: PermissionReply) {
      this.clearError(d.reqId)
      useConnectionStore().send({
        type: 'respond_permission',
        agent: d.agent,
        reqId: d.reqId,
        behavior: d.behavior,
        reason: d.reason || '',
        ruleTool: d.ruleTool || '',
      })
      this.approvals = this.approvals.filter((x) => !(x.agentId === d.agent && x.requestId === d.reqId))
    },
    respondQuestion(d: QuestionReply) {
      this.clearError(d.reqId)
      useConnectionStore().send({ type: 'respond_question', agent: d.agent, reqId: d.reqId, answers: d.answers })
      this.questions = this.questions.filter((x) => !(x.agentId === d.agent && x.requestId === d.reqId))
    },
    // Re-pull outstanding gates and enqueue them de-duped, so a member blocked
    // before/while we were disconnected stays answerable (RP-2 §3.3).
    async hydrate() {
      const id = useConnectionStore().spaceId
      if (!id) return
      let evs
      try {
        evs = await api.pending(id)
      } catch {
        return
      }
      for (const ev of evs || []) {
        if (isApproval(ev)) this.enqueue('approval', approvalOf(ev))
        else if (isQuestion(ev)) this.enqueue('question', questionOf(ev))
      }
    },
    reset() {
      this.approvals = []
      this.questions = []
      this.errors = {}
    },
  },
})
