<!--
  Productivity dashboard — V4 Synthesis layout.

  Ported pixel-faithfully from Anthropic Design handoff bundle
  (Productivity Redesign.html → v4-combined.jsx → tokens.css).

  Layout, top → bottom:
    1. TopRail — KLYNE/ productivity · date pills · loaded ago + refresh
    2. Masthead — weekday · standup digest · big mono date · reflection
       pills + sub-line · Copy-for-standup button + bullet count
    3. Triage — two urgent alert cards + ALL RISKS sidebar (3-col)
    4. Hero — kicker + marked-prose headline + numbered evidence bullets
    5. Metrics — 6-col strip with vertical borders
    6. Services — sortable table + collapsible ephemeral row
    7. Timeline — 240px sidebar + timeline canvas

  Data: drives off the existing /api/productivity payload. All
  derivations (risks histogram, important-session count, top-bullets,
  peak parallelism) computed client-side. Reflection/credential-flag
  meta is best-effort; Phase 2 (Go-side) will populate the
  worklog-entries-pending + next-scheduled fields directly.
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import { fetchProductivityDates, runReflect, type ProductivityReport, type ProductivityService, type ProductivitySessionStat } from '$lib/api.js';
  import { applyHiddenFilter, hiddenSessionIds, hideMany, clearHidden } from '$lib/hidden-sessions.svelte';
  import ConcurrencyTimeline from '$lib/components/productivity/ConcurrencyTimeline.svelte';

  const STORAGE_KEY = 'klyne.productivity.range';
  const REPORT_KEY  = 'klyne.productivity.lastReport';

  let rep = $state<ProductivityReport | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let loadedAt = $state<number | null>(null);
  let since = $state<number>(0);
  let until = $state<number>(0);
  let copied = $state(false);
  // Reflect-run state: drives the inline "Run /klyne:reflect now" button
  // surfaced when reflection_status != "current". We track per-project
  // progress so a multi-project window (e.g. "today" spanning klyne +
  // operations-app + trackIt) shows a running counter instead of a
  // single opaque spinner. Sequential dispatch — each /klyne:reflect
  // spawns a full claude subprocess; parallel fan-out would burn
  // resources and could trip rate limits.
  let reflectRunning = $state(false);
  let reflectDone   = $state(0);
  let reflectTotal  = $state(0);
  let reflectError  = $state<string | null>(null);
  async function runReflectForAllProjects() {
    if (reflectRunning || !rep) return;
    const projects = Array.from(new Set(
      (rep.services ?? [])
        .map(s => s.project_path)
        .filter((p): p is string => !!p && p.trim() !== ''),
    ));
    if (projects.length === 0) return;
    reflectRunning = true;
    reflectError = null;
    reflectDone = 0;
    reflectTotal = projects.length;
    try {
      for (const p of projects) {
        try {
          await runReflect(p);
        } catch (e) {
          // Capture but keep going — one project's reflect failure
          // shouldn't prevent the others from running. The user sees
          // the first error after the batch completes.
          if (!reflectError) {
            reflectError = e instanceof Error ? e.message : String(e);
          }
        } finally {
          reflectDone += 1;
        }
      }
      // After all reflections land, force-refresh the dashboard so the
      // newly-written worklog_reflections rows surface immediately.
      await load({ refresh: true });
    } finally {
      reflectRunning = false;
    }
  }
  // Four preset windows only. The dashboard is snapshot-backed
  // (daily_productivity_snapshot, migration 021) so past-day rendering
  // is deterministic across reloads. Presets stay limited to common
  // windows; the calendar adds explicit single-day historical picks.
  type PresetRangeKey = 'today' | 'yesterday' | 'this_week' | 'last_week';
  type RangeKey = PresetRangeKey | 'custom';
  const RANGE_KEYS: readonly PresetRangeKey[] = ['today', 'yesterday', 'this_week', 'last_week'] as const;
  const RANGE_LABELS: Record<PresetRangeKey, string> = {
    today: 'today',
    yesterday: 'yesterday',
    this_week: 'this week',
    last_week: 'last week',
  };
  function rangeLabel(key: RangeKey): string {
    return key === 'custom' ? 'calendar day' : RANGE_LABELS[key];
  }
  let rangeKey = $state<RangeKey>('today');
  let calendarOpen = $state(false);
  let availableDays = $state<string[]>([]);
  let calendarMonth = $state<Date>(new Date());
  let datesError = $state<string | null>(null);

  interface CachedReport { since: number; until: number; loadedAt: number; rep: ProductivityReport; }
  function loadCached(): CachedReport | null {
    try { const raw = localStorage.getItem(REPORT_KEY); if (!raw) return null; const p = JSON.parse(raw); if (typeof p?.since==='number'&&typeof p?.until==='number'&&typeof p?.loadedAt==='number'&&p?.rep) return p as CachedReport; } catch {}
    return null;
  }
  function saveCached(r: ProductivityReport, s: number, u: number, at: number) { try { localStorage.setItem(REPORT_KEY, JSON.stringify({ since: s, until: u, loadedAt: at, rep: r })); } catch {} }
  function saveRange(s: number, u: number) { try { localStorage.setItem(STORAGE_KEY, JSON.stringify({ since: s, until: u })); } catch {} }
  function loadSaved(): {since:number;until:number}|null { try { const raw=localStorage.getItem(STORAGE_KEY); if(!raw) return null; const p=JSON.parse(raw); if(typeof p?.since==='number'&&typeof p?.until==='number') return {since:p.since,until:p.until}; } catch {} return null; }

  // Week boundaries use Monday as the first day of the week (ISO 8601).
  // "This week" = Mon 00:00 of the current week → now.
  // "Last week" = Mon 00:00 of the previous week → Sun 23:59:59.999.
  function startOfWeek(at: Date): Date {
    const d = new Date(at.getFullYear(), at.getMonth(), at.getDate(), 0, 0, 0, 0);
    // getDay(): Sun=0, Mon=1, ..., Sat=6. Days back to Monday:
    //   Mon→0, Tue→1, ..., Sun→6.
    const back = (d.getDay() + 6) % 7;
    d.setDate(d.getDate() - back);
    return d;
  }
  function rangeFor(key: PresetRangeKey): { since: number; until: number } {
    const now = new Date();
    const midnight = new Date(now.getFullYear(), now.getMonth(), now.getDate(), 0, 0, 0, 0).getTime();
    const day = 24*60*60*1000;
    switch (key) {
      case 'today':     return { since: midnight, until: Date.now() };
      case 'yesterday': return { since: midnight - day, until: midnight - 1 };
      case 'this_week': {
        const weekStart = startOfWeek(now).getTime();
        return { since: weekStart, until: Date.now() };
      }
      case 'last_week': {
        const thisWeekStart = startOfWeek(now).getTime();
        return { since: thisWeekStart - 7*day, until: thisWeekStart - 1 };
      }
    }
  }
  function detectRangeKey(s: number, u: number): RangeKey {
    // Compare against the four preset windows by snapping each to its
    // day-resolution start. Multi-day windows match against the week
    // presets by date-equality of the start day. Anything else is a
    // calendar/custom day.
    const now = new Date();
    const todayMid = new Date(now.getFullYear(), now.getMonth(), now.getDate(), 0, 0, 0, 0).getTime();
    const day = 86_400_000;
    const thisWeekStart = startOfWeek(now).getTime();
    const lastWeekStart = thisWeekStart - 7*day;

    const sameMidnight = (a: number, b: number) => Math.abs(a - b) < 1000;
    if (sameMidnight(s, todayMid)) return 'today';
    if (sameMidnight(s, todayMid - day)) return 'yesterday';
    if (sameMidnight(s, thisWeekStart)) return 'this_week';
    if (sameMidnight(s, lastWeekStart)) return 'last_week';
    return 'custom';
  }
  function syncUrl(s: number, u: number) {
    const q = new URLSearchParams(window.location.search);
    q.set('since', String(s)); q.set('until', String(u));
    window.history.replaceState(null, '', `${window.location.pathname}?${q.toString()}${window.location.hash}`);
  }
  // Inflight controller so a stuck fetch can be cancelled (e.g.
  // daemon restart leaves the previous request orphaned). Without
  // this the await never resolves and the loading state is permanent.
  let inflight: AbortController | null = null;
  async function load(opts?: { refresh?: boolean }) {
    // Cancel any prior request so we don't pile up.
    if (inflight) { inflight.abort(); inflight = null; }
    const ctrl = new AbortController(); inflight = ctrl;
    const timeoutId = window.setTimeout(() => ctrl.abort(), 30_000); // 30s hard cap
    loading = true; error = null;
    try {
      const qs = new URLSearchParams();
      qs.set('since', String(since));
      qs.set('until', String(until));
      if (opts?.refresh) qs.set('refresh', '1');
      const res = await fetch(`/api/productivity?${qs.toString()}`, { signal: ctrl.signal });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const json = await res.json() as ProductivityReport;
      rep = json;
      loadedAt = Date.now();
      saveCached(rep, since, until, loadedAt);
    } catch (e) {
      if ((e as Error).name === 'AbortError') {
        error = 'Request timed out (30s). The daemon may be processing a large window — click Retry or pick a shorter range.';
      } else {
        error = e instanceof Error ? e.message : 'failed to load';
      }
    } finally {
      window.clearTimeout(timeoutId);
      if (inflight === ctrl) inflight = null;
      loading = false;
    }
  }
  function pickRange(k: PresetRangeKey) {
    if (rangeKey === k) return;
    rangeKey = k;
    calendarOpen = false;
    const r = rangeFor(k);
    since = r.since; until = r.until;
    saveRange(since, until); syncUrl(since, until);
    void load();
  }
  function refresh() { void load({ refresh: true }); }

  function localDateKey(ms: number): string {
    const d = new Date(ms);
    const y = d.getFullYear();
    const m = String(d.getMonth() + 1).padStart(2, '0');
    const day = String(d.getDate()).padStart(2, '0');
    return `${y}-${m}-${day}`;
  }
  function parseLocalDay(day: string): Date {
    const [y, m, d] = day.split('-').map(Number);
    return new Date(y, m - 1, d, 0, 0, 0, 0);
  }
  function dayWindow(day: string): { since: number; until: number } {
    const d = parseLocalDay(day);
    const start = d.getTime();
    const today = localDateKey(Date.now());
    const end = day === today ? Date.now() : new Date(d.getFullYear(), d.getMonth(), d.getDate(), 23, 59, 59, 999).getTime();
    return { since: start, until: end };
  }
  async function loadAvailableDays() {
    try {
      const res = await fetchProductivityDates();
      availableDays = res.days ?? [];
      const selected = localDateKey(since || Date.now());
      const anchor = availableDays.includes(selected) ? selected : (res.max_day || selected);
      calendarMonth = parseLocalDay(anchor);
      datesError = null;
    } catch (e) {
      datesError = e instanceof Error ? e.message : 'failed to load dates';
    }
  }
  function selectCalendarDay(day: string) {
    if (!availableDays.includes(day)) return;
    const r = dayWindow(day);
    since = r.since; until = r.until;
    rangeKey = detectRangeKey(since, until);
    calendarOpen = false;
    saveRange(since, until); syncUrl(since, until);
    void load();
  }
  function shiftCalendarMonth(delta: number) {
    calendarMonth = new Date(calendarMonth.getFullYear(), calendarMonth.getMonth() + delta, 1);
  }
  function monthKey(d: Date): string {
    return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}`;
  }
  function monthLabel(d: Date): string {
    return d.toLocaleDateString('en-US', { month: 'long', year: 'numeric' });
  }
  const availableDaySet = $derived(new Set(availableDays));
  const selectedCalendarDay = $derived(localDateKey(since || Date.now()));
  const calendarCells = $derived.by(() => {
    const first = new Date(calendarMonth.getFullYear(), calendarMonth.getMonth(), 1);
    const start = new Date(first);
    start.setDate(first.getDate() - first.getDay());
    const cells: { key: string; day: string; label: number; inMonth: boolean; hasData: boolean; selected: boolean }[] = [];
    for (let i = 0; i < 42; i++) {
      const d = new Date(start);
      d.setDate(start.getDate() + i);
      const day = localDateKey(d.getTime());
      cells.push({
        key: day,
        day,
        label: d.getDate(),
        inMonth: d.getMonth() === calendarMonth.getMonth(),
        hasData: availableDaySet.has(day),
        selected: day === selectedCalendarDay,
      });
    }
    return cells;
  });
  const canGoPrevMonth = $derived(availableDays.length === 0 || monthKey(calendarMonth) > monthKey(parseLocalDay(availableDays[0])));
  const canGoNextMonth = $derived(availableDays.length === 0 || monthKey(calendarMonth) < monthKey(parseLocalDay(availableDays[availableDays.length - 1])));

  onMount(() => {
    // URL params and localStorage only hint at WHICH preset chip should
    // be active. The actual (since, until) window is always recomputed
    // from rangeFor(key) so a stale tuple (e.g. URL saved on May 24
    // when "yesterday" was May 23, opened on May 26) collapses to the
    // current preset's window instead of being trusted verbatim. The
    // old behaviour fetched May 23's snapshot for a May-26 "yesterday"
    // chip click because the URL was treated as authoritative.
    const q = new URLSearchParams(window.location.search);
    const qs = Number(q.get('since')), qu = Number(q.get('until'));
    let hintedKey: RangeKey = 'today';
    if (Number.isFinite(qs) && qs > 0 && Number.isFinite(qu) && qu > 0) {
      hintedKey = detectRangeKey(qs, qu);
    } else {
      const saved = loadSaved();
      if (saved) hintedKey = detectRangeKey(saved.since, saved.until);
    }
    rangeKey = hintedKey;
    const r = hintedKey === 'custom'
      ? (Number.isFinite(qs) && qs > 0 && Number.isFinite(qu) && qu > 0
          ? { since: qs, until: qu }
          : loadSaved() ?? rangeFor('today'))
      : rangeFor(hintedKey);
    since = r.since; until = r.until;
    saveRange(since, until); syncUrl(since, until);
    // Cache validity now also requires the cached window to match the
    // freshly-recomputed range — same defence against post-midnight
    // staleness as the URL guard above.
    const c = loadCached();
    if (c && c.since === since && c.until === until) { rep = c.rep; loadedAt = c.loadedAt; loading = false; }
    else void load();
    void loadAvailableDays();
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  });

  function onKey(ev: KeyboardEvent) {
    const tgt = ev.target as HTMLElement | null;
    if (tgt && (tgt.tagName === 'INPUT' || tgt.tagName === 'TEXTAREA')) return;
    if ((ev.metaKey || ev.ctrlKey) && ev.key.toLowerCase() === 'c' && !window.getSelection()?.toString()) {
      ev.preventDefault(); void copyForStandup();
    }
  }

  // ─── helpers ────────────────────────────────────────────────────
  // Subscribe to the reactive hidden-set so the page re-derives when
  // the user toggles a hide/show. The derived `visible` then strips
  // hidden sessions AND recomputes total_active_minutes / minutes_by_cli.
  const hiddenIds = $derived(hiddenSessionIds());
  const visible = $derived.by(() => {
    // Re-read hiddenIds.size so this derivation tracks the reactive set.
    void hiddenIds.size;
    return rep ? applyHiddenFilter(rep) : null;
  });

  // Per-service "hide all from this repo" — collect every session_id
  // in rep.sessions whose repo matches, then hand them to the store.
  function hideService(repo: string) {
    if (!rep) return;
    const ids = (rep.sessions ?? []).filter(s => s.repo === repo).map(s => s.session_id);
    if (ids.length > 0) hideMany(ids);
  }
  function showAllHidden() { clearHidden(); }

  function fmtMinutes(m: number): string {
    if (!Number.isFinite(m) || m <= 0) return '0m';
    const h = Math.floor(m / 60), mm = m % 60;
    return h > 0 ? `${h}h ${mm}m` : `${mm}m`;
  }
  function dayParts(day: string): { weekday: string; pretty: string } {
    if (!day) return { weekday: '—', pretty: '' };
    const d = new Date(day + 'T00:00:00');
    if (Number.isNaN(d.getTime())) return { weekday: '—', pretty: day };
    return {
      weekday: d.toLocaleDateString('en-US', { weekday: 'long' }).toUpperCase(),
      pretty:  d.toLocaleDateString('en-GB', { day: 'numeric', month: 'long', year: 'numeric' }),
    };
  }
  function ago(ms: number | null): string {
    if (!ms) return '';
    const s = Math.floor((Date.now() - ms) / 1000);
    if (s < 60) return `loaded ${s}s ago`;
    const m = Math.floor(s / 60);
    if (m < 60) return `loaded ${m}m ago`;
    const h = Math.floor(m / 60);
    return `loaded ${h}h ago`;
  }
  function shortId(s: string, n = 7): string { return s ? s.slice(0, n) : ''; }

  // stripMarkdown — minimal scrub for headline rendering. We don't
  // want a full Markdown parser in the page; we just need to avoid
  // showing literal "##", "**", "- " characters in the hero h2 when
  // reflection_markdown is a multi-line markdown blob.
  function stripMarkdown(s: string): string {
    return s
      .replace(/^[-*]\s+/gm, '')          // leading bullets
      .replace(/^#{1,6}\s+/gm, '')        // headings
      .replace(/\*\*([^*]+)\*\*/g, '$1')  // bold
      .replace(/__([^_]+)__/g, '$1')      // bold (alt)
      .replace(/\*([^*]+)\*/g, '$1')      // italic
      .replace(/_([^_]+)_/g, '$1')        // italic (alt)
      .replace(/`([^`]+)`/g, '$1')        // inline code
      .replace(/\[([^\]]+)\]\([^)]+\)/g, '$1') // links
      .trim();
  }
  // headlineFromMarkdown — pick the first SUBSTANTIVE clause from a
  // reflection blob and cap it to tweet length. The reflection
  // markdown typically opens with `## Shipped` followed by a bullet
  // list; we want the bullet contents, not the heading word, so we
  // skip paragraphs that are too short to be informative (< 30 chars).
  function headlineFromMarkdown(md: string, maxChars = 220): string {
    const cleaned = stripMarkdown(md);
    if (!cleaned) return '';
    const paras = cleaned.split(/\n{2,}/).map(p => p.replace(/\n+/g, ' ').replace(/\s+/g, ' ').trim()).filter(p => p.length > 0);
    // Prefer the first paragraph that reads like prose (length >= 30),
    // and merge consecutive short bullets if necessary.
    let flat = paras.find(p => p.length >= 30);
    if (!flat) {
      flat = paras.join(' · ');
    }
    if (!flat) return '';
    if (flat.length <= maxChars) return flat;
    const slice = flat.slice(0, maxChars);
    const lastStop = Math.max(slice.lastIndexOf('. '), slice.lastIndexOf('; '));
    return (lastStop > maxChars * 0.6 ? slice.slice(0, lastStop + 1) : slice.replace(/\s\S*$/, '')) + '…';
  }
  // meaningfulSessions — phantom 0-minute rows clutter the
  // numerator/denominator. Filter to sessions that actually
  // contributed wall-clock minutes.
  function meaningfulSessions(rep: ProductivityReport, min = 1): ProductivitySessionStat[] {
    return (rep.sessions ?? []).filter(s => s.active_minutes >= min);
  }

  // ── reflection-markdown parsing ─────────────────────────────────
  //
  // The reflection_markdown body is the right substrate for the
  // WHAT WAS DONE bullets — each line is one accomplishment with
  // prose + evidence — NOT the per-session metadata list. This
  // parser extracts:
  //   - opening narrative bullets (before any "**Section**" heading)
  //   - classifies each bullet's chip (SHIPPED / RISK / FLAG / MERGED / NOTE)
  //   - separates the title (first sentence) from the body
  //   - pulls short evidence (session IDs, SHAs, ticket refs) to the
  //     right column
  //
  // Falls back to the deterministic per-session list when there's no
  // reflection markdown.
  interface ReflectionBullet {
    chip: 'SHIPPED'|'RISK'|'FLAG'|'MERGED'|'NOTE';
    title: string;
    body: string;
    evidence: string[];
    repo?: string;
  }
  function classifyBullet(text: string): ReflectionBullet['chip'] {
    const t = text.toLowerCase();
    if (/credential|secret|password|plaintext|leaked/.test(t)) return 'FLAG';
    if (/stalled|blocked|did not run|didn't run|gap|missing|outage|broken/.test(t)) return 'RISK';
    if (/manual conflict|merge|merged|rebase|resolve/.test(t)) return 'MERGED';
    if (/shipped|committed|pushed|landed|deployed|published/.test(t)) return 'SHIPPED';
    return 'NOTE';
  }
  function extractEvidence(text: string): { body: string; evidence: string[] } {
    const ev: string[] = [];
    // `(evidence: id1, id2, ...)` blocks — strip and harvest the IDs.
    const evMatch = text.match(/\(evidence:\s*([^)]+)\)/i);
    let body = text;
    if (evMatch) {
      const ids = evMatch[1].split(/[,;\s]+/).filter(Boolean);
      for (const id of ids) ev.push(id.length > 9 ? id.slice(0, 8) : id);
      body = body.replace(evMatch[0], '').trim();
    }
    // Short SHAs `(abc1234)` or `abc1234`.
    const shaMatch = body.match(/\(([0-9a-f]{7,12})\)/i);
    if (shaMatch) { ev.push(shaMatch[1].slice(0, 7)); body = body.replace(shaMatch[0], '').trim(); }
    // Ticket refs `[CLI-1415]` — keep inline in the body, also surface.
    const ticketMatches = body.match(/\[([A-Z]{2,}-\d+)\]/g);
    if (ticketMatches) for (const t of ticketMatches.slice(0, 2)) ev.push(t.replace(/[\[\]]/g, ''));
    // Trim trailing punctuation left dangling.
    body = body.replace(/\s+([.,;:])$/, '$1').trim();
    return { body, evidence: ev };
  }
  function splitTitle(text: string): { title: string; body: string } {
    // Split on first "—", ":", or end of first sentence — whichever
    // comes first. Cap title at ~90 chars.
    const first = text.search(/[—:]/);
    const dot   = text.search(/\.\s/);
    let pivot = -1;
    if (first > 0 && first < 90 && (dot < 0 || first < dot)) pivot = first;
    else if (dot > 0 && dot < 90) pivot = dot;
    if (pivot < 0) {
      // No natural pivot — title is the whole bullet, body is empty.
      const title = text.length > 110 ? text.slice(0, 107) + '…' : text;
      return { title, body: text.length > 110 ? text.slice(107) : '' };
    }
    const title = text.slice(0, pivot).trim();
    const body  = text.slice(pivot + 1).trim().replace(/^[\s—:]+/, '');
    return { title, body };
  }
  function parseReflectionBullets(md: string): ReflectionBullet[] {
    if (!md) return [];
    // Take everything before the first "**Section Heading**" line so
    // we only get the opening narrative — the bold sections at the
    // end are evidence groupings (Shipped / Open Loops), already
    // shown by the alert cards and services table. A line is treated
    // as a heading when it starts with `**` and contains no list
    // marker (so an INLINE `**bold**` mid-bullet doesn't split).
    const lines0 = md.split('\n');
    const stopAt = lines0.findIndex(l => /^\s*\*\*[^*]+\*\*\s*$/.test(l));
    const usable = (stopAt >= 0 ? lines0.slice(0, stopAt) : lines0)
      .map(l => l.trim()).filter(Boolean);
    const out: ReflectionBullet[] = [];
    for (const ln of usable) {
      if (!/^[-*]\s+/.test(ln)) continue;
      // Strip markdown formatting FIRST so the chip-classifier, title-
      // splitter, and evidence-extractor all see clean prose. Without
      // this, `**operations-app**` leaks into the rendered title as
      // literal asterisks.
      const raw = ln.replace(/^[-*]\s+/, '').trim();
      // Capture a leading `**repo-name**` bold prefix as the repo tag
      // BEFORE we strip markdown — reflection bullets routinely start
      // with the service name bolded (e.g. `**klyne** — ...`).
      let repo: string | undefined;
      const repoMatch = raw.match(/^\*\*([a-z0-9][a-z0-9._-]{1,40})\*\*\s*[—:\-]?\s*/i);
      let body0 = raw;
      if (repoMatch) {
        repo = repoMatch[1].toLowerCase();
        body0 = raw.slice(repoMatch[0].length);
      }
      const clean = stripMarkdown(body0);
      if (clean.length < 20) continue; // skip noise
      const chip = classifyBullet(clean);
      const { body: cleaned, evidence } = extractEvidence(clean);
      const { title, body } = splitTitle(cleaned);
      out.push({ chip, title, body, evidence: Array.from(new Set(evidence)).slice(0, 2), repo });
    }
    return out.slice(0, 8); // cap at 8
  }
  // Flatten reflections across every service in the report so days
  // where multiple projects ran `/klyne:reflect` show all bullets,
  // not just the first-iterated one (BuildReport's first-wins
  // assignment to rep.reflection_markdown drops the rest). Stamps the
  // service repo onto bullets that don't already carry a `**repo**`
  // prefix, and de-dupes by title so a bullet that also appears in
  // the report-wide body isn't shown twice.
  function gatherProjectBullets(rep: ProductivityReport): ReflectionBullet[] {
    const seenTitle = new Set<string>();
    const out: ReflectionBullet[] = [];
    const push = (b: ReflectionBullet, repoFallback?: string) => {
      const key = (b.title || '').trim().toLowerCase();
      if (!key || seenTitle.has(key)) return;
      seenTitle.add(key);
      out.push({ ...b, repo: b.repo ?? repoFallback });
    };
    for (const svc of rep.services ?? []) {
      if (!svc.reflection_markdown) continue;
      for (const b of parseReflectionBullets(svc.reflection_markdown)) push(b, svc.repo);
    }
    // The report-wide body is also the first-iterated service's body,
    // so its bullets normally land via the loop above. We still
    // include it as a fallback for the (impossible-in-current-server)
    // case where it diverges.
    if (out.length === 0 && rep.reflection_markdown) {
      for (const b of parseReflectionBullets(rep.reflection_markdown)) push(b);
    }
    return out.slice(0, 12); // raise cap to 12 since we now span projects
  }
  function chipTone(chip: ReflectionBullet['chip']): 'ok'|'warn'|'alert'|'info'|'muted' {
    return chip === 'SHIPPED' ? 'ok'
         : chip === 'RISK'    ? 'warn'
         : chip === 'FLAG'    ? 'alert'
         : chip === 'MERGED'  ? 'info'
         : 'muted';
  }
  // ── reflection cards (iterative-reflection — phase 3) ────────────
  // Each card is one /klyne:reflect run for one project on this day.
  // docs/features/iterative-reflection.md: A1 — show every group
  // chronologically (no cross-time AI re-synthesis), B — variable
  // detail count per group, headline = first bullet's title.
  interface ReflectionCard {
    id: string;                    // unique per group, used as the disclosure key
    ts: number;                    // ms epoch — when this reflection was written
    repo: string;                  // service repo (for the small chip)
    headline: ReflectionBullet;    // first bullet — the at-a-glance summary
    details: ReflectionBullet[];   // remaining bullets — revealed by `view details`
  }
  function gatherReflectionCards(rep: ProductivityReport): ReflectionCard[] {
    const cards: ReflectionCard[] = [];
    for (const svc of rep.services ?? []) {
      for (const g of svc.reflection_groups ?? []) {
        const bullets = parseReflectionBullets(g.body_md).map(b => ({
          ...b,
          repo: b.repo ?? svc.repo,
        }));
        if (bullets.length === 0) continue;
        cards.push({
          id:       g.id,
          ts:       g.ts,
          repo:     svc.repo,
          headline: bullets[0],
          details:  bullets.slice(1),
        });
      }
    }
    cards.sort((a, b) => a.ts - b.ts);
    return cards;
  }
  function fmtTimeHM(ms: number): string {
    if (!ms) return '';
    const d = new Date(ms);
    return `${d.getHours().toString().padStart(2,'0')}:${d.getMinutes().toString().padStart(2,'0')}`;
  }
  // Disclosure state per card id — latest expanded by default, others
  // collapsed. Toggled by the `▸ N details` button.
  const expandedCards = $state<Record<string, boolean>>({});
  function toggleCard(id: string) {
    expandedCards[id] = !expandedCards[id];
  }
  function isExpanded(id: string, isLatest: boolean): boolean {
    if (id in expandedCards) return expandedCards[id];
    return isLatest;
  }
  function totalBranches(svcs: ProductivityService[]): number { let c=0; for (const s of svcs) c += s.branches.length; return c; }
  function totalCommits(svcs: ProductivityService[]): number { let c=0; for (const s of svcs) for (const b of s.branches) c += b.commits.length; return c; }
  function totalShippedPushed(svcs: ProductivityService[]): number { let c=0; for (const s of svcs) for (const b of s.branches) if (b.ship==='pushed-to-remote') c+=1; return c; }
  function totalShippedMerged(svcs: ProductivityService[]): number { let c=0; for (const s of svcs) for (const b of s.branches) if (b.ship==='merged-to-default') c+=1; return c; }
  function rawSumMinutes(sessions: ProductivitySessionStat[]): number { let n=0; for (const s of sessions) n += s.active_minutes; return n; }
  function peakSweep(sessions: ProductivitySessionStat[]): { peak: number; overlapMinutes: number } {
    // Sub-second tool-call bursts make "peak parallel" inflate
    // wildly when you have hundreds of tiny sessions. Cap an
    // interval's minimum length to 30s for the sweep so a
    // millisecond-long Bash subagent doesn't count as a "parallel
    // session" alongside a real 2-hour interactive turn.
    const MIN_MS = 30_000;
    const pts: { t: number; d: number }[] = [];
    for (const s of sessions ?? []) for (const iv of s.active_intervals ?? []) {
      const a = Date.parse(iv.start), b = Date.parse(iv.end);
      if (!Number.isNaN(a) && !Number.isNaN(b) && (b - a) >= MIN_MS) {
        pts.push({ t: a, d: +1 }); pts.push({ t: b, d: -1 });
      }
    }
    pts.sort((x,y) => x.t - y.t);
    let cur=0, peak=0, overlap=0, lastT=0;
    for (const p of pts) { if (cur >= 2) overlap += p.t - lastT; cur += p.d; if (cur > peak) peak = cur; lastT = p.t; }
    return { peak, overlapMinutes: Math.round(overlap/60000) };
  }
  function risksHistogram(svcs: ProductivityService[]) {
    let uncommitted=0, unpushedSignal=0, drifted=0, unpushedNoise=0;
    for (const s of svcs ?? []) for (const r of s.risks ?? []) {
      if (r.kind === 'done-uncommitted') uncommitted++;
      else if (r.kind === 'unpushed') {
        // crude signal/noise split — branches authored by user with subjects ≥ 1 word are "signal"
        if ((r.commits ?? []).length > 0) unpushedSignal++; else unpushedNoise++;
      }
      else if (r.kind.includes('drift')) drifted++;
    }
    return { uncommitted, unpushedSignal, drifted, unpushedNoise };
  }
  // Fallback bullets when reflection_markdown is empty. Builds a
  // session-level evidence list so the page is never blank.
  function fallbackSessionBullets(rep: ProductivityReport): ReflectionBullet[] {
    return (rep.sessions ?? [])
      .filter(s => s.active_minutes >= 5)
      .sort((a,b) => b.active_minutes - a.active_minutes)
      .slice(0, 6)
      .map(s => ({
        chip: 'SHIPPED' as const,
        title: `${s.repo} — ${s.cli} session`,
        body:  `${fmtMinutes(s.active_minutes)} of active time across ${s.message_count} messages.`,
        evidence: [shortId(s.session_id, 8)],
        repo:  s.repo,
      }));
  }
  // Risk kind → human kicker. Centralised so the masthead pill and the
  // triage card use the same wording (and so the masthead can't mislabel
  // a done-uncommitted alert as "credential flag" the way it used to).
  const ALERT_KICKER: Record<string, string> = {
    'done-uncommitted': 'Uncommitted work',
    'unpushed':         'Unpushed commits',
  };
  function topAlerts(svcs: ProductivityService[]): { level:'alert'|'warn'; rank:string; kicker:string; title:string; body:string; meta:string }[] {
    const items: { level:'alert'|'warn'; weight:number; kicker:string; title:string; body:string; meta:string }[] = [];
    for (const svc of svcs ?? []) for (const r of svc.risks ?? []) {
      if (r.kind === 'done-uncommitted') {
        items.push({
          level: 'alert', weight: 90 + r.age_minutes/60,
          kicker: ALERT_KICKER['done-uncommitted'],
          title:  r.detail || `Uncommitted edits in ${svc.repo}`,
          body:   r.files?.length ? `${r.files.length} file${r.files.length===1?'':'s'}: ${r.files.slice(0,3).join(', ')}` : `branch ${r.branch || 'main'}`,
          meta:   `${svc.repo} · ${r.branch || 'main'} · ${fmtMinutes(r.age_minutes)} old`,
        });
      } else if (r.kind === 'unpushed') {
        items.push({
          level: 'warn', weight: 50 + (r.commits?.length ?? 0)*5,
          kicker: ALERT_KICKER['unpushed'],
          title:  r.detail || `${r.commits?.length ?? 0} commit(s) ahead, not on origin`,
          body:   r.commits?.length ? r.commits.slice(0,3).map(c => `${shortId(c.sha,7)} · ${c.subject}`).join('  ·  ') : `branch ${r.branch}`,
          meta:   `${svc.repo} · ${r.branch || 'main'}`,
        });
      }
    }
    items.sort((a,b) => b.weight - a.weight);
    return items.slice(0,2).map((it,i) => ({ ...it, rank: (i+1).toString().padStart(2,'0') }));
  }
  // pluralise — masthead pill copy ("1 alert" vs "2 alerts").
  function plural(n: number, s: string, p?: string): string {
    return `${n} ${n === 1 ? s : (p ?? s + 's')}`;
  }
  // Reflection status enum → display label. The handler emits
  // 'missing' | 'stale' | 'current'; anything else (including ''/null
  // for a single-day report that never set it) renders as a generic
  // "no data" label rather than echoing the raw string verbatim.
  const REFLECTION_LABEL: Record<string, string> = {
    current: 'reflection current',
    stale:   'reflection stale',
    missing: 'no reflection',
  };
  function reflectionLabel(status: string | undefined): string {
    return REFLECTION_LABEL[String(status || '')] ?? 'no reflection';
  }

  async function copyForStandup() {
    if (!visible) return;
    const v = visible; const dp = dayParts(v.day);
    const peak = peakSweep(v.sessions);
    const lines = [
      `# Standup · ${dp.weekday} ${dp.pretty}`,
      `${fmtMinutes(v.total_active_minutes)} focus · ${totalCommits(v.services)} commits · ${totalShippedPushed(v.services)+totalShippedMerged(v.services)} shipped · peak ${peak.peak}× parallel`,
      ``, `## What was done`,
    ];
    const parsed = gatherProjectBullets(v);
    const standupBullets = parsed.length > 0 ? parsed : fallbackSessionBullets(v);
    for (const b of standupBullets) lines.push(`- [${b.chip}]${b.repo ? ' **'+b.repo+'**' : ''} ${b.title}${b.body ? ' — ' + b.body : ''}`);
    const open = topAlerts(v.services);
    if (open.length > 0) {
      lines.push(``, `## Open loops`);
      for (const a of open) lines.push(`- ${a.kicker}: ${a.title} (${a.meta})`);
    }
    try { await navigator.clipboard.writeText(lines.join('\n')); copied = true; setTimeout(() => copied = false, 1800); } catch {}
  }
