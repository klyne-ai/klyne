export interface DashboardSearch {
  session: string | null;
  palette: boolean;
  tab: string | null;
  /** Optional message_id to scroll/highlight inside the session drawer
   * (set by SearchPalette when the user picks a hit). */
  msg: string | null;
}

export function parseDashboardSearch(u: URL): DashboardSearch {
  return {
    session: u.searchParams.get('session'),
    palette: u.searchParams.get('palette') === '1',
    tab: u.searchParams.get('tab'),
    msg: u.searchParams.get('msg'),
  };
}

function rebuild(pathAndSearch: string, mutate: (sp: URLSearchParams) => void): string {
  const [path, search = ''] = pathAndSearch.split('?');
  const sp = new URLSearchParams(search);
  mutate(sp);
  const out = sp.toString();
  return out ? `${path}?${out}` : path;
}

export function sessionUrl(current: string, id: string | null): string {
  return rebuild(current, (sp) => {
    if (id) sp.set('session', id);
    else { sp.delete('session'); sp.delete('msg'); }
  });
}

/** Open a session and request the drawer scroll to a specific message. */
export function sessionMessageUrl(current: string, sessionId: string, messageId: string): string {
  return rebuild(current, (sp) => {
    sp.set('session', sessionId);
    sp.set('msg', messageId);
  });
}

export function paletteUrl(current: string, open: boolean): string {
  return rebuild(current, (sp) => {
    if (open) sp.set('palette', '1');
    else sp.delete('palette');
  });
}

export function tabUrl(current: string, tab: string | null): string {
  return rebuild(current, (sp) => {
    if (tab) sp.set('tab', tab);
    else sp.delete('tab');
  });
}
