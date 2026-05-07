<script lang="ts">
  interface Props {
    status: 'active' | 'idle' | 'compacted';
  }

  const { status }: Props = $props();

  const label = $derived(status === 'active' ? 'active' : status === 'compacted' ? 'compacted' : 'idle');
  const cls = $derived(status === 'active' ? 'ad-badge--active' : status === 'compacted' ? 'ad-badge--compact' : 'ad-badge--idle');
</script>

<span class="ad-badge {cls}">
  <span class="ad-dot ad-dot--{status}"></span>
  {label}
</span>
