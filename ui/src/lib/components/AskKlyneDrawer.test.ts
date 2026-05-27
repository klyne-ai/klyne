/**
 * AskKlyneDrawer.test.ts — drawer interaction tests.
 *
 * Cases:
 *   - submit a question → user msg + assistant reply render
 *   - send button is disabled when input is empty
 *   - history is sent on the next turn (multi-turn ephemeral chat)
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/svelte';

vi.mock('../api', () => ({
  askKlyne: vi.fn().mockResolvedValue({
    answer: 'You fixed 3 bugs.',
    sessions_used: 5,
    model: 'claude-sonnet-4-6',
    duration_ms: 1200,
  }),
}));

import AskKlyneDrawer from './AskKlyneDrawer.svelte';
import * as api from '../api';

const askKlyneMock = api.askKlyne as unknown as ReturnType<typeof vi.fn>;

describe('AskKlyneDrawer', () => {
  beforeEach(() => {
    askKlyneMock.mockClear();
    askKlyneMock.mockResolvedValue({
      answer: 'You fixed 3 bugs.',
      sessions_used: 5,
      model: 'claude-sonnet-4-6',
      duration_ms: 1200,
    });
  });
  afterEach(() => cleanup());

  it('renders the user message and the assistant reply', async () => {
    render(AskKlyneDrawer, {
      props: { open: true, projects: ['/p/a'], fromMs: 100, toMs: 200 },
    });

    const textarea = screen.getByPlaceholderText(/ask klyne/i);
    await fireEvent.input(textarea, { target: { value: 'How many bugs?' } });

    const sendButton = screen.getByRole('button', { name: /send/i });
    await fireEvent.click(sendButton);

    expect(screen.getByText('How many bugs?')).toBeTruthy();
    await waitFor(() => {
      expect(screen.getByText('You fixed 3 bugs.')).toBeTruthy();
    });

    expect(askKlyneMock).toHaveBeenCalledTimes(1);
    const callArg = askKlyneMock.mock.calls[0][0];
    expect(callArg.question).toBe('How many bugs?');
    expect(callArg.projects).toEqual(['/p/a']);
    expect(callArg.from_ms).toBe(100);
    expect(callArg.to_ms).toBe(200);
  });

  it('disables send when input is empty', () => {
    render(AskKlyneDrawer, {
      props: { open: true, projects: [], fromMs: 0, toMs: 100 },
    });
    const sendButton = screen.getByRole('button', { name: /send/i }) as HTMLButtonElement;
    expect(sendButton.disabled).toBe(true);
  });

  it('sends the prior turns in history on the next request', async () => {
    render(AskKlyneDrawer, {
      props: { open: true, projects: [], fromMs: 0, toMs: 100 },
    });
    const textarea = screen.getByPlaceholderText(/ask klyne/i);
    const sendButton = screen.getByRole('button', { name: /send/i });

    await fireEvent.input(textarea, { target: { value: 'first' } });
    await fireEvent.click(sendButton);
    await waitFor(() => expect(screen.getByText('You fixed 3 bugs.')).toBeTruthy());

    await fireEvent.input(textarea, { target: { value: 'second' } });
    await fireEvent.click(sendButton);

    await waitFor(() => {
      expect(askKlyneMock.mock.calls.length).toBe(2);
    });
    const second = askKlyneMock.mock.calls[1][0];
    expect(second.history.length).toBe(2);
    expect(second.history[0].role).toBe('user');
    expect(second.history[0].content).toBe('first');
    expect(second.history[1].role).toBe('assistant');
  });
});
