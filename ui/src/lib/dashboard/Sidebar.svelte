<script lang="ts">
  import Icon from './Icon.svelte';
  import Logo from '$lib/ui/Logo.svelte';
  import { NAV, type NavId } from './nav';
  import { goto } from '$app/navigation';

  interface Props {
    current: NavId;
    collapsed: boolean;
    liveCount: number;
    advisorCount: number;
    projectCount: number;
    onToggleCollapse: () => void;
    onOpenSearch: () => void;
  }
  const { current, collapsed, liveCount, advisorCount, projectCount, onToggleCollapse, onOpenSearch }: Props = $props();

  function meta(id: NavId): { text: string; kind: '' | 'live' | 'alert' } {
    switch (id) {
      case 'live': return liveCount > 0 ? { text: `${liveCount} live`, kind: 'live' } : { text: '', kind: '' };
      case 'productivity': return { text: 'today', kind: '' };
      case 'projects': return { text: String(projectCount), kind: '' };
      case 'insights': return { text: '1d', kind: '' };
      case 'runbooks': return { text: '', kind: '' };
      case 'advisors': return { text: String(advisorCount), kind: advisorCount > 0 ? 'alert' : '' };
      case 'worklog': return { text: '', kind: '' };
    }
  }
</script>

<aside class="sidebar" class:collapsed>
  <div class="sidebar-brand">
    <span class="mark"><Logo size={22} /></span>
    {#if !collapsed}<div class="name">klyne<em>·local</em></div>{/if}
    <button class="sidebar-toggle" onclick={onToggleCollapse} title={collapsed ? 'Expand' : 'Collapse'}>
      {collapsed ? '›' : '‹'}
    </button>
  </div>

  <button class="sidebar-search" onclick={onOpenSearch} title="Search messages…">
    <Icon name="search" />
    <span class="search-text">Search messages…</span>
    <span class="key">⌘K</span>
  </button>

  {#each ['workspace', 'capture'] as section}
    <div class="sidebar-section">{section === 'workspace' ? 'Workspace' : 'Capture'}</div>
    <nav class="sidebar-nav">
      {#each NAV.filter((n) => n.section === section) as item}
        {@const m = meta(item.id)}
        <a
          href={item.href}
          class="sidebar-nav-item"
          class:active={current === item.id}
          title={collapsed ? item.label : undefined}
          aria-current={current === item.id ? 'page' : undefined}
          onclick={(e) => { e.preventDefault(); void goto(item.href); }}
        >
          <span class="icon"><Icon name={item.icon} /></span>
          <span class="label">{item.label}</span>
          {#if m.text}
            <span class="meta" class:live={m.kind === 'live'} class:alert={m.kind === 'alert'} aria-hidden="true">{m.text}</span>
            <span class="vh">{item.label} · {m.text}</span>
          {/if}
        </a>
      {/each}
    </nav>
  {/each}

  <div class="sidebar-foot">
    <div class="avatar">M</div>
    <div class="who">
      <div class="name">Local user</div>
      <div class="sub">127.0.0.1:7878</div>
    </div>
    <div class="status" title="daemon running"></div>
  </div>
</aside>

<style>
  .sidebar {
    display: flex; flex-direction: column;
    border-right: 1px solid var(--border-hair);
    background: var(--bg);
    overflow: hidden;
    width: var(--sidebar-w, 220px);
    transition: width .18s cubic-bezier(.3,.7,.2,1);
  }
  .sidebar-brand {
    display: flex; align-items: center; gap: 10px;
    padding: 18px 18px 14px;
    position: relative;
  }
  .sidebar.collapsed .sidebar-brand { padding: 18px 14px 14px; justify-content: center; }
  .sidebar.collapsed .sidebar-brand .name { display: none; }
  /* Hide labels via CSS (display: none) rather than removing them from the DOM
     with {#if !collapsed}. Conditional render re-flows mid-transition and makes
     the sidebar appear to jump. CSS-hidden labels keep the width animation
     smooth and the surrounding nav rows perfectly stable. */
  .sidebar.collapsed .sidebar-search .search-text,
  .sidebar.collapsed .sidebar-search .key,
  .sidebar.collapsed .sidebar-section,
  .sidebar.collapsed .sidebar-nav-item .label,
  .sidebar.collapsed .sidebar-nav-item .meta,
  .sidebar.collapsed .sidebar-foot .who,
  .sidebar.collapsed .sidebar-foot .status {
    display: none;
  }
  .sidebar-toggle {
    position: absolute;
    right: -10px; top: 22px;
    width: 20px; height: 20px;
    border-radius: 50%;
    background: var(--bg-card-2);
    border: 1px solid var(--border-hair);
    color: var(--fg-muted);
    display: grid; place-items: center;
    cursor: pointer;
    z-index: 2;
    font-family: var(--font-mono); font-size: 10px;
  }
  .sidebar-toggle:hover { color: var(--fg); background: var(--bg-card); border-color: var(--border-soft); }
  .sidebar-brand .mark {
    width: 24px; height: 24px;
    display: grid; place-items: center;
    color: var(--fg);
  }
  .sidebar-brand .name { font-family: var(--font-mono); font-size: 14px; color: var(--fg); letter-spacing: -0.01em; }
  .sidebar-brand .name em { font-style: italic; color: var(--fg-soft); margin-left: 2px; }

  .sidebar-search {
    margin: 0 12px 8px;
    display: flex; align-items: center; gap: 8px;
    padding: 7px 10px;
    background: var(--bg-card); border: 1px solid var(--border-hair); border-radius: 8px;
    color: var(--fg-muted);
    font-family: var(--font-mono); font-size: 11.5px;
    cursor: pointer;
    white-space: nowrap;
  }
  .sidebar.collapsed .sidebar-search { margin: 0 12px 8px; padding: 7px; justify-content: center; }
  .sidebar.collapsed .sidebar-search > span:nth-child(2),
  .sidebar.collapsed .sidebar-search .key { display: none; }
  .sidebar-search > span:nth-child(2) { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; }
  .sidebar-search:hover { border-color: var(--border-soft); color: var(--fg-soft); }
  .sidebar-search .key {
    margin-left: auto;
    padding: 1px 6px; border-radius: 4px;
    background: var(--bg-card-2); border: 1px solid var(--border-hair);
    font-size: 10px; color: var(--fg-muted);
  }

  .sidebar-section {
    padding: 14px 18px 6px;
    font-family: var(--font-mono); font-size: 10px;
    letter-spacing: 0.14em; text-transform: uppercase;
    color: var(--fg-dim);
  }
  .sidebar.collapsed .sidebar-section { visibility: hidden; height: 14px; padding: 6px 0; }

  .sidebar-nav { display: flex; flex-direction: column; padding: 0 8px; gap: 1px; }
  .sidebar-nav-item {
    display: flex; align-items: center; gap: 10px;
    padding: 8px 10px;
    border-radius: 7px;
    color: var(--fg-soft); font-size: 13px;
    cursor: pointer;
    border: 1px solid transparent;
    position: relative;
  }
  .sidebar.collapsed .sidebar-nav-item { justify-content: center; padding: 9px 0; }
  .sidebar.collapsed .sidebar-nav-item .label { display: none; }
  .sidebar.collapsed .sidebar-nav-item .meta {
    position: absolute;
    top: 2px; right: 4px;
    font-size: 0; width: 8px; height: 8px;
    background: var(--fg-muted); border-radius: 50%;
    padding: 0;
  }
  .sidebar.collapsed .sidebar-nav-item .meta.live { background: var(--ok); }
  .sidebar.collapsed .sidebar-nav-item .meta.alert { background: var(--alert); }
  .sidebar-nav-item:hover { background: var(--bg-card); color: var(--fg); }
  .sidebar-nav-item.active {
    background: var(--bg-card);
    color: var(--fg);
    border-color: var(--border-hair);
  }
  .sidebar-nav-item .icon {
    width: 16px; height: 16px;
    flex-shrink: 0;
    display: grid; place-items: center;
    color: var(--fg-muted);
  }
  .sidebar-nav-item.active .icon { color: var(--accent); }
  .sidebar-nav-item .label { flex: 1; }
  .sidebar-nav-item .meta {
    font-family: var(--font-mono); font-size: 10.5px; color: var(--fg-dim);
    white-space: nowrap; flex-shrink: 0;
  }
  .sidebar-nav-item .meta.live { color: var(--ok); }
  .sidebar-nav-item .meta.alert { color: var(--alert); }

  .sidebar-foot {
    margin-top: auto;
    padding: 12px 14px;
    border-top: 1px solid var(--border-hair);
    display: flex; align-items: center; gap: 10px;
  }
  .sidebar.collapsed .sidebar-foot { justify-content: center; padding: 12px 0; }
  .sidebar.collapsed .sidebar-foot .who,
  .sidebar.collapsed .sidebar-foot .status { display: none; }
  .sidebar-foot .who { flex: 1; min-width: 0; }
  .sidebar-foot .who .name { font-size: 12px; color: var(--fg); }
  .sidebar-foot .who .sub { font-family: var(--font-mono); font-size: 10px; color: var(--fg-dim); }
  .sidebar-foot .avatar {
    width: 26px; height: 26px; border-radius: 50%;
    background: var(--accent); color: var(--bg);
    display: grid; place-items: center;
    font-family: var(--font-mono); font-size: 11px; font-weight: 600;
  }
  .sidebar-foot .status {
    width: 8px; height: 8px; border-radius: 50%;
    background: var(--ok);
    box-shadow: 0 0 0 3px color-mix(in oklch, var(--ok) 18%, transparent);
  }

  /* Visually hidden — keeps content in the AT tree without occupying layout space. */
  .vh {
    position: absolute;
    width: 1px; height: 1px;
    padding: 0; margin: -1px;
    overflow: hidden;
    clip: rect(0,0,0,0);
    white-space: nowrap;
    border-width: 0;
  }
</style>
