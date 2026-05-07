<script lang="ts">
  interface Props {
    cli: 'claude' | 'codex';
  }

  const { cli }: Props = $props();
</script>

<span class="ad-badge ad-badge--{cli}">{cli}</span>
