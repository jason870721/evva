// Typed-ish REST client over the swarm service API (SPRD-1-8). Every call
// carries the session token as a Bearer header; the token is the one the
// `evva service` daemon printed on start (the user pastes it once — see App.vue).

export function createApi(getToken) {
  async function req(method, path, body) {
    const headers = { Authorization: 'Bearer ' + (getToken() || '') }
    const opts = { method, headers }
    if (body !== undefined) {
      headers['Content-Type'] = 'application/json'
      opts.body = JSON.stringify(body)
    }
    const resp = await fetch(path, opts)
    if (resp.status === 401) throw new Error('unauthorized — check the session token')
    if (!resp.ok) {
      const text = await resp.text().catch(() => '')
      throw new Error(`${method} ${path} → ${resp.status} ${text}`.trim())
    }
    if (resp.status === 204) return null
    const ct = resp.headers.get('Content-Type') || ''
    return ct.includes('application/json') ? resp.json() : resp.text()
  }

  const enc = encodeURIComponent
  return {
    // snapshots
    spaces: () => req('GET', '/api/swarms'),
    roster: (id) => req('GET', `/api/swarm/${enc(id)}`),
    // Board snapshot: { tasks: [active… + newest-5 completed], total: <completed count> }.
    tasks: (id) => req('GET', `/api/tasks?space=${enc(id)}`),
    // On-demand paged view of one status (the Completed tab): { tasks, total }.
    tasksPage: (id, { status, limit, offset } = {}) => {
      const p = new URLSearchParams({ space: id })
      if (status) p.set('status', status)
      if (limit != null) p.set('limit', String(limit))
      if (offset != null) p.set('offset', String(offset))
      return req('GET', `/api/tasks?${p.toString()}`)
    },
    messages: (id) => req('GET', `/api/messages?space=${enc(id)}`),
    transcript: (id, agent) =>
      req('GET', `/api/agents/${enc(agent)}/transcript?space=${enc(id)}`),
    // Outstanding approval/question gates (raw event shape) — re-rendered on
    // (re)connect so a member blocked before we connected isn't left hung.
    pending: (id) => req('GET', `/api/swarm/${enc(id)}/pending`),

    // commands
    run: (id, agent, prompt) =>
      req('POST', `/api/agents/${enc(agent)}/run?space=${enc(id)}`, { prompt }),
    // Operator → member message (flat comms). `to` may be "all".
    message: (id, to, body, subject) =>
      req('POST', `/api/agents/${enc(to)}/message?space=${enc(id)}`, { body, subject }),
    suspend: (id, agent) => req('POST', `/api/agents/${enc(agent)}/suspend?space=${enc(id)}`),
    resume: (id, agent) => req('POST', `/api/agents/${enc(agent)}/resume?space=${enc(id)}`),
    freeze: (id, agent) => req('POST', `/api/agents/${enc(agent)}/freeze?space=${enc(id)}`),
    unfreeze: (id, agent) => req('POST', `/api/agents/${enc(agent)}/unfreeze?space=${enc(id)}`),
    addMember: (id, agent) => req('POST', `/api/members?space=${enc(id)}`, { agent }),
    halt: (id) => req('POST', `/api/halt?space=${enc(id)}`),
    // Lifecycle (ref = id or name): stop KEEPS the space (run restarts it);
    // removeSpace forgets it entirely.
    stopSpace: (ref) => req('POST', `/api/swarm/${enc(ref)}/stop`),
    runSpace: (ref) => req('POST', `/api/swarm/${enc(ref)}/run`),
    removeSpace: (ref) => req('DELETE', `/api/swarm/${enc(ref)}`),
    // Wipe the space (ledger + every agent's context) and rebuild it under the
    // same id; returns { id }. Destructive — the caller should confirm first.
    reset: (id) => req('POST', `/api/swarm/${enc(id)}/reset`),
  }
}
