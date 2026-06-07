import { createRouter, createWebHashHistory } from 'vue-router'
import { useSessionStore } from '../stores/session'
import { useSpacesStore } from '../stores/spaces'
import LandingView from '../views/LandingView.vue'
import WorkspaceView from '../views/WorkspaceView.vue'
import BoardView from '../views/BoardView.vue'
import TimelineView from '../views/TimelineView.vue'
import StreamView from '../views/StreamView.vue'
import CompletedView from '../views/CompletedView.vue'
import ThemeProbe from '../views/ThemeProbe.vue'

// Hash history: the service serves the SPA from a plain embedded FileServer with
// no SPA-fallback (web/embed.go), so a deep-link refresh on a real path would
// 404. The hash keeps every URL resolving to index.html while still encoding
// state. URL = state: center view in the path, the inspector in the query
// (?m=<member> | ?t=<taskId>) so focused (stream) and selected (inspector) are
// orthogonal and independently deep-linkable (FE-2 §4).
export const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    { path: '/', name: 'landing', component: LandingView },
    { path: '/probe', name: 'probe', component: ThemeProbe },
    {
      path: '/s/:spaceId',
      component: WorkspaceView,
      children: [
        { path: '', name: 'workspace', redirect: (to) => ({ name: 'board', params: to.params }) },
        { path: 'board', name: 'board', component: BoardView },
        { path: 'timeline', name: 'timeline', component: TimelineView },
        { path: 'stream', name: 'stream', component: StreamView },
        { path: 'stream/:member', name: 'stream-member', component: StreamView },
        { path: 'completed', name: 'completed', component: CompletedView },
      ],
    },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
})

// Guard: probe/landing are open; everything else needs a token; workspace routes
// need a running space (a stopped space has no live agents to stream — same rule
// as v1 SpacePicker).
router.beforeEach(async (to) => {
  if (to.name === 'landing' || to.name === 'probe') return true
  const session = useSessionStore()
  if (!session.authed) return { name: 'landing' }
  const spaceId = to.params.spaceId
  if (spaceId) {
    const spaces = useSpacesStore()
    if (!spaces.list.length) {
      try {
        await spaces.load()
      } catch {
        /* fall through — byId check below redirects to landing */
      }
    }
    const sp = spaces.byId(String(spaceId))
    if (!sp || sp.status === 'stopped') return { name: 'landing' }
  }
  return true
})
