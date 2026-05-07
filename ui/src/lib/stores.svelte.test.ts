/**
 * stores.svelte.test.ts — Unit tests for Svelte 5 runes-based stores.
 *
 * Tests:
 *   TestSessionsStore_UpdatesOnMsgNew        — onMsgNew mutates session counters
 *   TestSearchResultsStore_ReplacesOnNewQuery — setSearchResults replaces data wholesale
 *   TestSessions_AppendAndSet               — pagination helpers
 *   TestCurrentSession_SetAndClear          — setCurrentSession / setCurrentSession(null)
 *   TestCostSummary_Set                     — setCostSummary replaces data
 *   TestSettings_Set                        — setSettings replaces data
 *   TestPatchSession_UpdatesList            — patchSession updates matching entry
 */

import { describe, it, expect, beforeEach } from 'vitest';
import {
  sessions,
  currentSession,
  searchResults,
  costSummary,
  settings,
  appendSessions,
  setSessions,
  setCurrentSession,
  setSearchResults,
  clearSearchResults,
  setCostSummary,
  setSettings,
  patchSession,
  onMsgNew,
  prependSession
} from './stores.svelte.js';
import type { Session, SessionResponse, SearchResponse, CostSummaryResponse, SettingsResponse, MsgNew } from './types.js';

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

function makeSession(id: string, overrides?: Partial<Session>): Session {
  return {
    id,
    cli: 'claude',
    project_path: '/projects/test',
    encoded_cwd: '',
    started_at: 1700000000000,
    last_msg_at: 1700000001000,
    msg_count: 5,
    tokens_in: 100,
    tokens_out: 200,
    cached_read_tokens: 0,
    cached_write_tokens: 0,
    cost_usd: 0.01,
    model: 'claude-sonnet-4.5',
    status: 'idle',
    raw_path: '',
    ...overrides
  };
}

function makeMsgNew(sessionId: string, overrides?: Partial<MsgNew>): MsgNew {
  return {
    session_id: sessionId,
    message_id: 'msg-new',
    ts: 1700000002000,
    role: 'assistant',
    model: 'claude-sonnet-4.5',
    tokens_in: 50,
    tokens_out: 100,
    cost_usd: 0.005,
    ...overrides
  };
}

// ---------------------------------------------------------------------------
// Reset store state before each test
// ---------------------------------------------------------------------------

beforeEach(() => {
  setSessions([], 0);
  setCurrentSession(null);
  clearSearchResults();
  // Reset costSummary and settings to initial state
  costSummary.data = null;
  costSummary.loading = false;
  settings.data = null;
  settings.loading = false;
  sessions.loading = false;
  currentSession.loading = false;
  searchResults.loading = false;
});

// ---------------------------------------------------------------------------
// TestSessionsStore_UpdatesOnMsgNew
// ---------------------------------------------------------------------------

describe('TestSessionsStore_UpdatesOnMsgNew', () => {
  it('increments msg_count and cost_usd for matching session', () => {
    const sess = makeSession('sess-1', { msg_count: 5, cost_usd: 0.01, tokens_in: 100, tokens_out: 200 });
    setSessions([sess], 0);

    const event = makeMsgNew('sess-1', { tokens_in: 50, tokens_out: 100, cost_usd: 0.005 });
    onMsgNew(event);

    const updated = sessions.items.find((s) => s.id === 'sess-1');
    expect(updated).toBeDefined();
    expect(updated!.msg_count).toBe(6);
    expect(updated!.cost_usd).toBeCloseTo(0.015);
    expect(updated!.tokens_in).toBe(150);
    expect(updated!.tokens_out).toBe(300);
  });

  it('updates last_msg_at to event ts', () => {
    setSessions([makeSession('sess-1')], 0);
    onMsgNew(makeMsgNew('sess-1', { ts: 1700000099999 }));
    const updated = sessions.items.find((s) => s.id === 'sess-1');
    expect(updated!.last_msg_at).toBe(1700000099999);
  });

  it('does not modify other sessions', () => {
    setSessions([makeSession('sess-1'), makeSession('sess-2')], 0);
    onMsgNew(makeMsgNew('sess-1'));
    const other = sessions.items.find((s) => s.id === 'sess-2');
    expect(other!.msg_count).toBe(5);
  });

  it('is a no-op when session id is not found', () => {
    setSessions([makeSession('sess-1')], 0);
    const before = sessions.items[0].msg_count;
    onMsgNew(makeMsgNew('sess-UNKNOWN'));
    expect(sessions.items[0].msg_count).toBe(before);
  });
});

// ---------------------------------------------------------------------------
// TestSearchResultsStore_ReplacesOnNewQuery
// ---------------------------------------------------------------------------

