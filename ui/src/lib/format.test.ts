/**
 * format.test.ts — Unit tests for formatting utilities.
 */

import { describe, it, expect } from 'vitest';
import { kfmt, relAgo, costFmt, dayLabel, isoWeek } from './format.js';

describe('kfmt', () => {
  it('formats sub-thousand numbers as-is', () => {
    expect(kfmt(0)).toBe('0');
    expect(kfmt(999)).toBe('999');
  });

  it('formats thousands as K', () => {
    expect(kfmt(1000)).toBe('1K');
    expect(kfmt(5500)).toBe('6K');
  });

  it('formats millions as M', () => {
    expect(kfmt(1_200_000)).toBe('1.2M');
  });
});

describe('relAgo', () => {
  it('formats seconds', () => {
    expect(relAgo(30_000)).toBe('30s ago');
  });

  it('formats minutes', () => {
    expect(relAgo(5 * 60_000)).toBe('5m ago');
  });

  it('formats hours', () => {
    expect(relAgo(2 * 3_600_000)).toBe('2h ago');
  });

  it('formats days', () => {
    expect(relAgo(3 * 86_400_000)).toBe('3d ago');
  });
});

describe('costFmt', () => {
  it('returns em-dash when not priced', () => {
    expect(costFmt(0, false)).toBe('—');
    expect(costFmt(5.99, false)).toBe('—');
  });

  it('returns formatted dollar amount when priced', () => {
    expect(costFmt(1.234, true)).toBe('$1.23');
    expect(costFmt(0.005, true)).toBe('$0.01');
  });
});

describe('dayLabel', () => {
  it('returns Today for current date', () => {
    expect(dayLabel(Date.now())).toBe('Today');
  });

  it('returns Yesterday for yesterday', () => {
    expect(dayLabel(Date.now() - 86_400_000)).toBe('Yesterday');
  });

  it('returns formatted date for older dates', () => {
    // May 4, 2025
    const label = dayLabel(new Date('2025-05-04').getTime());
    expect(label).toMatch(/May\s+4/);
  });
});

describe('isoWeek', () => {
  // Each case: [UTC date, expected ISO-week label, why it matters]
  const cases: Array<[Date, string, string]> = [
    [new Date(Date.UTC(2026, 0, 1)),  '2026-W01', 'Jan 1 2026 (Thu) — first day of W01'],
    [new Date(Date.UTC(2026, 0, 4)),  '2026-W01', 'Jan 4 (Sun) is in W01 (always-in-W1 rule)'],
    [new Date(Date.UTC(2026, 0, 5)),  '2026-W02', 'Mon after that flips to W02'],
    [new Date(Date.UTC(2026, 4, 17)), '2026-W20', 'Sun 2026-05-17 — last day of W20'],
    [new Date(Date.UTC(2026, 4, 18)), '2026-W21', 'Mon flips week boundary'],
    [new Date(Date.UTC(2026, 11, 31)),'2026-W53', 'Dec 31 2026 (Thu) — 2026 has W53'],
    [new Date(Date.UTC(2027, 0, 1)),  '2026-W53', 'Jan 1 2027 (Fri) still in 2026-W53'],
    [new Date(Date.UTC(2027, 0, 4)),  '2027-W01', 'Jan 4 2027 (Mon) flips to W01'],
    [new Date(Date.UTC(2024, 11, 30)),'2025-W01', 'Mon 2024-12-30 belongs to 2025-W01'],
  ];

  for (const [d, want, why] of cases) {
    it(`${d.toISOString().slice(0, 10)} → ${want} (${why})`, () => {
      expect(isoWeek(d.getTime())).toBe(want);
    });
  }
});
