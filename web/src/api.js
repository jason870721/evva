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
    tasks: (id) => req('GET', `/api/tasks?space=${enc(id)}`),
    messages: (id) => req('GET', `/api/messages?space=${enc(id)}`),
    transcript: (id, agent) =>
      req('GET', `/api/agents/${enc(agent)}/transcript?space=${enc(id)}`),

    // commands
    run: (id, agent, prompt) =>
      req('POST', `/api/agents/${enc(agent)}/run?space=${enc(id)}`, { prompt }),
    suspend: (id, agent) => req('POST', `/api/agents/${enc(agent)}/suspend?space=${enc(id)}`),
    resume: (id, agent) => req('POST', `/api/agents/${enc(agent)}/resume?space=${enc(id)}`),
    freeze: (id, agent) => req('POST', `/api/agents/${enc(agent)}/freeze?space=${enc(id)}`),
    unfreeze: (id, agent) => req('POST', `/api/agents/${enc(agent)}/unfreeze?space=${enc(id)}`),
    addMember: (id, agent) => req('POST', `/api/members?space=${enc(id)}`, { agent }),
    halt: (id) => req('POST', `/api/halt?space=${enc(id)}`),
    stopSpace: (id) => req('DELETE', `/api/swarm/${enc(id)}`),
  }
}
