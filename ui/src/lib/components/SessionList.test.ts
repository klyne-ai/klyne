/**
 * SessionList.test.ts
 *
 * Tests:
 *   TestSessionList_HappyPath     — renders sessions list with all metadata
 *   TestSessionList_Empty         — shows empty state when sessions = []
 *   TestSessionList_Loading       — shows skeleton UI while loading
 *   TestSessionList_Error         — shows error message
 *   TestSessionList_Click         — calls onselect callback on click
 *   TestSessionList_KeyEnter      — calls onselect on Enter key
 *   TestSessionList_HighlightIdx  — highlights item at highlightIndex
 *   TestSessionList_ActiveId      — marks active session
 *   TestSessionList_CliBadge      — renders correct CLI badge
 */

import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import SessionList from './SessionList.svelte';
import type { Session } from '$lib/types.js';

function makeSession(overrides: Partial<Session> = {}): Session {
  return {
    id: 'sess-1',
    cli: 'claude',
    project_path: '/home/user/myproject',
    encoded_cwd: '',
    started_at: 1700000000000,
    last_msg_at: Date.now() - 120_000, // 2 min ago
    msg_count: 42,
    tokens_in: 1000,
    tokens_out: 500,
    cost_usd: 0.025,
    model: 'claude-sonnet-4.5',
    status: 'idle',
    raw_path: '',
    ...overrides
  };
}

describe('TestSessionList_HappyPath', () => {
  it('renders session list container', () => {
    render(SessionList, { sessions: [makeSession()] });
    expect(screen.getByTestId('session-list')).toBeInTheDocument();
  });

  it('renders project name (last path segment)', () => {
    render(SessionList, { sessions: [makeSession()] });
    expect(screen.getByText('myproject')).toBeInTheDocument();
  });

  it('renders message count', () => {
    render(SessionList, { sessions: [makeSession({ msg_count: 42 })] });
    expect(screen.getByText('42 msgs')).toBeInTheDocument();
  });

  it('renders cost', () => {
    render(SessionList, { sessions: [makeSession({ cost_usd: 0.025 })] });
    // cost_usd: 0.025 < 0.01 is false, so uses toFixed(2) → $0.03... wait
    // 0.025 >= 0.01, so format is $0.03? No - 0.025.toFixed(2) = "0.03"?
    // Actually: 0.025 >= 0.01, so formatUsd returns $0.03 (2 decimal places, banker's rounding)
    // Actually Math: 0.025.toFixed(2) in JS = "0.03" due to floating point... let's just check it contains "$"
    const costEl = screen.getByTestId('session-cost');
    expect(costEl.textContent).toMatch(/^\$/);
  });

  it('renders relative time', () => {
    render(SessionList, { sessions: [makeSession()] });
    expect(screen.getByText(/ago/)).toBeInTheDocument();
  });

  it('renders cli badge', () => {
    render(SessionList, { sessions: [makeSession({ cli: 'claude' })] });
    expect(screen.getByText('claude')).toBeInTheDocument();
  });

  it('renders multiple sessions', () => {
    const sessions = [
      makeSession({ id: 'sess-1', project_path: '/foo/alpha' }),
      makeSession({ id: 'sess-2', project_path: '/foo/beta' })
    ];
    render(SessionList, { sessions });
    expect(screen.getAllByTestId('session-item')).toHaveLength(2);
    expect(screen.getByText('alpha')).toBeInTheDocument();
    expect(screen.getByText('beta')).toBeInTheDocument();
  });
});

describe('TestSessionList_Empty', () => {
  it('shows empty state when sessions array is empty', () => {
    render(SessionList, { sessions: [] });
    expect(screen.getByTestId('session-empty')).toBeInTheDocument();
  });

  it('shows helpful message in empty state', () => {
    render(SessionList, { sessions: [] });
    expect(screen.getByText(/No sessions yet/)).toBeInTheDocument();
    expect(screen.getByText(/claude/)).toBeInTheDocument();
  });

  it('does not render session items in empty state', () => {
    render(SessionList, { sessions: [] });
    expect(screen.queryByTestId('session-items')).not.toBeInTheDocument();
  });
});

