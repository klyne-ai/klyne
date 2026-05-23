export type NavId = 'live' | 'productivity' | 'projects' | 'insights' | 'runbooks' | 'advisors' | 'worklog';

export interface NavItem {
  id: NavId;
  label: string;
  href: string;
  icon: 'live' | 'productivity' | 'projects' | 'insights' | 'runbooks' | 'flame' | 'branch';
  section: 'workspace' | 'capture';
}

export const NAV: readonly NavItem[] = [
  { id: 'live',         label: 'Live',         href: '/',             icon: 'live',         section: 'workspace' },
  { id: 'productivity', label: 'Productivity', href: '/productivity', icon: 'productivity', section: 'workspace' },
  { id: 'projects',     label: 'Projects',     href: '/projects',     icon: 'projects',     section: 'workspace' },
  { id: 'insights',     label: 'Insights',     href: '/insights',     icon: 'insights',     section: 'workspace' },
  { id: 'runbooks',     label: 'Runbooks',     href: '/runbooks',     icon: 'runbooks',     section: 'workspace' },
  { id: 'advisors',     label: 'Advisors',     href: '/advisors',     icon: 'flame',        section: 'capture'   },
  { id: 'worklog',      label: 'Worklog',      href: '/worklog',      icon: 'branch',       section: 'capture'   },
] as const;

export function navIdForPath(pathname: string): NavId {
  if (pathname.startsWith('/productivity')) return 'productivity';
  if (pathname.startsWith('/projects'))     return 'projects';
  if (pathname.startsWith('/insights'))     return 'insights';
  if (pathname.startsWith('/runbooks'))     return 'runbooks';
  if (pathname.startsWith('/advisors'))     return 'advisors';
  if (pathname.startsWith('/worklog'))      return 'worklog';
  return 'live';
}

export function crumbsForNavId(id: NavId): readonly string[] {
  const item = NAV.find((n) => n.id === id);
  const section = item?.section ?? 'workspace';
  const label = item?.label ?? 'Live';
  return [section === 'workspace' ? 'Workspace' : 'Capture', label];
}
