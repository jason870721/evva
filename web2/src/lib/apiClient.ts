// Shared API client singleton, wired to the session token. Components/stores
// import `api` from here rather than constructing their own — the token is read
// lazily on each call from the session store (pinia is active by call time).
import { createApi } from './api'
import { useSessionStore } from '../stores/session'

export const api = createApi(() => useSessionStore().token)
