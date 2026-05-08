/**
 * markdown.ts — tiny Markdown → sanitized HTML helper used by the cockpit
 * preview tiles (and elsewhere a small thread excerpt is rendered).
 *
 * Why these libraries:
 *   - marked: fast, GFM-friendly, ~40KB. We don't need plugins or async.
 *   - DOMPurify: defense-in-depth even though message content originates
 *     from CLIs the user is already running locally. Pasted input from a
 *     model can contain raw HTML / `<img>` / `<script>` payloads — never
 *     trust prose at render time.
 *
 * The renderer is sync and the output is a string. Components inject it
 * via {@html ...}, so sanitization here is the only line of defense.
 */

import { marked } from 'marked';
import DOMPurify from 'dompurify';

// One-time configuration — keep it loose enough for typical AI output
// (lists, code fences, tables, links) but strict enough that scripts and
// event handlers can't slip through.
marked.setOptions({
  gfm: true,
  breaks: true, // single newlines render as <br>, matching what users see in their terminal
});

/**
 * Render a Markdown string as sanitized HTML.
 *
 * Returns an empty string for nullish or empty input. Stripped of every
 * HTML attribute that DOMPurify considers dangerous (event handlers,
 * inline scripts, `javascript:` URLs).
 *
 * Callers must inject the result via Svelte's {@html} or React's
 * dangerouslySetInnerHTML — there's no way around that for rendered
 * markdown.
 */
export function renderMarkdown(input: string | null | undefined): string {
  if (!input) return '';
  const dirtyHtml = marked.parse(input, { async: false }) as string;
  return DOMPurify.sanitize(dirtyHtml, {
    USE_PROFILES: { html: true },
    ADD_ATTR: ['target', 'rel'],
  });
}