</script>

<svelte:head><title>klyne — Productivity</title></svelte:head>

<div class="page">
  <!-- ── 1. Top rail ───────────────────────────────────────────── -->
  <div class="rail">
    <div class="rail-brand">
      <span class="rail-logo">KLYNE/</span>
      <span class="rail-app">productivity</span>
    </div>
    <div class="seg">
      {#each RANGE_KEYS as k (k)}
        <button class="seg-btn" class:on={rangeKey === k} onclick={() => pickRange(k)}>{RANGE_LABELS[k]}</button>
      {/each}
      <div class="cal-wrap">
        <button
          class="seg-btn cal-trigger"
          class:on={rangeKey === 'custom' || calendarOpen}
          onclick={() => { calendarOpen = !calendarOpen; if (calendarOpen && availableDays.length === 0) void loadAvailableDays(); }}
          title="Pick a specific day"
          aria-haspopup="dialog"
          aria-expanded={calendarOpen}
        >
          calendar
        </button>
        {#if calendarOpen}
          <div class="cal-pop" role="dialog" aria-label="Pick productivity date">
            <div class="cal-head">
              <button class="cal-nav" onclick={() => shiftCalendarMonth(-1)} disabled={!canGoPrevMonth} aria-label="Previous month">‹</button>
              <span>{monthLabel(calendarMonth)}</span>
              <button class="cal-nav" onclick={() => shiftCalendarMonth(1)} disabled={!canGoNextMonth} aria-label="Next month">›</button>
            </div>
            <div class="cal-week" aria-hidden="true">
              {#each ['S','M','T','W','T','F','S'] as d}
                <span>{d}</span>
              {/each}
            </div>
            <div class="cal-grid">
              {#each calendarCells as cell (cell.key)}
                <button
                  class="cal-day"
                  class:calDayMuted={!cell.inMonth}
                  class:calDayData={cell.hasData}
                  class:selected={cell.selected}
                  disabled={!cell.hasData}
                  onclick={() => selectCalendarDay(cell.day)}
                  aria-label={cell.hasData ? `Show productivity for ${cell.day}` : `No productivity data for ${cell.day}`}
                >
                  {cell.label}
                </button>
              {/each}
            </div>
            <div class="cal-foot">
              {#if datesError}
                <span class="alert">dates unavailable</span>
              {:else if availableDays.length > 0}
                <span>{availableDays[0]} → {availableDays[availableDays.length - 1]}</span>
              {:else}
                <span>no dated data yet</span>
              {/if}
            </div>
          </div>
        {/if}
      </div>
    </div>
    <div class="rail-right">
      <span class="rail-ago">{ago(loadedAt)}</span>
      <button class="btn-ghost" onclick={refresh} title="Refresh">↻</button>
    </div>
  </div>

  <!-- Banners (independent of the body render). Show the small range-
       switching status when we already have data, the full loader when
       we don't, and the error banner inline above the body when an
       error fires with stale data already on screen. -->
  {#if loading && rep}
    <div class="range-loading mono" role="status" aria-live="polite">
      <span class="range-spinner"></span>
      <span>Fetching {rangeLabel(rangeKey)}…</span>
      <button class="btn-ghost-l" onclick={() => { inflight?.abort(); }}>Cancel</button>
    </div>
  {/if}
  {#if error && rep}
    <div class="state state-err state-soft" role="alert">
      <span class="mono">Error: {error}</span>
      <button class="btn-ghost-l" onclick={refresh}>Retry</button>
    </div>
  {/if}

  {#if loading && !rep}
    <div class="state state-loading">
      <span class="mono">Reading sessions from disk…</span>
      <span class="mono dim">This can take a few seconds when the window contains many sessions.</span>
      <div class="state-actions">
        <button class="btn-ghost-l" onclick={() => { inflight?.abort(); }}>Cancel</button>
        <button class="btn-ghost-l" onclick={refresh}>Retry</button>
      </div>
    </div>
  {:else if error && !rep}
    <div class="state state-err">
      <span class="mono">Error: {error}</span>
      <div class="state-actions">
        <button class="btn-ghost-l" onclick={refresh}>Retry</button>
      </div>
    </div>
  {:else if visible}
    {@const v = visible}
    {@const dp = dayParts(v.day)}
    {@const realSessions = meaningfulSessions(v, 1)}
    {@const peak = peakSweep(realSessions)}
    {@const hist = risksHistogram(v.services)}
    {@const reflectionCards = gatherReflectionCards(v)}
    {@const parsedBullets = gatherProjectBullets(v)}
    {@const bullets = parsedBullets.length > 0 ? parsedBullets : fallbackSessionBullets(v)}
    {@const alerts = topAlerts(v.services)}
    {@const totalRisks = hist.uncommitted + hist.unpushedSignal + hist.drifted + hist.unpushedNoise + alerts.length}
    {@const claudeM = v.minutes_by_cli?.claude ?? 0}
    {@const codexM  = v.minutes_by_cli?.codex ?? 0}
    {@const branches = totalBranches(v.services)}
    {@const commits  = totalCommits(v.services)}
    {@const pushed   = totalShippedPushed(v.services)}
    {@const merged   = totalShippedMerged(v.services)}

    <div class="body">
      <!-- ── 2. Masthead ─────────────────────────────────────────── -->
      <header class="mast">
        <div class="mast-l">
          <span class="kick">{dp.weekday} · Standup digest</span>
          <h1 class="date">{dp.pretty}</h1>
        </div>
        <div class="mast-m">
          <div class="mast-pills">
            <span class="pill pill-{v.reflection_status === 'current' ? 'ok' : v.reflection_status === 'stale' ? 'warn' : 'alert'}">
              <span class="dot dot-{v.reflection_status === 'current' ? 'ok' : v.reflection_status === 'stale' ? 'warn' : 'alert'}"></span>{reflectionLabel(v.reflection_status)}
            </span>
            {#if v.reflection_status !== 'current'}
              <button
                class="pill pill-action"
                onclick={runReflectForAllProjects}
                disabled={reflectRunning}
                title="Spawn /klyne:reflect for every project in this window">
                {#if reflectRunning}
                  Running {reflectDone}/{reflectTotal}…
                {:else}
                  ▶ Run /klyne:reflect now
                {/if}
              </button>
            {/if}
            {#if alerts.length > 0}
              <span class="pill pill-alert"><span class="dot dot-alert"></span>{plural(alerts.length, 'open alert')} · review before EOD</span>
            {/if}
          </div>
          <span class="mast-sub">
            {realSessions.length} worklog entries pending · next /klyne:reflect when window resets
            {#if reflectError}
              · <span class="refl-err">last run reported: {reflectError}</span>
            {/if}
          </span>
        </div>
        <div class="mast-r">
          <button class="btn-primary" onclick={copyForStandup}>{copied ? '✓ Copied' : 'Copy for standup ⌘C'}</button>
          <span class="mast-sub">{bullets.length} bullets · {bullets.reduce((n,b)=>n+b.title.length+b.body.length, 0)} chars</span>
        </div>
      </header>

      <!-- ── 3. Triage ───────────────────────────────────────────── -->
      <section class="triage">
        {#each alerts as a (a.rank)}
          <article class="tc" class:tc-alert={a.level==='alert'} class:tc-warn={a.level==='warn'}>
            <span class="tc-bar"></span>
            <div class="tc-head">
              <div class="tc-head-l">
                <span class="mono dim">{a.rank}</span>
                <span class="kick" class:kick-alert={a.level==='alert'} class:kick-warn={a.level==='warn'}>{a.kicker}</span>
              </div>
              <span class="mono dim">{a.meta}</span>
            </div>
            <h3 class="tc-title">{a.title}</h3>
            <p class="tc-body">{a.body}</p>
          </article>
        {/each}
        {#if alerts.length === 0}
          <article class="tc tc-empty">
            <span class="tc-bar"></span>
            <div class="tc-head"><span class="kick kick-ok">All clear</span></div>
            <h3 class="tc-title">No open alerts for this window.</h3>
            <p class="tc-body">No uncommitted edits, no unpushed signal, no migrations stalled.</p>
          </article>
        {/if}
        <!-- Pad to 2 cards so the grid is stable -->
        {#if alerts.length === 1}<div></div>{/if}

        <aside class="risks">
          <div class="risks-head">
            <span class="kick">All risks</span>
            <span class="mono dim">{hist.uncommitted + hist.unpushedSignal} actionable</span>
          </div>
          <div class="risks-num-row">
            <span class="num-xl alert">{totalRisks || 0}</span>
            <span class="mono dim">open · {hist.unpushedNoise} noise · {hist.drifted} drifted</span>
          </div>
          <hr class="hr" />
          <ul class="risks-list">
            {#each [
              { level:'alert', count: alerts.filter(a=>a.level==='alert').length, label:'Credential exposure' },
              { level:'alert', count: alerts.filter(a=>a.level==='warn').length,  label:'Stalled migration' },
              { level:'warn',  count: hist.uncommitted,       label:'Uncommitted edits' },
              { level:'warn',  count: hist.unpushedSignal,    label:'Unpushed · signal' },
              { level:'info',  count: hist.drifted,           label:'Drifted from main' },
              { level:'muted', count: hist.unpushedNoise,     label:'Ephemeral worktrees' },
            ] as row (row.label)}
              <li>
                <span class="num-sm" class:alert={row.level==='alert'} class:warn={row.level==='warn'} class:info={row.level==='info'} class:dim={row.level==='muted'}>{row.count}</span>
                <span class="mono soft">{row.label}</span>
                <span class="risks-dot" class:alert={row.level==='alert'} class:warn={row.level==='warn'} class:info={row.level==='info'}></span>
              </li>
            {/each}
          </ul>
        </aside>
      </section>

      <!-- ── 4. Hero — what was done ─────────────────────────────── -->
      <!--
        Iterative reflection (docs/features/iterative-reflection.md):
        one card per /klyne:reflect run, oldest at top. Each card shows
        the headline (first bullet) by default; the rest expand behind
        a "view details" disclosure. Falls back to the flat bullet list
        when there are no reflection groups at all.
      -->
      <section class="card hero">
        <div class="hero-head">
          <span class="kick kick-accent">What was done</span>
          <span class="sep">·</span>
          {#if reflectionCards.length > 0}
            <span class="mono muted">{reflectionCards.length} reflection{reflectionCards.length === 1 ? '' : 's'} today · {realSessions.length} session{realSessions.length === 1 ? '' : 's'}</span>
          {:else}
            <span class="mono muted">{bullets.length} important sessions · {realSessions.length} total · importance ≥ 7</span>
          {/if}
          <span class="grow"></span>
          <span class="mono dim">deterministic · git + jsonl + sqlite</span>
        </div>
        {#if reflectionCards.length === 0 && bullets.length === 0}
          <h2 class="hero-h hero-empty">No important sessions in this window. Pick a wider range or check back after end of day.</h2>
        {/if}

        {#if reflectionCards.length > 0}
          <ol class="refl-cards">
            {#each reflectionCards as c, ci (c.id)}
              {@const isLast = ci === reflectionCards.length - 1}
              {@const expanded = isExpanded(c.id, isLast)}
              {@const headTone = chipTone(c.headline.chip)}
              <li class="refl-card">
                <header class="refl-head">
                  <span class="mono dim refl-num">T{ci + 1}</span>
                  <span class="mono refl-ts">{fmtTimeHM(c.ts)}</span>
                  <span class="mono dim refl-sep">·</span>
                  <span class="mono refl-repo">{c.repo}</span>
                  <span class="grow"></span>
                  <span class="pill pill-{headTone} refl-chip">{c.headline.chip}</span>
                </header>
                <div class="refl-body">
                  <div class="refl-title">{c.headline.title}</div>
                  {#if c.headline.body}<p class="refl-desc">{c.headline.body}</p>{/if}
                </div>
                {#if c.details.length > 0}
                  <button
                    class="refl-disclose"
                    onclick={() => toggleCard(c.id)}
                    aria-expanded={expanded}
                  >
                    <span class="refl-caret" class:open={expanded}>▸</span>
                    <span class="mono">{expanded ? 'hide' : 'view'} {c.details.length} detail{c.details.length === 1 ? '' : 's'}</span>
                  </button>
                  {#if expanded}
                    <ol class="refl-details">
                      {#each c.details as d, di (c.id + '|' + di)}
                        {@const dTone = chipTone(d.chip)}
                        <li class="refl-detail">
                          <span class="pill pill-{dTone} refl-detail-chip">{d.chip}</span>
                          <div class="refl-detail-body">
                            <div class="refl-detail-title">{d.title}</div>
                            {#if d.body}<p class="refl-detail-desc">{d.body}</p>{/if}
                          </div>
                          <div class="refl-detail-ev">
                            {#each d.evidence as e}<span class="mono dim">{e}</span>{/each}
                          </div>
                        </li>
                      {/each}
                    </ol>
                  {/if}
                {/if}
              </li>
            {/each}
          </ol>
        {:else if bullets.length > 0}
          <ol class="bul-list">
            {#each bullets as b, i (b.title + i)}
              {@const tone = chipTone(b.chip)}
              <li class="bul">
                <span class="mono dim bul-num">0{i+1}</span>
                <span class="pill pill-{tone} bul-pill">{b.chip}</span>
                <div class="bul-body">
                  <div class="bul-title">{b.title}</div>
                  {#if b.body}<p class="bul-desc">{b.body}</p>{/if}
                </div>
                <div class="bul-ev">
                  {#if b.repo}<span class="mono bul-repo">{b.repo}</span>{/if}
                  {#each b.evidence as e}<span class="mono dim">{e}</span>{/each}
                </div>
              </li>
            {/each}
          </ol>
        {/if}
      </section>

      <!-- ── 5. Metric strip ─────────────────────────────────────── -->
      <section class="card metrics">
        {#each [
          { k:'Total AI time',   v: fmtMinutes(v.total_active_minutes), sub:`wall · ${realSessions.length} sessions merged` },
          { k:'Claude / Codex',  v: fmtMinutes(claudeM),               sub:`Codex ${fmtMinutes(codexM)}` },
          { k:'Active services', v: String(v.services.length),         sub:`${v.services.filter(s=>!s.manual_only).length} with AI time` },
          { k:'Branches',        v: String(branches),                  sub:`${commits} commits` },
          { k:'Shipped',         v: String(pushed),                    sub:`${merged} merged · ${pushed} pushed` },
          { k:'Peak parallel',   v: `${peak.peak}×`,                   sub:`${fmtMinutes(peak.overlapMinutes)} overlap` },
        ] as m, i (m.k)}
          <div class="metric" class:no-bar={i===5}>
            <span class="kick metric-k">{m.k}</span>
            <span class="num-l">{m.v}</span>
            <span class="mono dim">{m.sub}</span>
          </div>
        {/each}
      </section>

      <!-- ── 6. Services table ───────────────────────────────────── -->
      <section class="card services">
        <header class="svc-head">
          <div>
            <span class="kick">Services</span>
            <span class="mono muted svc-head-sub">· {v.services.length} active</span>
          </div>
          {#if hiddenIds.size > 0}
            <button class="btn-ghost-l" onclick={showAllHidden} title="Restore all hidden sessions">
              {hiddenIds.size} hidden · show all
            </button>
          {/if}
        </header>
        <div class="svc-row svc-row-th">
          <span class="kick">Service</span>
          <span class="kick num">AI time</span>
          <span class="kick num">Commits</span>
          <span class="kick num">Shipped</span>
          <span class="kick">Open loops</span>
          <span class="kick num"></span>
        </div>
        {#each v.services as svc, si (svc.repo + si)}
          {@const svcMin = (svc.minutes_by_cli?.claude ?? 0) + (svc.minutes_by_cli?.codex ?? 0)}
          {@const svcCommits = svc.branches.reduce((a,b)=>a+b.commits.length, 0)}
          {@const svcShipped = svc.branches.filter(b=>b.ship==='pushed-to-remote'||b.ship==='merged-to-default').length}
          {@const tickets = svc.branches.map(b => b.ticket_id).filter(Boolean).slice(0, 3)}
          {@const loops = [
            ...svc.branches.filter(b => b.ahead > 0 || b.behind > 0).map(b => ({ kind:'unpushed', label: b.name, note: `${b.ahead} ahead${b.behind>0?`, ${b.behind} behind`:''}` })),
            ...svc.risks.filter(r => r.kind === 'done-uncommitted').map(r => ({ kind:'uncommitted', label: r.branch || 'main', note: r.detail || `${r.files?.length ?? 0} files` })),
          ]}
          <div class="svc-row">
            <div class="svc-name">
              <span class="mono">{svc.repo}</span>
              {#each tickets as t}<span class="pill pill-mini">{t}</span>{/each}
            </div>
            <span class="num-m num">{fmtMinutes(svcMin)}</span>
            <span class="num-m num" class:dim={!svcCommits}>{svcCommits || '—'}</span>
            <div class="num">
              {#if svcShipped > 0}<span class="pill pill-ok"><span class="dot dot-ok"></span>{svcShipped} pushed</span>{:else}<span class="mono dim">—</span>{/if}
            </div>
            <div class="svc-loops">
              {#if loops.length === 0}
                <span class="mono dim">clean · exploration only</span>
              {/if}
              {#each loops.slice(0,3) as l, li (l.kind + '|' + l.label + '|' + li)}
                <div class="loop">
                  <span class="mono loop-prefix" class:warn={l.kind==='uncommitted'} class:info={l.kind==='unpushed'}>{l.kind==='uncommitted'?'M':'↑'}</span>
                  <span class="mono loop-label">{l.label}</span>
                  <span class="mono dim loop-note">{l.note}</span>
                </div>
              {/each}
              {#if loops.length > 3}<span class="mono muted">+{loops.length-3} more</span>{/if}
            </div>
            <button class="svc-hide" onclick={() => hideService(svc.repo)} title="Hide all sessions from {svc.repo}" aria-label="Hide {svc.repo}">
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                <path d="M3 3l18 18" />
                <path d="M10.585 10.585a2 2 0 0 0 2.83 2.83" />
                <path d="M16.681 16.673A8.717 8.717 0 0 1 12 18c-4 0-7.333-2-10-6 1.166-1.75 2.5-3.146 4-4.188" />
                <path d="M9.882 5.205A9.336 9.336 0 0 1 12 5c4 0 7.333 2 10 6-.706 1.06-1.49 1.99-2.349 2.79" />
              </svg>
            </button>
          </div>
        {/each}
        {#if v.services.length === 0}
          <div class="svc-empty">No services in this window.</div>
        {/if}
      </section>

      <!-- ── 7. Concurrency / timeline ───────────────────────────── -->
      <section class="card timeline">
        <div class="tl-side">
          <span class="kick">Concurrency</span>
          {#each [
            { k:'Wall-clock', v: fmtMinutes(v.total_active_minutes), sub: 'elapsed' },
            { k:'Raw sum',    v: fmtMinutes(rawSumMinutes(v.sessions)), sub: 'all sessions' },
            { k:'Sessions',   v: String(realSessions.length), sub: `${bullets.length} important` },
            { k:'Peak',       v: `${peak.peak}×`, sub: fmtMinutes(peak.overlapMinutes) + ' overlap' },
          ] as s (s.k)}
            <div class="tl-stat">
              <span class="mono muted">{s.k}</span>
              <span>
                <span class="num-m">{s.v}</span>
                <span class="mono dim tl-stat-sub">{s.sub}</span>
              </span>
            </div>
          {/each}
        </div>
        <div class="tl-main">
          <header class="tl-h">
            <span class="mono soft">parallel session timeline · {realSessions.length} sessions</span>
          </header>
          <ConcurrencyTimeline sessions={v.sessions} since={since} until={until} />
        </div>
      </section>
    </div>
  {/if}
</div>

<style>
  /* ─── design tokens (V4 Synthesis) — scoped to this page so the
     rest of the app keeps its existing palette ───────────────── */
  .page {
    --bg:           oklch(0.155 0.005 80);
    --bg-card:      oklch(0.195 0.005 80);
    --bg-card-2:    oklch(0.225 0.005 80);
    --bg-inset:    oklch(0.13  0.005 80);
    --fg:           oklch(0.975 0.005 80);
    --fg-soft:      oklch(0.82  0.005 80);
    --fg-muted:     oklch(0.62  0.005 80);
    --fg-dim:       oklch(0.46  0.005 80);
    --border:       oklch(0.30  0.005 80);
    --border-soft:  oklch(0.24  0.005 80);
    --border-hair:  oklch(0.21  0.005 80);
    --ok:    oklch(0.74 0.13 150);
    --warn:  oklch(0.80 0.13 75);
    --alert: oklch(0.70 0.16 27);
    --info:  oklch(0.75 0.13 230);
    --accent:oklch(0.78 0.13 80);
    --font-sans: 'Geist', 'Inter', system-ui, -apple-system, sans-serif;
    --font-mono: 'JetBrains Mono', ui-monospace, 'SF Mono', Menlo, monospace;

    background: var(--bg); color: var(--fg);
    font-family: var(--font-sans); font-size: 13px; line-height: 1.45;
    min-height: 100vh; min-width: 0;
  }
  :global(body) { background: oklch(0.155 0.005 80); }
  .page :global(*) { box-sizing: border-box; }

  .state { color: var(--fg-muted); padding: 24px; font-family: var(--font-mono); font-size: 12px; }
  .state-err { color: var(--alert); }
  .state-loading { display: flex; flex-direction: column; gap: 8px; align-items: flex-start; padding: 48px 28px; }
  .state-loading .mono { font-size: 12px; }
  .state-err { display: flex; flex-direction: column; gap: 8px; padding: 48px 28px; color: var(--alert); }
  /* Soft variant: render in a slim banner inline above the still-visible
     body so an error during a range refetch doesn't blank the page. */
  .state-err.state-soft {
    flex-direction: row;
    align-items: center;
    padding: 10px 16px;
    margin: 8px 20px 0;
    background: color-mix(in oklch, var(--alert) 8%, var(--bg-card));
    border: 1px solid color-mix(in oklch, var(--alert) 30%, transparent);
    border-radius: 8px;
    gap: 12px;
  }
  /* Slim "Fetching <range>…" banner shown while a range-switch fetch is
     in flight and we still have last range's data on screen. */
  .range-loading {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 8px 14px;
    margin: 8px 20px 0;
    background: color-mix(in oklch, var(--accent) 10%, var(--bg-card));
    border: 1px solid color-mix(in oklch, var(--accent) 30%, transparent);
    border-radius: 8px;
    font-size: 12px;
    color: var(--fg-soft);
  }
  .range-spinner {
    width: 12px; height: 12px;
    border-radius: 50%;
    border: 2px solid color-mix(in oklch, var(--accent) 40%, transparent);
    border-top-color: var(--accent);
    animation: range-spin 0.7s linear infinite;
  }
  @keyframes range-spin {
    to { transform: rotate(360deg); }
  }
  .state-actions { display: flex; gap: 8px; padding-top: 8px; }
  .btn-ghost-l { background: transparent; color: var(--fg); border: 1px solid var(--border); padding: 6px 14px; border-radius: 6px; cursor: pointer; font-family: var(--font-mono); font-size: 11px; }
  .btn-ghost-l:hover { background: var(--bg-card); }

  .mono { font-family: var(--font-mono); font-variant-numeric: tabular-nums; letter-spacing: -0.01em; }
  .soft { color: var(--fg-soft); }
  .muted { color: var(--fg-muted); }
  .dim   { color: var(--fg-dim); }
  .alert { color: var(--alert); }
  .warn  { color: var(--warn); }
  .info  { color: var(--info); }
  .grow  { flex: 1; }
  .num   { text-align: right; }
  .sep   { color: var(--border); }

  .kick { font-family: var(--font-mono); font-size: 11px; letter-spacing: 0.12em; text-transform: uppercase; color: var(--fg-muted); }
  .kick-accent { color: var(--accent); }
  .kick-alert  { color: var(--alert); }
  .kick-warn   { color: var(--warn); }
  .kick-ok     { color: var(--ok); }

  .card  { background: var(--bg-card); border: 1px solid var(--border-soft); border-radius: 10px; }
  .hr    { border: 0; border-top: 1px solid var(--border-hair); margin: 0; }

  /* number readouts */
  .num-l  { font-family: var(--font-mono); font-variant-numeric: tabular-nums; letter-spacing: -0.02em; line-height: 1; font-size: 24px; color: var(--fg); }
  .num-m  { font-family: var(--font-mono); font-variant-numeric: tabular-nums; letter-spacing: -0.02em; font-size: 15px; color: var(--fg); }
  .num-xl { font-family: var(--font-mono); font-variant-numeric: tabular-nums; letter-spacing: -0.02em; line-height: 1; font-size: 36px; }
  .num-sm { font-family: var(--font-mono); font-variant-numeric: tabular-nums; font-size: 13px; text-align: right; color: var(--fg); }
  .num-sm.alert { color: var(--alert); }
  .num-sm.warn  { color: var(--warn); }
  .num-sm.info  { color: var(--info); }
  .num-sm.dim   { color: var(--fg-dim); }

  /* pills */
  .pill { display: inline-flex; align-items: center; gap: 6px; padding: 3px 8px; border-radius: 999px; font-family: var(--font-mono); font-size: 11px; line-height: 1.4; border: 1px solid var(--border); color: var(--fg-soft); background: var(--bg-card-2); white-space: nowrap; }
  .pill-mini { padding: 2px 6px; font-size: 10px; color: var(--fg-soft); }
  .pill-ok    { color: var(--ok);    border-color: color-mix(in oklch, var(--ok)    35%, transparent); background: color-mix(in oklch, var(--ok)    10%, var(--bg-card-2)); }
  .pill-warn  { color: var(--warn);  border-color: color-mix(in oklch, var(--warn)  35%, transparent); background: color-mix(in oklch, var(--warn)  10%, var(--bg-card-2)); }
  .pill-alert { color: var(--alert); border-color: color-mix(in oklch, var(--alert) 35%, transparent); background: color-mix(in oklch, var(--alert) 10%, var(--bg-card-2)); }
  .pill-info  { color: var(--info);  border-color: color-mix(in oklch, var(--info)  35%, transparent); background: color-mix(in oklch, var(--info)  10%, var(--bg-card-2)); }
  /* The reflect-now trigger looks like a pill but is interactive. Borrow
     the accent palette so it reads as the primary call-to-action when
     reflection_status is missing/stale. Disabled state dims and removes
     the hover lift while a /klyne:reflect batch is in flight. */
  .pill-action {
    color: var(--accent, var(--ok));
    border-color: color-mix(in oklch, var(--accent, var(--ok)) 45%, transparent);
    background: color-mix(in oklch, var(--accent, var(--ok)) 12%, var(--bg-card-2));
    cursor: pointer;
    font-family: var(--font-mono);
    font-size: 11px;
  }
  .pill-action:hover:not(:disabled) {
    background: color-mix(in oklch, var(--accent, var(--ok)) 22%, var(--bg-card-2));
  }
  .pill-action:disabled { cursor: progress; opacity: 0.7; }
  .refl-err { color: var(--alert); }

  .dot { width: 7px; height: 7px; border-radius: 50%; background: var(--fg-muted); flex-shrink: 0; }
  .dot-ok    { background: var(--ok); }
  .dot-warn  { background: var(--warn); }
  .dot-alert { background: var(--alert); }

  .btn       { display: inline-flex; align-items: center; gap: 6px; padding: 6px 10px; border-radius: 8px; border: 1px solid var(--border); background: var(--bg-card-2); color: var(--fg-soft); font-family: var(--font-mono); font-size: 11px; cursor: pointer; }
  .btn:hover { background: var(--bg-card); color: var(--fg); }
  .btn-fill  { background: var(--bg-card-2); color: var(--fg); }
  .btn-ghost { display: inline-flex; align-items: center; justify-content: center; width: 28px; height: 28px; border-radius: 6px; border: 1px solid var(--border); background: transparent; color: var(--fg-soft); font-family: var(--font-mono); font-size: 14px; cursor: pointer; }
  .btn-ghost:hover { background: var(--bg-card); color: var(--fg); }
  .btn-primary { background: var(--fg); color: var(--bg); border: 1px solid var(--fg); padding: 8px 14px; border-radius: 8px; font-family: var(--font-mono); font-size: 11px; cursor: pointer; }
  .btn-primary:hover { opacity: 0.92; }

  /* ── 1. Top rail ─────────────────────────────────────────── */
  .rail {
    position: relative; z-index: 5; overflow: visible;
    display: grid; grid-template-columns: 1fr auto 1fr;
    align-items: center; padding: 10px 28px;
    border-bottom: 1px solid var(--border-hair);
    background: var(--bg-inset);
  }
  .rail-brand { display: inline-flex; align-items: baseline; gap: 10px; }
  .rail-logo { font-family: var(--font-mono); font-size: 11px; letter-spacing: 0.18em; color: var(--accent); }
  .rail-app  { font-family: var(--font-mono); font-size: 11.5px; color: var(--fg-soft); }
  .seg {
    display: inline-flex; gap: 1px; padding: 2px;
    background: var(--bg-card); border: 1px solid var(--border-hair); border-radius: 6px;
    justify-self: center;
  }
  .seg-btn {
    padding: 4px 10px; border-radius: 4px;
    background: transparent; border: none; color: var(--fg-muted);
    font-family: var(--font-mono); font-size: 11px; cursor: pointer;
  }
  .seg-btn.on { background: var(--fg); color: var(--bg); }
  .cal-wrap { position: relative; display: inline-flex; }
  .cal-trigger { min-width: 74px; }
  .cal-pop {
    position: absolute;
    top: calc(100% + 8px);
    right: 0;
    z-index: 20;
    width: 252px;
    padding: 10px;
    background: var(--bg-card);
    border: 1px solid var(--border-soft);
    border-radius: 8px;
    box-shadow: 0 18px 50px color-mix(in oklch, black 42%, transparent);
  }
  .cal-head {
    display: grid;
    grid-template-columns: 28px 1fr 28px;
    align-items: center;
    gap: 8px;
    margin-bottom: 8px;
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--fg-soft);
    text-align: center;
  }
  .cal-nav {
    width: 28px; height: 26px;
    border-radius: 6px;
    border: 1px solid var(--border-hair);
    background: var(--bg-inset);
    color: var(--fg-soft);
    cursor: pointer;
    font-size: 16px;
    line-height: 1;
  }
  .cal-nav:disabled { opacity: 0.35; cursor: default; }
  .cal-week, .cal-grid { display: grid; grid-template-columns: repeat(7, 1fr); gap: 4px; }
  .cal-week { margin-bottom: 4px; color: var(--fg-dim); font-family: var(--font-mono); font-size: 10px; text-align: center; }
  .cal-day {
    height: 28px;
    border-radius: 6px;
    border: 1px solid transparent;
    background: transparent;
    color: var(--fg-dim);
    font-family: var(--font-mono);
    font-size: 11px;
    cursor: default;
  }
  .cal-day.calDayData {
    cursor: pointer;
    color: var(--fg-soft);
    background: var(--bg-inset);
    border-color: var(--border-hair);
  }
  .cal-day.calDayData:hover { color: var(--fg); border-color: var(--border); }
  .cal-day.selected {
    color: var(--bg);
    background: var(--fg);
    border-color: var(--fg);
  }
  .cal-day.calDayMuted { opacity: 0.38; }
  .cal-day:disabled { opacity: 0.22; }
  .cal-foot {
    margin-top: 9px;
    padding-top: 8px;
    border-top: 1px solid var(--border-hair);
    color: var(--fg-dim);
    font-family: var(--font-mono);
    font-size: 10px;
    text-align: center;
  }
  .rail-right { display: inline-flex; align-items: center; gap: 10px; justify-self: end; }
  .rail-ago { font-family: var(--font-mono); font-size: 10.5px; color: var(--fg-dim); }

  /* ── body wrapper ────────────────────────────────────────── */
  .body { display: flex; flex-direction: column; gap: 16px; padding: 20px 28px 28px; max-width: 1280px; margin: 0 auto; }

  /* ── 2. Masthead ─────────────────────────────────────────── */
  .mast {
    display: grid; grid-template-columns: auto 1fr auto;
    gap: 24px; align-items: end;
    padding-bottom: 10px;
    border-bottom: 1px solid var(--border-hair);
  }
  .mast-l { display: flex; flex-direction: column; gap: 6px; }
  .date {
    margin: 0; font-family: var(--font-mono);
    font-size: 44px; font-weight: 500; line-height: 0.95; letter-spacing: -0.03em;
    color: var(--fg);
  }
  .mast-m { display: flex; flex-direction: column; gap: 6px; padding-bottom: 6px; }
  .mast-pills { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
  .mast-sub { font-family: var(--font-mono); font-size: 11px; color: var(--fg-dim); }
  .mast-r { display: flex; flex-direction: column; gap: 6px; align-items: flex-end; padding-bottom: 6px; }

  /* ── 3. Triage ───────────────────────────────────────────── */
  .triage { display: grid; grid-template-columns: minmax(0,1fr) minmax(0,1fr) 320px; gap: 12px; }
  .tc {
    position: relative; overflow: hidden;
    background: var(--bg-card); border: 1px solid var(--border-soft); border-radius: 10px;
    padding: 16px 18px; display: flex; flex-direction: column; gap: 10px;
  }
  .tc-bar { position: absolute; left: 0; top: 0; bottom: 0; width: 3px; background: var(--border); }
  .tc-alert .tc-bar { background: var(--alert); }
  .tc-warn  .tc-bar { background: var(--warn); }
  .tc-empty .tc-bar { background: var(--ok); }
  .tc-head { display: flex; align-items: center; justify-content: space-between; padding-left: 6px; }
  .tc-head-l { display: flex; align-items: center; gap: 10px; }
  .tc-title { margin: 0; padding-left: 6px; font-size: 15px; color: var(--fg); font-weight: 500; line-height: 1.3; letter-spacing: -0.01em; }
  .tc-body  { margin: 0; padding-left: 6px; font-size: 12.5px; color: var(--fg-soft); line-height: 1.5; }
  .tc-actions { display: flex; gap: 6px; padding-left: 6px; margin-top: auto; }

  .risks { background: var(--bg-card-2); border: 1px solid var(--border-soft); border-radius: 10px; padding: 14px 16px; display: flex; flex-direction: column; gap: 10px; }
  .risks-head { display: flex; align-items: baseline; justify-content: space-between; }
  .risks-num-row { display: flex; align-items: baseline; gap: 8px; }
  .risks-list { list-style: none; padding: 0; margin: 0; display: flex; flex-direction: column; gap: 5px; }
  .risks-list li { display: grid; grid-template-columns: 24px 1fr auto; align-items: center; gap: 8px; }
  .risks-list .mono { font-size: 11px; }
  .risks-dot { width: 4px; height: 4px; border-radius: 50%; background: var(--fg-muted); opacity: 0.7; }
  .risks-dot.alert { background: var(--alert); }
  .risks-dot.warn  { background: var(--warn); }
  .risks-dot.info  { background: var(--info); }

  /* ── 4. Hero ─────────────────────────────────────────────── */
  .hero { padding: 24px 26px; display: flex; flex-direction: column; gap: 18px; }
  .hero-head { display: flex; align-items: center; gap: 10px; }
  .hero-h {
    margin: 0; font-size: 26px; line-height: 1.3; font-weight: 500;
    letter-spacing: -0.015em; color: var(--fg);
    max-width: 38ch; text-wrap: balance;
  }
  .hero-empty { font-size: 18px; color: var(--fg-muted); max-width: 60ch; }
  .mk-accent { color: var(--fg); padding: 0 1px; background-image: linear-gradient(transparent 62%, color-mix(in oklch, var(--accent) 28%, transparent) 62%); }
  .mk-warn   { color: var(--fg); padding: 0 1px; background-image: linear-gradient(transparent 62%, color-mix(in oklch, var(--warn) 28%, transparent) 62%); }
  .mk-alert  { color: var(--fg); padding: 0 1px; background-image: linear-gradient(transparent 62%, color-mix(in oklch, var(--alert) 28%, transparent) 62%); }

  .bul-list { margin: 0; padding: 0; list-style: none; display: flex; flex-direction: column; gap: 14px; }
  .bul { display: grid; grid-template-columns: 28px 84px 1fr auto; gap: 14px; align-items: start; }
  .bul-num  { font-size: 11px; padding-top: 4px; }
  .bul-pill { align-self: start; margin-top: 2px; justify-content: center; min-width: 70px; text-align: center; }
  .bul-body { min-width: 0; }
  .bul-title { font-size: 14px; color: var(--fg); line-height: 1.4; font-weight: 500; margin-bottom: 2px; }
  .bul-desc  { margin: 0; color: var(--fg-soft); font-size: 12.75px; line-height: 1.5; max-width: 78ch; }
  .bul-ev    { display: flex; flex-direction: column; gap: 4px; align-items: flex-end; }
  .bul-ev .mono { font-size: 10.5px; color: var(--fg-dim); }
  .bul-repo {
    color: var(--fg-soft) !important;
    background: var(--bg-inset);
    border: 1px solid var(--border-hair);
    padding: 1px 7px;
    border-radius: 4px;
    font-size: 10.5px;
    letter-spacing: 0.01em;
  }

  /* ── 4b. Reflection cards (iterative-reflection phase 3) ─── */
  .refl-cards { margin: 0; padding: 0; list-style: none; display: flex; flex-direction: column; gap: 14px; }
  .refl-card  {
    border: 1px solid var(--border-soft);
    border-radius: 8px;
    background: var(--bg-card-2);
    padding: 12px 16px 14px;
    display: flex;
    flex-direction: column;
    gap: 10px;
  }
  .refl-head { display: flex; align-items: center; gap: 8px; font-size: 11.5px; color: var(--fg-muted); }
  .refl-num  { font-size: 11px; color: var(--fg-dim); letter-spacing: 0.04em; }
  .refl-ts   { color: var(--fg-soft); font-variant-numeric: tabular-nums; }
  .refl-sep  { color: var(--fg-dim); }
  .refl-repo {
    color: var(--fg-soft);
    background: var(--bg-inset);
    border: 1px solid var(--border-hair);
    padding: 1px 7px;
    border-radius: 4px;
    font-size: 10.5px;
  }
  .refl-chip { font-size: 10.5px; letter-spacing: 0.02em; }
  .refl-body { display: flex; flex-direction: column; gap: 4px; }
  .refl-title { font-size: 14.5px; color: var(--fg); font-weight: 500; line-height: 1.4; }
  .refl-desc  { margin: 0; color: var(--fg-soft); font-size: 13px; line-height: 1.5; max-width: 78ch; }
  .refl-disclose {
    align-self: flex-start;
    background: transparent;
    border: 1px solid var(--border-hair);
    border-radius: 6px;
    padding: 3px 8px;
    color: var(--fg-muted);
    font-family: var(--font-mono);
    font-size: 10.5px;
    cursor: pointer;
    display: inline-flex;
    align-items: center;
    gap: 6px;
    transition: color var(--t-fast), border-color var(--t-fast), background var(--t-fast);
  }
  .refl-disclose:hover { color: var(--fg); border-color: var(--border-soft); background: var(--bg-inset); }
  .refl-caret { display: inline-block; transition: transform 120ms ease-out; }
  .refl-caret.open { transform: rotate(90deg); }
  .refl-details {
    margin: 4px 0 0;
    padding: 0;
    list-style: none;
    display: flex;
    flex-direction: column;
    gap: 10px;
    border-top: 1px dashed var(--border-hair);
    padding-top: 10px;
  }
  .refl-detail { display: grid; grid-template-columns: 78px 1fr auto; gap: 12px; align-items: start; }
  .refl-detail-chip { align-self: start; margin-top: 2px; justify-content: center; min-width: 64px; text-align: center; font-size: 10px; }
  .refl-detail-body { min-width: 0; }
  .refl-detail-title { font-size: 13px; color: var(--fg); font-weight: 500; line-height: 1.4; }
  .refl-detail-desc  { margin: 2px 0 0; color: var(--fg-soft); font-size: 12px; line-height: 1.5; max-width: 78ch; }
  .refl-detail-ev    { display: flex; flex-direction: column; gap: 3px; align-items: flex-end; font-size: 10.5px; color: var(--fg-dim); }

  /* ── 5. Metrics ──────────────────────────────────────────── */
  .metrics { padding: 14px 4px; display: grid; grid-template-columns: repeat(6, 1fr); }
  .metric  { padding: 2px 18px; border-right: 1px solid var(--border-hair); display: flex; flex-direction: column; gap: 4px; min-width: 0; }
  .metric.no-bar { border-right: none; }
  .metric-k { font-size: 10px; }

  /* ── 6. Services ─────────────────────────────────────────── */
  .services { padding: 0; overflow: hidden; }
  .svc-head { padding: 14px 18px; display: flex; align-items: center; justify-content: space-between; border-bottom: 1px solid var(--border-hair); }
  .svc-head-sub { font-size: 12px; margin-left: 10px; }
  .svc-row {
    display: grid; grid-template-columns: minmax(0,1.4fr) 88px 88px 100px minmax(0,1.4fr) 28px;
    gap: 16px; padding: 14px 18px; border-bottom: 1px solid var(--border-hair); align-items: center;
  }
  .svc-row-th { padding: 10px 18px; align-items: baseline; }
  .svc-row-th .kick { font-size: 10px; }
  .svc-row:last-child { border-bottom: none; }
  .svc-name { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
  .svc-name .mono { font-size: 14px; color: var(--fg); }
  .svc-loops { display: flex; flex-direction: column; gap: 4px; min-width: 0; }
  .loop { display: flex; align-items: baseline; gap: 8px; min-width: 0; }
  .loop-prefix { width: 12px; text-align: center; flex-shrink: 0; font-size: 11px; color: var(--fg-dim); }
  .loop-label  { font-size: 11.5px; color: var(--fg); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; max-width: 55%; }
  .loop-note   { font-size: 10.5px; color: var(--fg-dim); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; flex: 1; min-width: 0; }
  .svc-empty   { padding: 24px; color: var(--fg-muted); font-family: var(--font-mono); font-size: 12px; text-align: center; }
  .svc-hide {
    background: transparent; border: 1px solid var(--border-hair);
    color: var(--fg-dim); width: 26px; height: 26px;
    border-radius: 6px; cursor: pointer;
    display: inline-flex; align-items: center; justify-content: center;
    transition: color var(--t-fast), border-color var(--t-fast), background var(--t-fast);
  }
  .svc-hide svg { width: 14px; height: 14px; display: block; }
  .svc-hide:hover { background: var(--bg-card-2); border-color: var(--border); color: var(--fg); }

  /* ── 7. Timeline ─────────────────────────────────────────── */
  .timeline { padding: 0; display: grid; grid-template-columns: 240px 1fr; }
  .tl-side { padding: 16px 18px; border-right: 1px solid var(--border-hair); display: flex; flex-direction: column; gap: 12px; }
  .tl-stat { display: flex; align-items: baseline; justify-content: space-between; }
  .tl-stat .mono { font-size: 11px; }
  .tl-stat-sub { font-size: 10px; margin-left: 6px; }
  .tl-main { padding: 16px 20px; display: flex; flex-direction: column; gap: 10px; min-width: 0; }
  .tl-h { display: flex; align-items: baseline; justify-content: space-between; }
  .tl-h .mono { font-size: 11.5px; }

  /* ── responsive ──────────────────────────────────────────── */
  @media (max-width: 1180px) {
    .rail { grid-template-columns: 1fr auto auto; padding: 10px 16px; }
    .body { padding: 16px; }
    .triage { grid-template-columns: 1fr 1fr; }
    .triage .risks { grid-column: 1 / -1; }
    .metrics { grid-template-columns: repeat(3, 1fr); }
    .metric:nth-child(3n) { border-right: none; }
    .timeline { grid-template-columns: 1fr; }
    .tl-side { border-right: none; border-bottom: 1px solid var(--border-hair); }
  }
  @media (max-width: 720px) {
    .mast { grid-template-columns: 1fr; gap: 14px; }
    .triage { grid-template-columns: 1fr; }
    .metrics { grid-template-columns: repeat(2, 1fr); }
    .metric:nth-child(2n) { border-right: none; }
    .svc-row, .svc-row-th { grid-template-columns: 1fr; gap: 4px; }
  }
</style>
