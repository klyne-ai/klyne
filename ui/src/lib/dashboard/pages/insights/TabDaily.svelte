<script lang="ts">
  import { kfmt } from '$lib/format.js';
  import type { UsageStatsResponse, DailyRow } from '$lib/types.js';

  interface Props {
    stats: UsageStatsResponse | null;
  }
  const { stats }: Props = $props();

  type SortCol = 'date' | 'messages' | 'input' | 'output' | 'cache_read' | 'cache_write' | 'total';
  type SortDir = 'asc' | 'desc';

  let sortCol = $state<SortCol>('date');
  let sortDir = $state<SortDir>('desc');

  function ariaSort(col: SortCol): 'ascending' | 'descending' | 'none' {
    if (sortCol !== col) return 'none';
    return sortDir === 'asc' ? 'ascending' : 'descending';
  }

  function setSort(col: SortCol): void {
    if (sortCol === col) {
      sortDir = sortDir === 'asc' ? 'desc' : 'asc';
    } else {
      sortCol = col;
      // Date defaults desc (newest first); numeric cols default desc (highest first)
      sortDir = 'desc';
    }
  }

  function sortIndicator(col: SortCol): string {
    if (sortCol !== col) return '';
    return sortDir === 'asc' ? ' ↑' : ' ↓';
  }

  const sortedRows = $derived.by<DailyRow[]>(() => {
    const rows: DailyRow[] = stats ? [...stats.daily] : [];
    rows.sort((a, b) => {
      let cmp: number;
      switch (sortCol) {
        case 'date':
          cmp = a.date.localeCompare(b.date);
          break;
        case 'messages':
          cmp = a.messages - b.messages;
          break;
        case 'input':
          cmp = a.input - b.input;
          break;
        case 'output':
          cmp = a.output - b.output;
          break;
        case 'cache_read':
          cmp = a.cache_read - b.cache_read;
          break;
        case 'cache_write':
          cmp = a.cache_write - b.cache_write;
          break;
        case 'total':
          cmp = a.total - b.total;
          break;
        default:
          cmp = 0;
      }
      return sortDir === 'asc' ? cmp : -cmp;
    });
    return rows;
  });

  const thStyle = 'text-align: right; padding: 10px 12px; font-weight: 500; color: var(--ad-muted); font-size: 11px; text-transform: uppercase; letter-spacing: 0.06em; cursor: pointer; user-select: none; white-space: nowrap;';
  const thStyleLeft = 'text-align: left; padding: 10px 16px; font-weight: 500; color: var(--ad-muted); font-size: 11px; text-transform: uppercase; letter-spacing: 0.06em; cursor: pointer; user-select: none; white-space: nowrap;';
</script>

<div class="ad-card" style="padding: 0;">
  {#if !stats || stats.daily.length === 0}
    <div style="padding: 32px 16px; color: var(--ad-faint); font-size: 13px; text-align: center;">No daily data in this window.</div>
  {:else}
    <table style="width: 100%; border-collapse: collapse; font-size: 13px;">
      <thead style="background: var(--ad-bg-2);">
        <tr>
          <th
            style={thStyleLeft}
            aria-sort={ariaSort('date')}
            onclick={() => setSort('date')}
          >Date{sortIndicator('date')}</th>
          <th
            style={thStyle}
            aria-sort={ariaSort('messages')}
            onclick={() => setSort('messages')}
          >Messages{sortIndicator('messages')}</th>
          <th
            style={thStyle}
            aria-sort={ariaSort('input')}
            onclick={() => setSort('input')}
          >Input{sortIndicator('input')}</th>
          <th
            style={thStyle}
            aria-sort={ariaSort('output')}
            onclick={() => setSort('output')}
          >Output{sortIndicator('output')}</th>
          <th
            style={thStyle}
            aria-sort={ariaSort('cache_read')}
            onclick={() => setSort('cache_read')}
          >Cache read{sortIndicator('cache_read')}</th>
          <th
            style={thStyle}
            aria-sort={ariaSort('cache_write')}
            onclick={() => setSort('cache_write')}
          >Cache write{sortIndicator('cache_write')}</th>
          <th
            style={thStyle}
            aria-sort={ariaSort('total')}
            onclick={() => setSort('total')}
          >Total{sortIndicator('total')}</th>
        </tr>
      </thead>
      <tbody>
        {#each sortedRows as d (d.date)}
          <tr style="border-top: 1px solid var(--ad-border-soft);">
            <td class="mono" style="padding: 10px 16px;">{d.date}</td>
            <td class="mono ad-tnum" style="padding: 10px 12px; text-align: right;">{d.messages}</td>
            <td class="mono ad-tnum" style="padding: 10px 12px; text-align: right;">{kfmt(d.input)}</td>
            <td class="mono ad-tnum" style="padding: 10px 12px; text-align: right;">{kfmt(d.output)}</td>
            <td class="mono ad-tnum" style="padding: 10px 12px; text-align: right;">{kfmt(d.cache_read)}</td>
            <td class="mono ad-tnum" style="padding: 10px 12px; text-align: right;">{kfmt(d.cache_write)}</td>
            <td class="mono ad-tnum" style="padding: 10px 12px; text-align: right; font-weight: 600;">{kfmt(d.total)}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</div>
