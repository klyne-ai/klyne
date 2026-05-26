import { describe, it, expect } from 'vitest';
import { sessionUrl, paletteUrl, tabUrl, parseDashboardSearch, sessionMessageUrl } from './url-state';

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
    expect(parseDashboardSearch(u)).toEqual({ session: 'abc', palette: true, tab: 'daily', msg: null });
  });

  it('parseDashboardSearch picks up the msg jump param', () => {
    const u = new URL('http://x/insights?session=abc&msg=m-123');
    expect(parseDashboardSearch(u)).toEqual({ session: 'abc', palette: false, tab: null, msg: 'm-123' });
  });

  it('sessionUrl with id=null removes both session and msg (drawer close)', () => {
    expect(sessionUrl('/insights?session=abc&msg=m1&tab=daily', null)).toBe('/insights?tab=daily');
    expect(sessionUrl('/insights?session=abc', null)).toBe('/insights');
  });

  it('sessionMessageUrl sets both params together', () => {
    expect(sessionMessageUrl('/insights?tab=daily', 'sess-9', 'msg-7'))
      .toBe('/insights?tab=daily&session=sess-9&msg=msg-7');
  });

  it('parseDashboardSearch on a bare URL returns all-null/false', () => {
    expect(parseDashboardSearch(new URL('http://x/'))).toEqual({ session: null, palette: false, tab: null, msg: null });
  });

  it('tabUrl with tab=null deletes the param', () => {
    expect(tabUrl('/insights?tab=daily', null)).toBe('/insights');
    expect(tabUrl('/insights', null)).toBe('/insights');
  });
});
