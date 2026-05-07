/**
 * CostPanel.test.ts
 *
 * Tests:
 *   TestCostPanel_HappyPath    — renders total cost, token counts, bucket list
 *   TestCostPanel_Loading      — shows skeleton while loading
 *   TestCostPanel_Error        — shows error message
 *   TestCostPanel_Empty        — shows empty state when no buckets
 *   TestCostPanel_NullData     — shows empty state when data is null
 *   TestCostPanel_Formatting   — formats small and large USD values correctly
 */

import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import CostPanel from './CostPanel.svelte';
import type { CostSummaryResponse } from '$lib/types.js';

function makeCostData(overrides: Partial<CostSummaryResponse> = {}): CostSummaryResponse {
  return {
    group: 'session',
    since: 0,
    until: 0,
    buckets: [
      { key: 'sess-1', tokens_in: 1000, tokens_out: 500, cost_usd: 0.025, count: 10 },
      { key: 'sess-2', tokens_in: 2000, tokens_out: 800, cost_usd: 0.04, count: 5 }
    ],
    total: { key: 'total', tokens_in: 3000, tokens_out: 1300, cost_usd: 0.065, count: 15 },
    ...overrides
  };
}

describe('TestCostPanel_HappyPath', () => {
  it('renders the cost panel', () => {
    render(CostPanel, { props: { data: makeCostData() } });
    expect(screen.getByTestId('cost-panel')).toBeInTheDocument();
  });

  it('renders total cost', () => {
    render(CostPanel, { props: { data: makeCostData() } });
    expect(screen.getByTestId('cost-total')).toHaveTextContent('$0.07');
  });

  it('renders bucket items', () => {
    render(CostPanel, { props: { data: makeCostData() } });
    expect(screen.getByText('sess-1')).toBeInTheDocument();
    expect(screen.getByText('sess-2')).toBeInTheDocument();
  });

  it('renders token counts', () => {
    render(CostPanel, { props: { data: makeCostData() } });
    expect(screen.getByText('3,000')).toBeInTheDocument(); // tokens_in
    expect(screen.getByText('1,300')).toBeInTheDocument(); // tokens_out
  });

  it('renders message count', () => {
    render(CostPanel, { props: { data: makeCostData() } });
    expect(screen.getByText('15')).toBeInTheDocument();
  });

  it('shows group label', () => {
    render(CostPanel, { props: { data: makeCostData({ group: 'day' }) } });
    expect(screen.getByText('By day')).toBeInTheDocument();
  });
});

describe('TestCostPanel_Loading', () => {
  it('renders loading skeleton', () => {
    render(CostPanel, { props: { loading: true } });
    expect(screen.getByTestId('cost-loading')).toBeInTheDocument();
  });

  it('does not render cost-total while loading', () => {
    render(CostPanel, { props: { loading: true } });
    expect(screen.queryByTestId('cost-total')).not.toBeInTheDocument();
  });
});

describe('TestCostPanel_Error', () => {
  it('renders error message', () => {
    render(CostPanel, { props: { error: 'Network failure' } });
    expect(screen.getByTestId('cost-error')).toHaveTextContent('Network failure');
  });

  it('does not show empty state when error is shown', () => {
    render(CostPanel, { props: { error: 'oops' } });
    expect(screen.queryByTestId('cost-empty')).not.toBeInTheDocument();
  });
});

describe('TestCostPanel_Empty', () => {
  it('shows empty state when buckets array is empty', () => {
    render(CostPanel, {
      props: {
        data: makeCostData({
          buckets: [],
          total: { key: 'total', tokens_in: 0, tokens_out: 0, cost_usd: 0, count: 0 }
        })
      }
    });
    expect(screen.getByTestId('cost-empty')).toBeInTheDocument();
  });

  it('shows empty state when data is null', () => {
    render(CostPanel, { props: { data: null } });
    expect(screen.getByTestId('cost-empty')).toBeInTheDocument();
  });
});

describe('TestCostPanel_NullData', () => {
  it('renders empty state for undefined data', () => {
    render(CostPanel);
    expect(screen.getByTestId('cost-empty')).toBeInTheDocument();
  });
});

describe('TestCostPanel_Formatting', () => {
  it('formats small cost with 4 decimal places', () => {
    const data = makeCostData({
      buckets: [{ key: 'x', tokens_in: 1, tokens_out: 1, cost_usd: 0.0012, count: 1 }],
      total: { key: 'total', tokens_in: 1, tokens_out: 1, cost_usd: 0.0012, count: 1 }
    });
    render(CostPanel, { props: { data } });
    expect(screen.getByTestId('cost-total')).toHaveTextContent('$0.0012');
  });

  it('formats larger cost with 2 decimal places', () => {
    const data = makeCostData({
      buckets: [{ key: 'x', tokens_in: 1000, tokens_out: 1000, cost_usd: 1.5, count: 10 }],
      total: { key: 'total', tokens_in: 1000, tokens_out: 1000, cost_usd: 1.5, count: 10 }
    });
    render(CostPanel, { props: { data } });
    expect(screen.getByTestId('cost-total')).toHaveTextContent('$1.50');
  });
});
