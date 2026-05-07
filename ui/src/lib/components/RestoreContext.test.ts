/**
 * RestoreContext.test.ts
 *
 * Tests:
 *   RestoreContext_OpensAndFetchesData — renders loading state then shows data
 *   RestoreContext_ShowsError          — shows error on fetch failure
 *   RestoreContext_CopyCallsClipboard  — "Copy" button calls navigator.clipboard
 *   RestoreContext_CloseButton         — close button calls onclose
 *   RestoreContext_ShowsMarkdown       — markdown bundle is visible in <pre>
 *   RestoreContext_ShowsResumeCmd      — resume command is shown
 */

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/svelte';
import RestoreContext from './RestoreContext.svelte';
import * as api from '$lib/api.js';
import type { RestoreResponse } from '$lib/types.js';

const MOCK_RESTORE: RestoreResponse = {
  session_id: 'sess-123',
  summary: 'This is a test summary.',
  tail: [],
  markdown: '# Session sess-123 — restore context\n\n## Summary\n\nTest summary.\n',
  resume_cmd: 'claude --resume sess-123',
  project_path: '/tmp/proj',
  generated_at: 1700000000000
};

describe('RestoreContext', () => {
  beforeEach(() => {
    vi.restoreAllMocks();

    // Default: mock clipboard.
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText: vi.fn().mockResolvedValue(undefined) },
      writable: true,
      configurable: true
    });
  });

  it('RestoreContext_OpensAndFetchesData — shows loading then data', async () => {
    let resolveRestore!: (v: RestoreResponse) => void;
    const fetchSpy = vi.spyOn(api, 'fetchRestore').mockReturnValue(
      new Promise<RestoreResponse>((res) => { resolveRestore = res; })
    );

    render(RestoreContext, { props: { sessionId: 'sess-123' } });

    // Loading state.
    expect(screen.getByTestId('restore-loading')).toBeInTheDocument();

    // Resolve fetch.
    resolveRestore(MOCK_RESTORE);

    await waitFor(() => {
      expect(screen.queryByTestId('restore-loading')).not.toBeInTheDocument();
    });

    expect(fetchSpy).toHaveBeenCalledWith('sess-123');
    expect(screen.getByTestId('restore-markdown')).toBeInTheDocument();
    expect(screen.getByTestId('restore-resume-cmd')).toHaveTextContent('claude --resume sess-123');
  });

  it('RestoreContext_ShowsError — shows error when fetch fails', async () => {
    vi.spyOn(api, 'fetchRestore').mockRejectedValue(new Error('network error'));

    render(RestoreContext, { props: { sessionId: 'sess-fail' } });

    await waitFor(() => {
      expect(screen.getByTestId('restore-error')).toBeInTheDocument();
    });

    expect(screen.getByTestId('restore-error')).toHaveTextContent('network error');
  });

  it('RestoreContext_CopyCallsClipboard — copy button calls clipboard.writeText', async () => {
    vi.spyOn(api, 'fetchRestore').mockResolvedValue(MOCK_RESTORE);

    render(RestoreContext, { props: { sessionId: 'sess-123' } });

    await waitFor(() => {
      expect(screen.queryByTestId('restore-loading')).not.toBeInTheDocument();
    });

    const copyBtn = screen.getByTestId('restore-copy-button');
    await fireEvent.click(copyBtn);

    expect(navigator.clipboard.writeText).toHaveBeenCalledWith(MOCK_RESTORE.markdown);
  });

  it('RestoreContext_CloseButton — calls onclose when close button clicked', async () => {
    vi.spyOn(api, 'fetchRestore').mockResolvedValue(MOCK_RESTORE);
    const onclose = vi.fn();

    render(RestoreContext, { props: { sessionId: 'sess-123', onclose } });

    // Close immediately even while loading.
    const closeBtn = screen.getByTestId('restore-close-button');
    await fireEvent.click(closeBtn);

    expect(onclose).toHaveBeenCalled();
  });

  it('RestoreContext_ShowsMarkdown — markdown bundle appears in <pre>', async () => {
    vi.spyOn(api, 'fetchRestore').mockResolvedValue(MOCK_RESTORE);

    render(RestoreContext, { props: { sessionId: 'sess-123' } });

    await waitFor(() => {
      expect(screen.queryByTestId('restore-loading')).not.toBeInTheDocument();
    });

    const pre = screen.getByTestId('restore-markdown');
    expect(pre.textContent).toContain('restore context');
  });

  it('RestoreContext_ShowsResumeCmd — resume command is visible', async () => {
    vi.spyOn(api, 'fetchRestore').mockResolvedValue(MOCK_RESTORE);

    render(RestoreContext, { props: { sessionId: 'sess-123' } });

    await waitFor(() => {
      expect(screen.getByTestId('restore-resume-cmd')).toBeInTheDocument();
    });

    expect(screen.getByTestId('restore-resume-cmd')).toHaveTextContent('claude --resume sess-123');
  });
});
