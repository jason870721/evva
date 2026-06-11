import type { ProposalInfo } from '../types/wire'

// splitProposals separates the service's oldest-first proposal list into the
// open review queue (kept oldest-first — the order the leader should look at
// them) and the decided history (newest decision first, the audit trail).
export function splitProposals(list: ProposalInfo[] | null | undefined): {
  open: ProposalInfo[]
  decided: ProposalInfo[]
} {
  const open: ProposalInfo[] = []
  const decided: ProposalInfo[] = []
  for (const p of list || []) (p.status === 'open' ? open : decided).push(p)
  decided.sort((a, b) => (b.decidedAt || b.createdAt) - (a.decidedAt || a.createdAt))
  return { open, decided }
}

// countOpen is the roster-badge number: how many proposals await the leader.
export function countOpen(list: ProposalInfo[] | null | undefined): number {
  let n = 0
  for (const p of list || []) if (p.status === 'open') n++
  return n
}
