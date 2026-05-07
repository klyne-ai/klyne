/**
 * SessionView.test.ts
 *
 * Tests:
 *   TestSessionView_HappyPath     — renders messages via MessageBubble
 *   TestSessionView_ToolCalls     — renders ToolCallBlock for assistant tool calls
 *   TestSessionView_Loading       — shows skeleton while loading
 *   TestSessionView_Error         — shows error state with retry button
 *   TestSessionView_Empty         — shows empty state when messages = []
 *   TestSessionView_SkipsToolRole — skips standalone tool-role messages
 *   TestSessionView_Highlight     — passes highlightMessageId to MessageBubble
 */

import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import SessionView from './SessionView.svelte';
import type { Message } from '$lib/types.js';

function makeMessage(overrides: Partial<Message> = {}): Message {
  return {
    id: 'msg-1',
    session_id: 'sess-1',
    cli: 'claude',
    project_path: '/projects/foo',
    role: 'user',
    content: 'Hello',
    tokens_in: 10,
    tokens_out: 20,
    cost_usd: 0,
    model: 'claude-sonnet-4.5',
    ts: 1700000000000,
    ...overrides
  };
}

describe('TestSessionView_HappyPath', () => {
  it('renders the session view container', () => {
    render(SessionView, { props: { messages: [] } });
    expect(screen.getByTestId('session-view')).toBeInTheDocument();
  });

  it('renders user and assistant messages', () => {
    const messages = [
      makeMessage({ id: 'msg-1', role: 'user', content: 'User message' }),
      makeMessage({ id: 'msg-2', role: 'assistant', content: 'Assistant reply' })
    ];
    render(SessionView, { props: { messages } });
    expect(screen.getByText('User message')).toBeInTheDocument();
    expect(screen.getByText('Assistant reply')).toBeInTheDocument();
  });

  it('renders multiple message bubbles', () => {
    const messages = [
      makeMessage({ id: 'msg-1', role: 'user', content: 'First' }),
      makeMessage({ id: 'msg-2', role: 'assistant', content: 'Second' }),
      makeMessage({ id: 'msg-3', role: 'user', content: 'Third' })
    ];
    render(SessionView, { props: { messages } });
    expect(screen.getAllByTestId('message-bubble')).toHaveLength(3);
  });
});

describe('TestSessionView_ToolCalls', () => {
  it('renders ToolCallBlock for assistant messages with tool calls', () => {
    const messages = [
      makeMessage({
        id: 'msg-1',
        role: 'assistant',
        content: '',
        tool_calls: [{ id: 'tc-1', name: 'bash', input: '{"cmd":"ls"}' }]
      })
    ];
    render(SessionView, { props: { messages } });
    expect(screen.getByTestId('tool-call-block')).toBeInTheDocument();
    expect(screen.getByTestId('tool-name')).toHaveTextContent('bash');
  });

  it('passes matched tool result to ToolCallBlock', () => {
    const messages = [
      makeMessage({
        id: 'msg-1',
        role: 'assistant',
        content: '',
        tool_calls: [{ id: 'tc-1', name: 'bash', input: '{}' }]
      }),
      makeMessage({
        id: 'msg-2',
        role: 'tool',
        content: 'tool result',
        tool_results: [{ id: 'tc-1', output: 'file1.txt', is_error: false }]
      })
    ];
    render(SessionView, { props: { messages } });
    // The tool result should be visible via ToolCallBlock (after clicking to expand)
    expect(screen.getByText('✓ ok')).toBeInTheDocument();
  });
});

describe('TestSessionView_Loading', () => {
  it('shows loading skeleton', () => {
    render(SessionView, { props: { messages: [], loading: true } });
    expect(screen.getByTestId('session-view-loading')).toBeInTheDocument();
  });

  it('does not show empty state while loading', () => {
    render(SessionView, { props: { messages: [], loading: true } });
    expect(screen.queryByTestId('session-view-empty')).not.toBeInTheDocument();
  });
});

describe('TestSessionView_Error', () => {
  it('shows error state', () => {
    render(SessionView, { props: { messages: [], error: 'Network error' } });
    expect(screen.getByTestId('session-view-error')).toBeInTheDocument();
    expect(screen.getByText('Network error')).toBeInTheDocument();
  });

  it('shows retry button when onRetry is provided', () => {
    const onRetry = vi.fn();
    render(SessionView, { props: { messages: [], error: 'oops', onRetry } });
    expect(screen.getByText('Retry')).toBeInTheDocument();
  });

  it('calls onRetry when retry button is clicked', async () => {
    const onRetry = vi.fn();
    render(SessionView, { props: { messages: [], error: 'oops', onRetry } });
    await fireEvent.click(screen.getByText('Retry'));
    expect(onRetry).toHaveBeenCalledOnce();
  });

  it('does not show retry button when onRetry is not provided', () => {
    render(SessionView, { props: { messages: [], error: 'oops' } });
    expect(screen.queryByText('Retry')).not.toBeInTheDocument();
  });
});

describe('TestSessionView_Empty', () => {
  it('shows empty state when messages array is empty', () => {
    render(SessionView, { props: { messages: [] } });
    expect(screen.getByTestId('session-view-empty')).toBeInTheDocument();
  });

  it('shows descriptive text in empty state', () => {
    render(SessionView, { props: { messages: [] } });
    expect(screen.getByText(/No messages in this session yet/)).toBeInTheDocument();
  });
});

describe('TestSessionView_SkipsToolRole', () => {
  it('does not render standalone tool-role messages as bubbles', () => {
    const messages = [
      makeMessage({ id: 'msg-1', role: 'user', content: 'User msg' }),
      makeMessage({
        id: 'msg-2',
        role: 'tool',
        content: 'tool output',
        tool_results: [{ id: 'tc-1', output: 'output', is_error: false }]
      })
    ];
    render(SessionView, { props: { messages } });
    // Only 1 bubble (user), not 2 — tool role is skipped
    expect(screen.getAllByTestId('message-bubble')).toHaveLength(1);
  });
});

describe('TestSessionView_Highlight', () => {
  it('passes highlight=true to the matching message bubble', () => {
    const messages = [
      makeMessage({ id: 'msg-target', role: 'user', content: 'Highlighted' }),
      makeMessage({ id: 'msg-other', role: 'assistant', content: 'Normal' })
    ];
    render(SessionView, { props: { messages, highlightMessageId: 'msg-target' } });
    const bubbles = screen.getAllByTestId('message-bubble');
    // First bubble (msg-target) should have ring-2
    expect(bubbles[0].classList.contains('ring-2')).toBe(true);
    // Second bubble should not
    expect(bubbles[1].classList.contains('ring-2')).toBe(false);
  });
});
