// WebSocket bridge to the swarm service. Subscribes to one space (the service
// fans out only that space's events, keyed by (spaceID, AgentID) — SPRD-1-8),
// streams parsed wire events to onEvent, and exposes the inbound command
// channel the Leader Chat / approval overlays use (run, respond_permission,
// respond_question).
//
// It reconnects with a small backoff so a daemon restart or a transient drop
// doesn't strand the UI.

export function openSocket({ token, spaceId, onEvent, onStatus }) {
  let ws = null
  let closed = false
  let backoff = 500

  const url = () => {
    const proto = location.protocol === 'https:' ? 'wss' : 'ws'
    const t = encodeURIComponent(token || '')
    const s = encodeURIComponent(spaceId)
    return `${proto}://${location.host}/ws?space=${s}&token=${t}`
  }

  function connect() {
    if (closed) return
    onStatus && onStatus('connecting')
    ws = new WebSocket(url())

    ws.onopen = () => {
      backoff = 500
      onStatus && onStatus('open')
    }
    ws.onmessage = (m) => {
      let parsed
      try {
        parsed = JSON.parse(m.data)
      } catch {
        return
      }
      // The service sends {spaceId, event}; pass the inner event through.
      onEvent && onEvent(parsed.event || parsed, parsed.spaceId)
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
    send(cmd) {
      if (ws && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify(cmd))
    },
    close() {
      closed = true
      if (ws) ws.close()
    },
  }
}