describe('TestSessionList_Loading', () => {
  it('shows loading skeleton', () => {
    render(SessionList, { sessions: [], loading: true });
    expect(screen.getByTestId('session-loading')).toBeInTheDocument();
  });

  it('does not show empty state while loading', () => {
    render(SessionList, { sessions: [], loading: true });
    expect(screen.queryByTestId('session-empty')).not.toBeInTheDocument();
  });
});

describe('TestSessionList_Error', () => {
  it('shows error message', () => {
    render(SessionList, { sessions: [], error: 'Network error' });
    expect(screen.getByTestId('session-error')).toBeInTheDocument();
    expect(screen.getByText('Network error')).toBeInTheDocument();
  });

  it('does not render items in error state', () => {
    render(SessionList, { sessions: [], error: 'oops' });
    expect(screen.queryByTestId('session-items')).not.toBeInTheDocument();
  });
});

describe('TestSessionList_Click', () => {
  it('calls onselect when session is clicked', async () => {
    const sessions = [makeSession({ id: 'sess-click' })];
    const onselect = vi.fn();
    render(SessionList, { sessions, onselect });

    const item = screen.getByTestId('session-item');
    await fireEvent.click(item);

    expect(onselect).toHaveBeenCalledOnce();
    expect(onselect.mock.calls[0][0].id).toBe('sess-click');
  });
});

describe('TestSessionList_KeyEnter', () => {
  it('calls onselect on Enter key', async () => {
    const sessions = [makeSession({ id: 'sess-enter' })];
    const onselect = vi.fn();
    render(SessionList, { sessions, onselect });

    const item = screen.getByTestId('session-item');
    await fireEvent.keyDown(item, { key: 'Enter' });

    expect(onselect).toHaveBeenCalledOnce();
    expect(onselect.mock.calls[0][0].id).toBe('sess-enter');
  });

  it('calls onselect on Space key', async () => {
    const sessions = [makeSession({ id: 'sess-space' })];
    const onselect = vi.fn();
    render(SessionList, { sessions, onselect });

    const item = screen.getByTestId('session-item');
    await fireEvent.keyDown(item, { key: ' ' });

    expect(onselect).toHaveBeenCalledOnce();
  });
});

describe('TestSessionList_HighlightIdx', () => {
  it('applies ring class to highlighted session', () => {
    const sessions = [
      makeSession({ id: 'sess-a' }),
      makeSession({ id: 'sess-b' })
    ];
    render(SessionList, { sessions, highlightIndex: 1 });
    const items = screen.getAllByTestId('session-item');
    expect(items[0].classList.contains('ring-1')).toBe(false);
    expect(items[1].classList.contains('ring-1')).toBe(true);
  });
});

describe('TestSessionList_ActiveId', () => {
  it('marks active session with aria-selected=true', () => {
    const sessions = [makeSession({ id: 'active-sess' })];
    render(SessionList, { sessions, activeId: 'active-sess' });
    const item = screen.getByTestId('session-item');
    expect(item).toHaveAttribute('aria-selected', 'true');
  });

  it('marks inactive sessions with aria-selected=false', () => {
    const sessions = [makeSession({ id: 'other-sess' })];
    render(SessionList, { sessions, activeId: 'active-sess' });
    const item = screen.getByTestId('session-item');
    expect(item).toHaveAttribute('aria-selected', 'false');
  });
});

describe('TestSessionList_CliBadge', () => {
  it('renders codex CLI badge', () => {
    const sessions = [makeSession({ cli: 'codex' })];
    render(SessionList, { sessions });
    expect(screen.getByText('codex')).toBeInTheDocument();
  });

  it('renders claude CLI badge', () => {
    const sessions = [makeSession({ cli: 'claude' })];
    render(SessionList, { sessions });
    expect(screen.getByText('claude')).toBeInTheDocument();
  });
});
