// WebSocket bridge to the swarm service — ported from web/src/ws.js. Subscribes
// to one space, streams parsed wire events to onEvent, and exposes the inbound
// command channel (run / respond_permission / respond_question). Reconnects with
// a small backoff so a daemon restart or transient drop doesn't strand the UI.

import type { WireEvent } from '../types/events'

export type WsStatus = 'connecting' | 'open' | 'closed'

export interface SocketHandlers {
  token: string
  spaceId: string
  onEvent: (ev: WireEvent, spaceId?: string) => void
  onStatus?: (s: WsStatus) => void
}

export interface Socket {
  send(cmd: unknown): void
  close(): void
}

export function openSocket({ token, spaceId, onEvent, onStatus }: SocketHandlers): Socket {
  let ws: WebSocket | null = null
  let closed = false
  let backoff = 500

  const url = (): string => {
    const proto = location.protocol === 'https:' ? 'wss' : 'ws'
    const t = encodeURIComponent(token || '')
    const s = encodeURIComponent(spaceId)
    return `${proto}://${location.host}/ws?space=${s}&token=${t}`
  }

  function connect(): void {
    if (closed) return
    onStatus && onStatus('connecting')
    ws = new WebSocket(url())

    ws.onopen = () => {
      backoff = 500
      onStatus && onStatus('open')
    }
    ws.onmessage = (m: MessageEvent) => {
      let parsed: { event?: WireEvent; spaceId?: string } & Partial<WireEvent>
      try {
        parsed = JSON.parse(m.data)
      } catch {
        return
      }
      // The service sends {spaceId, event}; pass the inner event through.
      onEvent((parsed.event ?? (parsed as WireEvent)), parsed.spaceId)
    }
    ws.onclose = () => {
      onStatus && onStatus('closed')
      if (closed) return
      setTimeout(connect, backoff)
      backoff = Math.min(backoff * 2, 5000)
    }
    ws.onerror = () => ws && ws.close()
  }

  connect()

  return {
    send(cmd: unknown) {
      if (ws && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify(cmd))
    },
    close() {
      closed = true
      if (ws) ws.close()
    },
  }
}