describe('TestSearchResultsStore_ReplacesOnNewQuery', () => {
  it('replaces previous results on a new query', () => {
    const first: SearchResponse = {
      query: 'foo',
      hits: [
        {
          message_id: 'm1',
          session_id: 's1',
          cli: 'claude',
          project_path: '',
          role: 'user',
          snippet: 'foo bar',
          score: 0.9,
          ts: 1700000000000
        }
      ],
      took_ms: 5
    };
    setSearchResults(first);
    expect(searchResults.data!.hits).toHaveLength(1);
    expect(searchResults.query).toBe('foo');

    const second: SearchResponse = { query: 'bar', hits: [], took_ms: 2 };
    setSearchResults(second);
    expect(searchResults.data!.hits).toHaveLength(0);
    expect(searchResults.query).toBe('bar');
  });

  it('clearSearchResults empties the store', () => {
    setSearchResults({ query: 'test', hits: [], took_ms: 1 });
    clearSearchResults();
    expect(searchResults.data).toBeNull();
    expect(searchResults.query).toBe('');
  });
});

// ---------------------------------------------------------------------------
// TestSessions_AppendAndSet
// ---------------------------------------------------------------------------

describe('TestSessions_AppendAndSet', () => {
  it('setSessions replaces items completely', () => {
    setSessions([makeSession('a'), makeSession('b')], 100);
    expect(sessions.items).toHaveLength(2);

    setSessions([makeSession('c')], 50);
    expect(sessions.items).toHaveLength(1);
    expect(sessions.items[0].id).toBe('c');
    expect(sessions.nextBefore).toBe(50);
  });

  it('appendSessions adds to existing items', () => {
    setSessions([makeSession('a')], 100);
    appendSessions([makeSession('b'), makeSession('c')], 50);
    expect(sessions.items).toHaveLength(3);
    expect(sessions.nextBefore).toBe(50);
  });

  it('prependSession adds to the front', () => {
    setSessions([makeSession('b')], 0);
    prependSession(makeSession('a'));
    expect(sessions.items[0].id).toBe('a');
    expect(sessions.items).toHaveLength(2);
  });
});

// ---------------------------------------------------------------------------
// TestCurrentSession_SetAndClear
// ---------------------------------------------------------------------------

describe('TestCurrentSession_SetAndClear', () => {
  it('setCurrentSession stores the response', () => {
    const resp: SessionResponse = { session: makeSession('sess-1') };
    setCurrentSession(resp);
    expect(currentSession.data).not.toBeNull();
    expect(currentSession.data!.session.id).toBe('sess-1');
  });

  it('setCurrentSession(null) clears the store', () => {
    setCurrentSession({ session: makeSession('sess-1') });
    setCurrentSession(null);
    expect(currentSession.data).toBeNull();
  });
});

// ---------------------------------------------------------------------------
// TestCostSummary_Set
// ---------------------------------------------------------------------------

describe('TestCostSummary_Set', () => {
  it('setCostSummary stores the response', () => {
    const data: CostSummaryResponse = {
      group: 'day',
      since: 0,
      until: 0,
      buckets: [],
      total: { key: 'total', tokens_in: 1000, tokens_out: 2000, cost_usd: 0.1, count: 5 }
    };
    setCostSummary(data);
    expect(costSummary.data).not.toBeNull();
    expect(costSummary.data!.total.cost_usd).toBeCloseTo(0.1);
  });
});

// ---------------------------------------------------------------------------
// TestSettings_Set
// ---------------------------------------------------------------------------

describe('TestSettings_Set', () => {
  it('setSettings stores the response', () => {
    const data: SettingsResponse = {
      ai: {
        summary_model: { provider: 'anthropic', model: 'claude-sonnet-4.5' },
        title_model: { provider: 'anthropic', model: 'claude-haiku-4' },
        embed_model: { provider: 'openai', model: 'text-embedding-3-small' }
      },
      detected: { anthropic: true, openai: true, gemini: false, ollama: false }
    };
    setSettings(data);
    expect(settings.data).not.toBeNull();
    expect(settings.data!.detected.anthropic).toBe(true);
  });
});

// ---------------------------------------------------------------------------
// TestPatchSession_UpdatesList
// ---------------------------------------------------------------------------

describe('TestPatchSession_UpdatesList', () => {
  it('patchSession updates the matching session', () => {
    setSessions([makeSession('sess-1', { status: 'active' }), makeSession('sess-2')], 0);
    patchSession({ id: 'sess-1', status: 'compacted', cost_usd: 99.99 });

    const updated = sessions.items.find((s) => s.id === 'sess-1');
    expect(updated!.status).toBe('compacted');
    expect(updated!.cost_usd).toBeCloseTo(99.99);
  });

  it('patchSession also updates currentSession if id matches', () => {
    const sess = makeSession('sess-1');
    setCurrentSession({ session: sess });
    setSessions([sess], 0);

    patchSession({ id: 'sess-1', status: 'idle' });
    expect(currentSession.data!.session.status).toBe('idle');
  });

  it('patchSession does not touch other sessions', () => {
    setSessions([makeSession('sess-1'), makeSession('sess-2', { status: 'active' })], 0);
    patchSession({ id: 'sess-1', status: 'compacted' });
    const other = sessions.items.find((s) => s.id === 'sess-2');
    expect(other!.status).toBe('active');
  });
});
