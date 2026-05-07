/**
 * ui-primitives.test.ts — Smoke tests for new v1.1 UI primitives.
 *
 * These verify the components render without throwing.
 * Full visual coverage will come in E2E; unit tests only assert
 * "renders and doesn't crash" per v1.1 mandate.
 */

import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import StatusBadge from './StatusBadge.svelte';
import CliBadge from './CliBadge.svelte';
import Sparkline from './Sparkline.svelte';
import BarColumns from './BarColumns.svelte';

// ---------------------------------------------------------------------------
// StatusBadge
// ---------------------------------------------------------------------------

describe('StatusBadge', () => {
  it('renders active status', () => {
    const { container } = render(StatusBadge, { status: 'active' });
    expect(container.querySelector('.ad-badge')).toBeTruthy();
    expect(container.textContent).toContain('active');
  });

  it('renders idle status', () => {
    const { container } = render(StatusBadge, { status: 'idle' });
    expect(container.querySelector('.ad-badge--idle')).toBeTruthy();
    expect(container.textContent).toContain('idle');
  });

  it('renders compacted status', () => {
    const { container } = render(StatusBadge, { status: 'compacted' });
    expect(container.querySelector('.ad-badge--compact')).toBeTruthy();
    expect(container.textContent).toContain('compacted');
  });
});

// ---------------------------------------------------------------------------
// CliBadge
// ---------------------------------------------------------------------------

describe('CliBadge', () => {
  it('renders claude badge', () => {
    const { container } = render(CliBadge, { cli: 'claude' });
    expect(container.querySelector('.ad-badge--claude')).toBeTruthy();
    expect(container.textContent).toContain('claude');
  });

  it('renders codex badge', () => {
    const { container } = render(CliBadge, { cli: 'codex' });
    expect(container.querySelector('.ad-badge--codex')).toBeTruthy();
    expect(container.textContent).toContain('codex');
  });
});

// ---------------------------------------------------------------------------
// Sparkline
// ---------------------------------------------------------------------------

describe('Sparkline', () => {
  it('renders SVG without throwing', () => {
    const { container } = render(Sparkline, { data: [1, 2, 3, 4, 5] });
    expect(container.querySelector('svg')).toBeTruthy();
  });

  it('renders with empty data without throwing', () => {
    const { container } = render(Sparkline, { data: [] });
    expect(container.querySelector('svg')).toBeTruthy();
  });

  it('renders with custom height', () => {
    const { container } = render(Sparkline, { data: [10, 20], height: 60 });
    const svg = container.querySelector('svg');
    expect(svg).toBeTruthy();
  });
});

// ---------------------------------------------------------------------------
// BarColumns
// ---------------------------------------------------------------------------

describe('BarColumns', () => {
  it('renders bars without throwing', () => {
    const { container } = render(BarColumns, { data: [10, 20, 30] });
    expect(container.firstChild).toBeTruthy();
  });

  it('renders with empty data without throwing', () => {
    const { container } = render(BarColumns, { data: [] });
    expect(container.firstChild).toBeTruthy();
  });

  it('renders correct number of bar divs', () => {
    const { container } = render(BarColumns, { data: [1, 2, 3] });
    // The top-level div contains one child div per bar
    const wrapper = container.firstElementChild;
    expect(wrapper?.children.length).toBe(3);
  });
});
