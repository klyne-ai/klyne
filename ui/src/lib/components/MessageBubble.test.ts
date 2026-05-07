/**
 * MessageBubble.test.ts
 *
 * Tests:
 *   TestMessageBubble_HappyPath_User       — renders user message with correct styling
 *   TestMessageBubble_HappyPath_Assistant  — renders assistant message
 *   TestMessageBubble_HappyPath_Tool       — renders tool message
 *   TestMessageBubble_HappyPath_System     — renders system message
 *   TestMessageBubble_EmptyContent         — renders fallback when content is empty
 *   TestMessageBubble_Highlight            — applies highlight ring when highlight=true
 *   TestMessageBubble_CostFooter           — shows cost footer when cost_usd > 0
 */

import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import MessageBubble from './MessageBubble.svelte';
import type { Message } from '$lib/types.js';

function makeMessage(overrides: Partial<Message> = {}): Message {
  return {
    id: 'msg-1',
    session_id: 'sess-1',
    cli: 'claude',
    project_path: '/projects/foo',
    role: 'user',
    content: 'Hello, world!',
    tokens_in: 10,
    tokens_out: 20,
    cost_usd: 0,
    model: 'claude-sonnet-4.5',
    ts: 1700000000000,
    ...overrides
  };
}

describe('TestMessageBubble_HappyPath_User', () => {
  it('renders user message content', () => {
    render(MessageBubble, { props: { message: makeMessage({ role: 'user', content: 'Hello from user' }) } });
    expect(screen.getByText('Hello from user')).toBeInTheDocument();
  });

  it('renders role label "You" for user', () => {
    render(MessageBubble, { props: { message: makeMessage({ role: 'user' }) } });
    expect(screen.getByText('You')).toBeInTheDocument();
  });

  it('sets data-role attribute', () => {
    render(MessageBubble, { props: { message: makeMessage({ role: 'user' }) } });
    const article = screen.getByTestId('message-bubble');
    expect(article).toHaveAttribute('data-role', 'user');
  });
});

describe('TestMessageBubble_HappyPath_Assistant', () => {
  it('renders assistant message content', () => {
    render(MessageBubble, { props: { message: makeMessage({ role: 'assistant', content: 'Hi there!' }) } });
    expect(screen.getByText('Hi there!')).toBeInTheDocument();
  });

  it('renders role label "Assistant"', () => {
    render(MessageBubble, { props: { message: makeMessage({ role: 'assistant' }) } });
    expect(screen.getByText('Assistant')).toBeInTheDocument();
  });

  it('shows model name', () => {
    render(MessageBubble, { props: { message: makeMessage({ role: 'assistant', model: 'claude-sonnet-4.5' }) } });
    expect(screen.getByText('claude-sonnet-4.5')).toBeInTheDocument();
  });
});

describe('TestMessageBubble_HappyPath_Tool', () => {
  it('renders tool message content', () => {
    render(MessageBubble, { props: { message: makeMessage({ role: 'tool', content: 'tool result here' }) } });
    expect(screen.getByText('tool result here')).toBeInTheDocument();
  });

  it('renders role label "Tool"', () => {
    render(MessageBubble, { props: { message: makeMessage({ role: 'tool' }) } });
    expect(screen.getByText('Tool')).toBeInTheDocument();
  });
});

describe('TestMessageBubble_HappyPath_System', () => {
  it('renders system message content', () => {
    render(MessageBubble, { props: { message: makeMessage({ role: 'system', content: 'System prompt' }) } });
    expect(screen.getByText('System prompt')).toBeInTheDocument();
  });

  it('renders role label "System"', () => {
    render(MessageBubble, { props: { message: makeMessage({ role: 'system' }) } });
    expect(screen.getByText('System')).toBeInTheDocument();
  });
});

describe('TestMessageBubble_EmptyContent', () => {
  it('shows tool call placeholder when content is empty but has tool calls', () => {
    const message = makeMessage({
      content: '',
      tool_calls: [{ id: 'tc-1', name: 'bash', input: '{}' }]
    });
    render(MessageBubble, { props: { message } });
    expect(screen.getByText('Tool call')).toBeInTheDocument();
  });

  it('shows empty placeholder when content is empty and no tool calls', () => {
    render(MessageBubble, { props: { message: makeMessage({ content: '' }) } });
    expect(screen.getByText('(empty)')).toBeInTheDocument();
  });
});

describe('TestMessageBubble_Highlight', () => {
  it('does not apply ring when highlight is false', () => {
    render(MessageBubble, { props: { message: makeMessage(), highlight: false } });
    const article = screen.getByTestId('message-bubble');
    expect(article.classList.contains('ring-2')).toBe(false);
  });

  it('applies ring-2 class when highlight is true', () => {
    render(MessageBubble, { props: { message: makeMessage(), highlight: true } });
    const article = screen.getByTestId('message-bubble');
    expect(article.classList.contains('ring-2')).toBe(true);
  });
});

describe('TestMessageBubble_CostFooter', () => {
  it('does not render cost footer when cost_usd is 0', () => {
    render(MessageBubble, { props: { message: makeMessage({ cost_usd: 0 }) } });
    expect(screen.queryByText(/\$0\.\d+/)).not.toBeInTheDocument();
  });

  it('renders cost footer when cost_usd > 0', () => {
    render(MessageBubble, { props: { message: makeMessage({ cost_usd: 0.001234 }) } });
    // (0.001234).toFixed(4) = "0.0012"
    expect(screen.getByText(/\$0\.0012/)).toBeInTheDocument();
  });
});
