/**
 * hidden-sessions.svelte.test.ts — Unit tests for the repo-level hidden
 * helpers (hiddenRepoNames / toggleHiddenRepo / clearHiddenRepos), mirroring
 * the session-level pattern. localStorage is provided by the jsdom env.
 */

import { describe, it, expect, beforeEach } from 'vitest';
import {
  hiddenRepoNames,
  toggleHiddenRepo,
  clearHiddenRepos
} from './hidden-sessions.svelte.js';

const REPO_STORAGE_KEY = 'klyne.productivity.hiddenRepos';

describe('hidden-repos helpers', () => {
  beforeEach(() => {
    clearHiddenRepos();
    localStorage.removeItem(REPO_STORAGE_KEY);
    clearHiddenRepos();
  });

  it('toggleHiddenRepo adds then removes a repo', () => {
    expect(hiddenRepoNames().has('acme/api')).toBe(false);
    toggleHiddenRepo('acme/api');
    expect(hiddenRepoNames().has('acme/api')).toBe(true);
    toggleHiddenRepo('acme/api');
    expect(hiddenRepoNames().has('acme/api')).toBe(false);
  });

  it('toggleHiddenRepo ignores empty names', () => {
    toggleHiddenRepo('');
    expect(hiddenRepoNames().size).toBe(0);
  });

  it('toggleHiddenRepo persists to localStorage', () => {
    toggleHiddenRepo('acme/web');
    const raw = localStorage.getItem(REPO_STORAGE_KEY);
    expect(raw).not.toBeNull();
    expect(JSON.parse(raw as string)).toContain('acme/web');
  });

  it('clearHiddenRepos drops all hidden repos', () => {
    toggleHiddenRepo('a');
    toggleHiddenRepo('b');
    expect(hiddenRepoNames().size).toBe(2);
    clearHiddenRepos();
    expect(hiddenRepoNames().size).toBe(0);
    expect(JSON.parse(localStorage.getItem(REPO_STORAGE_KEY) as string)).toEqual([]);
  });

  it('hiddenRepoNames returns a fresh Set reference per toggle (reactivity)', () => {
    const before = hiddenRepoNames();
    toggleHiddenRepo('x');
    const after = hiddenRepoNames();
    expect(after).not.toBe(before);
  });
});
