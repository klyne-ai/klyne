/**
 * api.test.ts — Unit tests for the typed fetch wrappers.
 *
 * Tests:
 *   TestFetchSessions_HappyPath     — mock fetch; assert response typed
 *   TestFetchSessions_4xx_Throws    — mock 400; assert thrown ApiError
 *   TestSearch_QueryEncoded         — assert q param is URL-encoded
 *   TestFetchSession_HappyPath      — single session fetch
 *   TestFetchMessages_HappyPath     — paginated messages fetch
 *   TestFetchHealthz_HappyPath      — healthz endpoint
 *   TestUpdateSettings_HappyPath    — PUT /settings
 *   TestFetchCostSummary_HappyPath  — cost summary with query params
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { ApiError, fetchSessions, search, fetchSession, fetchMessages, fetchHealthz, fetchCostSummary } from './api.js';
import type { SessionListResponse, SessionResponse, MessageListResponse, HealthzResponse, CostSummaryResponse } from './types.js';

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function mockFetch(status: number, body: unknown): void {
  const response = new Response(
    status === 204 ? null : JSON.stringify(body),
    {
      status,
      headers: { 'Content-Type': 'application/json' }
    }
  );
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response));
}

function mockFetchFn(fn: (url: string, init?: RequestInit) => Promise<Response>): void {
  vi.stubGlobal('fetch', vi.fn(fn));
}

// ---------------------------------------------------------------------------
// TestFetchSessions_HappyPath
// ---------------------------------------------------------------------------

describe('TestFetchSessions_HappyPath', () => {
  beforeEach(() => {
    const payload: SessionListResponse = {
      sessions: [
        {
          id: 'sess-1',
          cli: 'claude',
          project_path: '/projects/foo',
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
          raw_path: ''
        }
      ],
      next_before: 0
    };
    mockFetch(200, payload);
  });

  afterEach(() => vi.unstubAllGlobals());

  it('returns a typed SessionListResponse', async () => {
    const result = await fetchSessions();
    expect(result.sessions).toHaveLength(1);
    expect(result.sessions[0].id).toBe('sess-1');
    expect(result.sessions[0].cli).toBe('claude');
    expect(result.next_before).toBe(0);
  });

  it('calls the correct URL', async () => {
    await fetchSessions();
    const url = (vi.mocked(fetch).mock.calls[0][0] as string);
    expect(url).toContain('/sessions');
  });

  it('passes query params correctly', async () => {
    await fetchSessions({ cli: 'claude', limit: 20, before: 1700000000000 });
    const url = (vi.mocked(fetch).mock.calls[0][0] as string);
    expect(url).toContain('cli=claude');
    expect(url).toContain('limit=20');
    expect(url).toContain('before=1700000000000');
  });
});

// ---------------------------------------------------------------------------
// TestFetchSessions_4xx_Throws
// ---------------------------------------------------------------------------

describe('TestFetchSessions_4xx_Throws', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('throws ApiError with correct status on 400', async () => {
    mockFetch(400, { error: 'bad request' });
    let thrown: unknown;
    try {
      await fetchSessions();
    } catch (e) {
      thrown = e;
    }
    expect(thrown).toBeInstanceOf(ApiError);
    expect((thrown as ApiError).status).toBe(400);
  });

  it('throws ApiError with correct status on 500', async () => {
    mockFetch(500, { error: 'internal server error' });
    let thrown: unknown;
    try {
      await fetchSessions();
    } catch (e) {
      thrown = e;
    }
    expect(thrown).toBeInstanceOf(ApiError);
    expect((thrown as ApiError).status).toBe(500);
  });

  it('includes the response body in ApiError', async () => {
    const errorBody = { error: 'not found' };
    mockFetch(404, errorBody);
    try {
      await fetchSessions();
      expect.fail('Should have thrown');
    } catch (e) {
      expect(e).toBeInstanceOf(ApiError);
      expect((e as ApiError).body).toEqual(errorBody);
    }
  });
});

// ---------------------------------------------------------------------------
// TestSearch_QueryEncoded
// ---------------------------------------------------------------------------

describe('TestSearch_QueryEncoded', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('URL-encodes the query parameter', async () => {
    mockFetch(200, { query: 'hello world', hits: [], took_ms: 1 });
    await search('hello world');
    const url = (vi.mocked(fetch).mock.calls[0][0] as string);
    expect(url).toContain('q=hello+world');
  });

  it('includes limit parameter when provided', async () => {
    mockFetch(200, { query: 'foo', hits: [], took_ms: 1 });
    await search('foo', 25);
    const url = (vi.mocked(fetch).mock.calls[0][0] as string);
    expect(url).toContain('limit=25');
  });

  it('handles special characters in query', async () => {
    mockFetch(200, { query: 'foo & bar', hits: [], took_ms: 1 });
    await search('foo & bar');
    const url = (vi.mocked(fetch).mock.calls[0][0] as string);
    // URLSearchParams encodes & as %26
    expect(url).toContain('q=foo+%26+bar');
  });
});

// ---------------------------------------------------------------------------
// TestFetchSession_HappyPath
// ---------------------------------------------------------------------------

describe('TestFetchSession_HappyPath', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('returns a typed SessionResponse', async () => {
    const payload: SessionResponse = {
      session: {
        id: 'sess-abc',
        cli: 'codex',
        project_path: '/projects/bar',
        encoded_cwd: '',
        started_at: 0,
        last_msg_at: 0,
        msg_count: 0,
        tokens_in: 0,
        tokens_out: 0,
        cached_read_tokens: 0,
        cached_write_tokens: 0,
        cost_usd: 0,
        model: '',
        status: 'active',
        raw_path: ''
      }
    };
    mockFetch(200, payload);
    const result = await fetchSession('sess-abc');
    expect(result.session.id).toBe('sess-abc');
    expect(result.session.cli).toBe('codex');
  });

  it('URL-encodes the session id', async () => {
    mockFetch(200, { session: { id: 'a/b', cli: 'claude', project_path: '', encoded_cwd: '', started_at: 0, last_msg_at: 0, msg_count: 0, tokens_in: 0, tokens_out: 0, cached_read_tokens: 0, cached_write_tokens: 0, cost_usd: 0, model: '', status: 'idle', raw_path: '' } });
    await fetchSession('a/b');
    const url = (vi.mocked(fetch).mock.calls[0][0] as string);
    expect(url).toContain('a%2Fb');
  });
});

// ---------------------------------------------------------------------------
// TestFetchMessages_HappyPath
// ---------------------------------------------------------------------------

describe('TestFetchMessages_HappyPath', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('returns a typed MessageListResponse', async () => {
    const payload: MessageListResponse = {
      messages: [
        {
          id: 'msg-1',
          session_id: 'sess-1',
          cli: 'claude',
          project_path: '',
          role: 'user',
          content: 'Hello',
          tokens_in: 10,
          tokens_out: 0,
          cached_read_tokens: 0,
          cached_write_tokens: 0,
          cost_usd: 0,
          model: '',
          ts: 1700000000000
        }
      ],
      next_before: 0
    };
    mockFetch(200, payload);
    const result = await fetchMessages('sess-1');
    expect(result.messages).toHaveLength(1);
    expect(result.messages[0].role).toBe('user');
  });
});

// ---------------------------------------------------------------------------
// TestFetchHealthz_HappyPath
// ---------------------------------------------------------------------------

describe('TestFetchHealthz_HappyPath', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('returns a typed HealthzResponse', async () => {
    const payload: HealthzResponse = { ok: true, version: 'v0.1.0', schema_version: 3 };
    mockFetch(200, payload);
    const result = await fetchHealthz();
    expect(result.ok).toBe(true);
    expect(result.version).toBe('v0.1.0');
    expect(result.schema_version).toBe(3);
  });
});

// ---------------------------------------------------------------------------
// TestFetchCostSummary_HappyPath
// ---------------------------------------------------------------------------

describe('TestFetchCostSummary_HappyPath', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('passes group, since, until params', async () => {
    const payload: CostSummaryResponse = {
      group: 'day',
      since: 0,
      until: 0,
      buckets: [],
      total: { key: 'total', tokens_in: 0, tokens_out: 0, cost_usd: 0, count: 0 }
    };
    mockFetch(200, payload);
    await fetchCostSummary({ group: 'day', since: 1700000000000, until: 1700086400000 });
    const url = (vi.mocked(fetch).mock.calls[0][0] as string);
    expect(url).toContain('group=day');
    expect(url).toContain('since=1700000000000');
    expect(url).toContain('until=1700086400000');
  });
});

// ---------------------------------------------------------------------------
// TestFetchRestore_HappyPath
// ---------------------------------------------------------------------------

import { fetchRestore, fetchSummary } from './api.js';
import type { RestoreResponse, SummaryResponse } from './types.js';

describe('TestFetchRestore_HappyPath', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('returns a typed RestoreResponse', async () => {
    const payload: RestoreResponse = {
      session_id: 'sess-1',
      summary: 'Session summary',
      tail: [],
      markdown: '# Resume',
      resume_cmd: 'claude --resume sess-1',
      project_path: '/projects/foo',
      generated_at: 1700000000000
    };
    mockFetch(200, payload);
    const result = await fetchRestore('sess-1');
    expect(result.session_id).toBe('sess-1');
    expect(result.resume_cmd).toBe('claude --resume sess-1');
  });
});

// ---------------------------------------------------------------------------
// TestFetchSummary_HappyPath
// ---------------------------------------------------------------------------

describe('TestFetchSummary_HappyPath', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('returns a typed SummaryResponse', async () => {
    const payload: SummaryResponse = {
      session_id: 'sess-1',
      version: 3,
      text: 'Summary text here',
      model: 'claude-sonnet-4.5',
      ts: 1700000000000
    };
    mockFetch(200, payload);
    const result = await fetchSummary('sess-1');
    expect(result.version).toBe(3);
    expect(result.text).toBe('Summary text here');
  });
});

