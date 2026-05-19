import { describe, it, expect } from 'vitest';
import { sessionHref, projectHref, sessionBackHref } from './navlinks';

describe('sessionHref', () => {
  it('builds a Work-context session link when basePath is empty', () => {
    expect(sessionHref('', 'abc 123')).toBe('/sessions/abc%20123');
  });
  it('builds an Insights-context session link under /insights', () => {
    expect(sessionHref('/insights', 'abc123')).toBe('/insights/sessions/abc123');
  });
});

describe('projectHref', () => {
  it('builds a Work-context project link when basePath is empty', () => {
    expect(projectHref('', 'oms/svc')).toBe('/projects/oms%2Fsvc');
  });
  it('builds an Insights-context project link under /insights', () => {
    expect(projectHref('/insights', 'oms-service')).toBe('/insights/projects/oms-service');
  });
});

describe('sessionBackHref', () => {
  it('Work + known project → /projects/<name>', () => {
    expect(sessionBackHref('', 'oms-service')).toBe('/projects/oms-service');
  });
  it('Work + unknown project → /projects', () => {
    expect(sessionBackHref('', '')).toBe('/projects');
  });
  it('Insights + known project → /insights/projects/<name>', () => {
    expect(sessionBackHref('/insights', 'oms-service')).toBe('/insights/projects/oms-service');
  });
  it('Insights + unknown project → /insights', () => {
    expect(sessionBackHref('/insights', '')).toBe('/insights');
  });
});
