/**
 * messageFilters.ts — global rule for "which messages do we ever show to
 * the user?"
 *
 * One source of truth so every surface (Terminal tile on the Work page,
 * Cockpit project tiles, Projects index, Session detail view, Session
 * embed component) drops the exact same set of rows.
 *
 * Rule: only conversational prose is shown.
 *   - role === 'user'      with non-empty content
 *   - role === 'assistant' with non-empty content
 * Drop:
 *   - role === 'tool'       (every tool-result row, regardless of content)
 *   - role === 'system'     (advisor lines, compact boundaries, etc.)
 *   - role === 'assistant'  with no text body  (pure tool_use envelopes)
 *
 * Why "no text assistant" gets dropped: Claude Code emits a separate
 * assistant message per tool_use block, so a single user request can
 * produce 5–10 "blank" assistant rows that exist only to carry tool
 * calls. They render as empty bubbles / "called: X" labels in every
 * view, which is the noise the global fix is meant to eliminate.
 */

import type { Message } from './types';

/**
 * isConversationalMessage returns true when the message should be shown
 * to the user in any conversational view. See module header for the rule.
 */
export function isConversationalMessage(m: Message): boolean {
  if (!m) return false;
  if (m.role !== 'user' && m.role !== 'assistant') return false;
  const text = (m.content ?? '').trim();
  return text.length > 0;
}

/** filterConversational is the slice-friendly version of the predicate. */
export function filterConversational<T extends Message>(messages: T[]): T[] {
  return messages.filter(isConversationalMessage);
}
