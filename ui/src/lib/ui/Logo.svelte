<!--
  Logo — combined K-with-merge brand mark. Distills the document-merge
  motif into a clean K with two converging merge lines + accent dot.
  Inherits currentColor for the K body so it adapts per-theme; uses
  --brand-accent / --brand-line for accent details.
-->
<script lang="ts">
  interface Props {
    size?: number;
  }
  const { size = 28 }: Props = $props();
</script>

<svg width={size} height={size} viewBox="0 0 32 32" fill="none" aria-hidden="true">
  <path d="M2 11 C6 11, 7.5 13, 9.5 15" stroke="var(--brand-line)" stroke-width="1.6" stroke-linecap="round" />
  <path d="M2 21 C6 21, 7.5 19, 9.5 17" stroke="var(--brand-line)" stroke-width="1.6" stroke-linecap="round" />
  <circle cx="11" cy="16" r="2" fill="var(--brand-accent)" />
  <rect x="14.2" y="5.5" width="2.8" height="21" rx="0.6" fill="currentColor" />
  <path d="M17 16 L25.5 6.5" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" />
  <path d="M17 16 L25.5 25.5" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" />
</svg>
