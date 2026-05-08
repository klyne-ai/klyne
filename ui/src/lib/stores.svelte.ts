/**
 * stores.svelte.ts — Svelte 5 runes-based application state.
 *
 * All state uses $state runes (Svelte 5 syntax). No external state library.
 * These stores are consumed by W14's business components.
 *
 * Exported values:
 *   sessions         — paginated session list
 *   currentSession   — the session currently being viewed
 *   searchResults    — latest search response (replaced on each new query)
 *   costSummary      — latest cost summary response
 *   settings         — daemon settings (AI model config, detected providers)
 */

import type {
  CostSummaryResponse,
  MsgNew,
  SearchResponse,
  Session,
  SessionResponse,
  SettingsResponse
} from './types.js';

// ---------------------------------------------------------------------------
// sessions
// ---------------------------------------------------------------------------

/** Reactive list of sessions shown in the sidebar / list pane. */
export const sessions = $state<{ items: Session[]; nextBefore: number; loading: boolean }>({
  items: [],
  nextBefore: 0,
  loading: false
});

/** Append a page of sessions (cursor-based pagination). */
export function appendSessions(items: Session[], nextBefore: number): void {
  sessions.items = [...sessions.items, ...items];
  sessions.nextBefore = nextBefore;
}

/** Replace the entire session list (e.g. on first load or filter change). */
export function setSessions(items: Session[], nextBefore: number): void {
  sessions.items = [...items];
  sessions.nextBefore = nextBefore;
}

/** Update a single session in the list (e.g. on SSE SessionUpdate). */
export function patchSession(updated: Partial<Session> & { id: string }): void {
  sessions.items = sessions.items.map((s) =>
    s.id === updated.id ? { ...s, ...updated } : s
  );
  // Also update currentSession if it's the same session.
  if (currentSession.data?.session.id === updated.id) {
    currentSession.data = {
      session: { ...currentSession.data.session, ...updated }
    };
  }
}

/** Add a new session to the top of the list (from SSE MsgNew for an unknown session). */
export function prependSession(session: Session): void {
  sessions.items = [session, ...sessions.items];
}

/** Drop a session from the list and clear it from currentSession if matched. */
export function removeSession(id: string): void {
  sessions.items = sessions.items.filter((s) => s.id !== id);
  if (currentSession.data?.session.id === id) {
    currentSession.data = null;
  }
}

// ---------------------------------------------------------------------------
// currentSession
// ---------------------------------------------------------------------------

/** The session currently being viewed in the main pane. */
export const currentSession = $state<{ data: SessionResponse | null; loading: boolean }>({
  data: null,
  loading: false
});

/** Set the current session (after fetching from the API). */
export function setCurrentSession(data: SessionResponse | null): void {
  currentSession.data = data;
}

// ---------------------------------------------------------------------------
// searchResults
// ---------------------------------------------------------------------------

/** Latest full-text search results. Replaced entirely on each new query. */
export const searchResults = $state<{ data: SearchResponse | null; query: string; loading: boolean }>({
  data: null,
  query: '',
  loading: false
});

/** Replace search results with a new response. */
export function setSearchResults(data: SearchResponse): void {
  searchResults.data = data;
  searchResults.query = data.query;
}

/** Clear search results (e.g. when the search box is emptied). */
export function clearSearchResults(): void {
  searchResults.data = null;
  searchResults.query = '';
}

// ---------------------------------------------------------------------------
// costSummary
// ---------------------------------------------------------------------------

/** Latest cost summary response. */
export const costSummary = $state<{ data: CostSummaryResponse | null; loading: boolean }>({
  data: null,
  loading: false
});

/** Replace the cost summary with a fresh response. */
export function setCostSummary(data: CostSummaryResponse): void {
  costSummary.data = data;
}

// ---------------------------------------------------------------------------
// settings
// ---------------------------------------------------------------------------

/** Daemon settings (AI model config + detected providers). */
export const settings = $state<{ data: SettingsResponse | null; loading: boolean }>({
  data: null,
  loading: false
});

/** Replace settings with a fresh response. */
export function setSettings(data: SettingsResponse): void {
  settings.data = data;
}

// ---------------------------------------------------------------------------
// SSE integration helpers
// ---------------------------------------------------------------------------

/**
 * Handle a MsgNew SSE event: update the relevant session's counters
 * in the sessions list and bump its last_msg_at.
 *
 * Full message fetch is left to the component layer (W14); the store
 * only updates session-level metadata visible in the list.
 */
export function onMsgNew(event: MsgNew): void {
  sessions.items = sessions.items.map((s) => {
    if (s.id !== event.session_id) return s;
    return {
      ...s,
      last_msg_at: event.ts,
      msg_count: s.msg_count + 1,
      cost_usd: s.cost_usd + event.cost_usd,
      tokens_in: s.tokens_in + event.tokens_in,
      tokens_out: s.tokens_out + event.tokens_out,
      model: event.model || s.model
    };
  });
}
