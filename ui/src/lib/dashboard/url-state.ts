export interface DashboardSearch {
  session: string | null;
  palette: boolean;
  tab: string | null;
}

export function parseDashboardSearch(u: URL): DashboardSearch {
  return {
    session: u.searchParams.get('session'),
    palette: u.searchParams.get('palette') === '1',
    tab: u.searchParams.get('tab'),
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
    else sp.delete('session');
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
