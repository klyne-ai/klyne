import { describe, it, expect } from 'vitest';
import { sessionUrl, paletteUrl, tabUrl, parseDashboardSearch } from './url-state';

describe('dashboard url-state', () => {
  it('sessionUrl appends ?session=<id> preserving existing params', () => {
    expect(sessionUrl('/projects?tab=worklog', 'abc123')).toBe('/projects?tab=worklog&session=abc123');
  });

  it('sessionUrl replaces an existing session param', () => {
    expect(sessionUrl('/insights?session=old', 'new1')).toBe('/insights?session=new1');
  });

  it('paletteUrl toggles ?palette=1', () => {
    expect(paletteUrl('/projects?tab=overview', true)).toBe('/projects?tab=overview&palette=1');
    expect(paletteUrl('/projects?palette=1&tab=overview', false)).toBe('/projects?tab=overview');
  });

  it('tabUrl swaps the tab param', () => {
    expect(tabUrl('/insights', 'daily')).toBe('/insights?tab=daily');
    expect(tabUrl('/insights?tab=overview', 'models')).toBe('/insights?tab=models');
  });

  it('parseDashboardSearch returns drawer + palette state', () => {
    const u = new URL('http://x/insights?session=abc&palette=1&tab=daily');
    expect(parseDashboardSearch(u)).toEqual({ session: 'abc', palette: true, tab: 'daily' });
  });
});
