/**
 * TokenSavings.test.ts — savings-math edge cases for the per-session
 * token-savings indicator.
 *
 * Cases covered:
 *   - <50% fill, calibrated  → bar renders, CTA hidden
 *   - >=70% fill, calibrated → bar + CTA + advisor button render
 *   - uncalibrated (-1)      → bar shows "—", CTA hidden
 *   - break-advice happy path → click advisor → result card renders
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import TokenSavings from './TokenSavings.svelte';
import type { SessionUsageResponse, BreakAdviceResponse } from '../types.js';

// ---------------------------------------------------------------------------
// API mocks
// ---------------------------------------------------------------------------

vi.mock('../api', () => ({
  fetchSessionUsage: vi.fn(),
  fetchBreakAdvice: vi.fn(),
  fetchSession: vi.fn().mockResolvedValue({
    session: {
      id: 'sess-1',
      cli: 'claude',
      project_path: '/p',
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
      status: 'idle',
      raw_path: ''
    }
  })
}));

// SSE subscribe is harmless to no-op in tests.
vi.mock('../sse', () => ({
  subscribe: vi.fn().mockReturnValue(() => {})
}));

// Importing the module after vi.mock() is hoisted ensures we get the
// mocked fns.
import * as api from '../api';

const fetchSessionUsageMock = api.fetchSessionUsage as unknown as ReturnType<
  typeof vi.fn
>;
const fetchBreakAdviceMock = api.fetchBreakAdvice as unknown as ReturnType<
  typeof vi.fn
>;

function makeUsage(overrides: Partial<SessionUsageResponse> = {}): SessionUsageResponse {
  return {
    session_id: 'sess-1',
    model: 'claude-sonnet-4.5',
    context_window: 200_000,
    context_used: 90_000,
    context_fill_pct: 45,
    next_turn_pct_5h: 6.0,
    compacted_next_turn_pct_5h: 1.0,
    restarted_next_turn_pct_5h: 0.5,
    compact_savings_pct_5h: 5.0,
    restart_savings_pct_5h: 5.5,
    compact_ratio: 0.15,
    calibrated_from_oauth: true,
    ...overrides
  };
}

beforeEach(() => {
  vi.clearAllMocks();
});

afterEach(() => {
  vi.restoreAllMocks();
});

// Stub clipboard so the copy buttons don't blow up in jsdom.
function stubClipboard(): void {
  Object.assign(navigator, {
    clipboard: { writeText: vi.fn().mockResolvedValue(undefined) }
  });
}

// ---------------------------------------------------------------------------
// Bar-only rendering at low fill
// ---------------------------------------------------------------------------

describe('TokenSavings — fill < 50, calibrated', () => {
  it('renders the bar but hides the compact CTA and advisor', async () => {
    fetchSessionUsageMock.mockResolvedValue(
      makeUsage({ context_fill_pct: 45, next_turn_pct_5h: 6.0 })
    );

    const { container } = render(TokenSavings, {
      sessionId: 'sess-1',
      cli: 'claude'
    });

    await waitFor(() => {
      expect(container.textContent).toContain('Session 45% full');
    });

    // Bar headline reflects calibrated next-turn percentage.
    expect(container.textContent).toContain('6.0%');
    // CTA is hidden because fill < 50.
    expect(container.textContent).not.toContain('Compact now');
    // Advisor button is hidden because fill < 60.
    expect(container.textContent).not.toContain('Should I start a fresh session?');
  });
});

// ---------------------------------------------------------------------------
// CTA + advisor rendering at high fill
// ---------------------------------------------------------------------------

describe('TokenSavings — fill >= 70, calibrated', () => {
  it('renders the bar, the compact CTA with deltas, and the advisor button', async () => {
    fetchSessionUsageMock.mockResolvedValue(
      makeUsage({
        context_fill_pct: 70,
        next_turn_pct_5h: 8.5,
        compacted_next_turn_pct_5h: 1.3,
        compact_savings_pct_5h: 7.2
      })
    );

    const { container } = render(TokenSavings, {
      sessionId: 'sess-1',
      cli: 'claude'
    });

    await waitFor(() => {
      expect(container.textContent).toContain('Session 70% full');
    });

    // Headline carries both projections.
    expect(container.textContent).toContain('8.5%');
    expect(container.textContent).toContain('Compact now');
    expect(container.textContent).toContain('1.3%');
    expect(container.textContent).toContain('7.2%');

    // Advisor button is shown but not yet clicked (no result card).
    expect(container.textContent).toContain('Should I start a fresh session?');
    expect(container.textContent).not.toContain('Start a fresh session');
  });
});

// ---------------------------------------------------------------------------
// Uncalibrated state
// ---------------------------------------------------------------------------

describe('TokenSavings — uncalibrated (-1)', () => {
  it('shows "—" for next-turn and hides the CTA', async () => {
    fetchSessionUsageMock.mockResolvedValue(
      makeUsage({
        context_fill_pct: 70,
        next_turn_pct_5h: -1,
        compacted_next_turn_pct_5h: -1,
        compact_savings_pct_5h: -1
      })
    );

    const { container } = render(TokenSavings, {
      sessionId: 'sess-1',
      cli: 'claude'
    });

    await waitFor(() => {
      expect(container.textContent).toContain('Session 70% full');
    });

    // Em-dash placeholder for the projection.
    expect(container.textContent).toContain('Next turn ≈ —');

    // CTA must be hidden when we can't project a calibrated saving.
    expect(container.textContent).not.toContain('Compact now');

    // Advisor still appears at >=60% fill.
    expect(container.textContent).toContain('Should I start a fresh session?');
  });
});

// ---------------------------------------------------------------------------
// Break-advice click path
// ---------------------------------------------------------------------------

describe('TokenSavings — break-advice click path', () => {
  it('renders the verdict card on successful response', async () => {
    stubClipboard();
    fetchSessionUsageMock.mockResolvedValue(
      makeUsage({
        context_fill_pct: 80,
        next_turn_pct_5h: 9.0,
        compacted_next_turn_pct_5h: 1.5,
        compact_savings_pct_5h: 7.5
      })
    );

    const adviceResp: BreakAdviceResponse = {
      session_id: 'sess-1',
      verdict: 'start_fresh',
      reason: 'Context is dominated by stale exploration; a fresh session will be cheaper.',
      suggested_topic: 'fix login bug',
      provider: 'anthropic',
      model: 'claude-haiku-4',
      cached_at: 0
    };
    fetchBreakAdviceMock.mockResolvedValueOnce(adviceResp);

    const { container } = render(TokenSavings, {
      sessionId: 'sess-1',
      cli: 'claude'
    });

    // Wait for usage fetch to land + advisor button to render.
    const advisorBtn = await screen.findByRole('button', {
      name: /Should I start a fresh session/i
    });

    await fireEvent.click(advisorBtn);

    // After the click, the verdict card should appear.
    await waitFor(() => {
      expect(container.textContent).toContain('Start a fresh session');
    });
    expect(container.textContent).toContain(adviceResp.reason);
    expect(container.textContent).toContain('fix login bug');
    expect(container.textContent).toContain('anthropic');
  });
});
