/**
 * SearchBar.test.ts
 *
 * Tests:
 *   TestSearchBar_HappyPath         — renders input with placeholder
 *   TestSearchBar_Debounce          — calls onsearch after 300ms
 *   TestSearchBar_ClearButton       — shows/hides clear button
 *   TestSearchBar_EscapeClears      — Esc key clears and calls onsearch with empty
 *   TestSearchBar_AriaLabel         — has correct aria-label
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import SearchBar from './SearchBar.svelte';

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
});

describe('TestSearchBar_HappyPath', () => {
  it('renders the search input', () => {
    render(SearchBar);
    expect(screen.getByRole('searchbox')).toBeInTheDocument();
  });

  it('renders custom placeholder', () => {
    render(SearchBar, { placeholder: 'Find something…' });
    expect(screen.getByPlaceholderText('Find something…')).toBeInTheDocument();
  });

  it('shows the search bar container', () => {
    render(SearchBar);
    expect(screen.getByTestId('search-bar')).toBeInTheDocument();
  });

  it('has correct aria-label', () => {
    render(SearchBar);
    expect(screen.getByLabelText('Search messages')).toBeInTheDocument();
  });
});

describe('TestSearchBar_Debounce', () => {
  it('calls onsearch after debounce delay', async () => {
    const onsearch = vi.fn();
    render(SearchBar, { debounceMs: 300, onsearch });

    const input = screen.getByRole('searchbox');
    await fireEvent.input(input, { target: { value: 'hello' } });

    // No call yet
    expect(onsearch).not.toHaveBeenCalled();

    // Advance timers
    vi.advanceTimersByTime(300);
    await waitFor(() => expect(onsearch).toHaveBeenCalledOnce());
    expect(onsearch).toHaveBeenCalledWith('hello');
  });

  it('debounces rapid input — calls onsearch only once at end', async () => {
    const onsearch = vi.fn();
    render(SearchBar, { debounceMs: 300, onsearch });

    const input = screen.getByRole('searchbox');
    await fireEvent.input(input, { target: { value: 'a' } });
    vi.advanceTimersByTime(100);
    await fireEvent.input(input, { target: { value: 'ab' } });
    vi.advanceTimersByTime(100);
    await fireEvent.input(input, { target: { value: 'abc' } });
    vi.advanceTimersByTime(300);

    await waitFor(() => expect(onsearch).toHaveBeenCalledOnce());
    expect(onsearch).toHaveBeenCalledWith('abc');
  });
});

describe('TestSearchBar_ClearButton', () => {
  it('does not show clear button when value is empty', () => {
    render(SearchBar, { value: '' });
    expect(screen.queryByLabelText('Clear search')).not.toBeInTheDocument();
  });

  it('shows clear button when value is non-empty', () => {
    render(SearchBar, { value: 'hello' });
    expect(screen.getByLabelText('Clear search')).toBeInTheDocument();
  });

  it('calls onsearch with empty string on clear button click', async () => {
    const onsearch = vi.fn();
    render(SearchBar, { value: 'hello', debounceMs: 0, onsearch });

    const clearBtn = screen.getByLabelText('Clear search');
    await fireEvent.click(clearBtn);

    expect(onsearch).toHaveBeenCalledWith('');
  });
});

describe('TestSearchBar_EscapeClears', () => {
  it('calls onsearch with empty on Escape key press', async () => {
    const onsearch = vi.fn();
    render(SearchBar, { value: 'something', debounceMs: 0, onsearch });

    const input = screen.getByRole('searchbox');
    await fireEvent.keyDown(input, { key: 'Escape' });

    expect(onsearch).toHaveBeenCalledWith('');
  });
});
