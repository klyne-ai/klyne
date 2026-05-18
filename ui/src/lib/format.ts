/**
 * format.ts — Display formatting utilities for klyne v1.1 UI.
 */

/** kfmt — format a number as compact K/M notation. */
export function kfmt(n: number): string {
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + 'M';
  if (n >= 1_000) return (n / 1_000).toFixed(0) + 'K';
  return String(n);
}

/** relTime — format an epoch-ms timestamp as a relative time string. */
export function relTime(tsMs: number): string {
  return relAgo(Date.now() - tsMs);
}

/** relAgo — format a millisecond delta as a relative time string. */
export function relAgo(deltaMs: number): string {
  const s = Math.floor(deltaMs / 1000);
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  const d = Math.floor(h / 24);
  return `${d}d ago`;
}

/** costFmt — format a cost value or return em dash if not priced. */
export function costFmt(cost: number, priced: boolean): string {
  if (!priced) return '—';
  return `$${cost.toFixed(2)}`;
}

/** dayLabel — format an epoch-ms timestamp as "Today" / "Yesterday" / "May 4". */
export function dayLabel(tsMs: number): string {
  const date = new Date(tsMs);
  const now = new Date();
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const yesterday = new Date(today.getTime() - 86_400_000);
  const dateDay = new Date(date.getFullYear(), date.getMonth(), date.getDate());

  if (dateDay.getTime() === today.getTime()) return 'Today';
  if (dateDay.getTime() === yesterday.getTime()) return 'Yesterday';

  return date.toLocaleDateString('en-US', { month: 'short', day: 'numeric' });
}

/**
 * ISO 8601 week label for an epoch-ms timestamp. UTC matches the
 * backend's IsoWeek helper (internal/worklog/export.go:31) so server
 * and UI agree on bucket boundaries.
 *
 * Algorithm: find the Thursday of the same week (ISO 8601 anchors
 * weeks on Thursday), then count weeks from year start.
 */
export function isoWeek(ts: number): string {
  const d = new Date(ts);
  const utc = new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), d.getUTCDate()));
  const dayNum = utc.getUTCDay() === 0 ? 7 : utc.getUTCDay();
  utc.setUTCDate(utc.getUTCDate() + 4 - dayNum);
  const yearStart = new Date(Date.UTC(utc.getUTCFullYear(), 0, 1));
  const week = Math.ceil(((utc.getTime() - yearStart.getTime()) / 86_400_000 + 1) / 7);
  return `${utc.getUTCFullYear()}-W${String(week).padStart(2, '0')}`;
}
