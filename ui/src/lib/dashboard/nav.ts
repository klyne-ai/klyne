export type NavId = 'live' | 'productivity' | 'projects' | 'insights' | 'runbooks' | 'worklog';

export interface NavItem {
  id: NavId;
  label: string;
  href: string;
  icon: 'live' | 'productivity' | 'projects' | 'insights' | 'runbooks' | 'branch';
  section: 'workspace' | 'capture';
}

import { SHOW_REFLECTIONS } from '$lib/featureFlags';

const ALL_NAV: readonly NavItem[] = [
  { id: 'live',         label: 'Live',         href: '/',             icon: 'live',         section: 'workspace' },
  { id: 'productivity', label: 'Productivity', href: '/productivity', icon: 'productivity', section: 'workspace' },
  { id: 'projects',     label: 'Projects',     href: '/projects',     icon: 'projects',     section: 'workspace' },
  { id: 'insights',     label: 'Insights',     href: '/insights',     icon: 'insights',     section: 'workspace' },
  { id: 'runbooks',     label: 'Runbooks',     href: '/runbooks',     icon: 'runbooks',     section: 'workspace' },
  { id: 'worklog',      label: 'Worklog',      href: '/worklog',      icon: 'branch',       section: 'capture'   },
] as const;

// The Worklog tab is reflection-centric; hide it when reflections are off.
export const NAV: readonly NavItem[] = ALL_NAV.filter(
  (n) => SHOW_REFLECTIONS || n.id !== 'worklog',
);

export function navIdForPath(pathname: string): NavId {
  if (pathname.startsWith('/productivity')) return 'productivity';
  if (pathname.startsWith('/projects'))     return 'projects';
  if (pathname.startsWith('/insights'))     return 'insights';
  if (pathname.startsWith('/runbooks'))     return 'runbooks';
  if (pathname.startsWith('/worklog'))      return 'worklog';
  return 'live';
}

export function crumbsForNavId(id: NavId): readonly string[] {
  const item = NAV.find((n) => n.id === id);
  const section = item?.section ?? 'workspace';
  const label = item?.label ?? 'Live';
  return [section === 'workspace' ? 'Workspace' : 'Capture', label];
}
