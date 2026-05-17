import { describe, it, expect } from 'vitest';
import { isConversationalMessage, filterConversational } from './messageFilters';
import type { Message } from './types';

function msg(partial: Partial<Message>): Message {
  return {
    id: 'id',
    session_id: 's',
    cli: 'claude',
    project_path: '/p',
    role: 'user',
    content: '',
    tokens_in: 0,
    tokens_out: 0,
    cached_read_tokens: 0,
    cached_write_tokens: 0,
    cost_usd: 0,
    model: '',
    ts: 0,
    ...partial,
  };
}

describe('isConversationalMessage', () => {
  it('keeps user messages with text', () => {
    expect(isConversationalMessage(msg({ role: 'user', content: 'hello' }))).toBe(true);
  });

  it('keeps assistant messages with text', () => {
    expect(isConversationalMessage(msg({ role: 'assistant', content: 'hi back' }))).toBe(true);
  });

  it('drops tool messages even when they have content', () => {
    expect(isConversationalMessage(msg({ role: 'tool', content: 'stdout: ok' }))).toBe(false);
  });

  it('drops tool messages with tool_results only', () => {
    expect(
      isConversationalMessage(
        msg({
          role: 'tool',
          content: '',
          tool_results: [{ id: 't1', output: 'ok', is_error: false }],
        })
      )
    ).toBe(false);
  });

  it('drops system messages', () => {
    expect(isConversationalMessage(msg({ role: 'system', content: 'advisor line' }))).toBe(false);
  });

  it('drops assistant messages with no text body (pure tool_use)', () => {
    expect(
      isConversationalMessage(
        msg({
          role: 'assistant',
          content: '',
          tool_calls: [{ id: 't1', name: 'Bash', input: '{}' }],
        })
      )
    ).toBe(false);
  });

  it('drops user messages with only whitespace', () => {
    expect(isConversationalMessage(msg({ role: 'user', content: '   \n  ' }))).toBe(false);
  });

  it('drops null / undefined input safely', () => {
    // @ts-expect-error intentional bad input
    expect(isConversationalMessage(null)).toBe(false);
    // @ts-expect-error intentional bad input
    expect(isConversationalMessage(undefined)).toBe(false);
  });
});

describe('filterConversational', () => {
  it('removes tool + system + empty-assistant rows in one pass', () => {
    const input: Message[] = [
      msg({ id: 'a', role: 'user', content: 'first' }),
      msg({
        id: 'b',
        role: 'assistant',
        content: '',
        tool_calls: [{ id: 't1', name: 'Bash', input: '{}' }],
      }),
      msg({ id: 'c', role: 'tool', content: 'stdout' }),
      msg({ id: 'd', role: 'system', content: 'advisor' }),
      msg({ id: 'e', role: 'assistant', content: 'second' }),
    ];
    const out = filterConversational(input);
    expect(out.map((m) => m.id)).toEqual(['a', 'e']);
  });
});
