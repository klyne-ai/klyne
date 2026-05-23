<!--
  Logo — klyne mark v2. Geometric lowercase `k` on a 16-unit grid:
  vertical stem at x=6, two diagonals converging into the stem at the
  midline (y=16), and a gold node at the junction. The node is the same
  pip used as the live/idle streaming indicator across the dashboard.

  Stroke uses `currentColor` so the mark adapts to fg per theme. Node
  fills with var(--accent). Strokes use square caps for the precise,
  infra-tooling feel that matches the tile-border vocabulary.
-->
<script lang="ts">
  interface Props {
    /** Pixel size of the (square) mark. Stroke widens slightly at <=20. */
    size?: number;
  }
  const { size = 24 }: Props = $props();

  // Stroke compensation: scale ladder calls for 3.4 at small sizes, 3.2 above.
  const stroke = $derived(size <= 20 ? 3.4 : 3.2);
</script>

<svg
  width={size}
  height={size}
  viewBox="0 0 32 32"
  fill="none"
  stroke="currentColor"
  stroke-width={stroke}
  stroke-linecap="square"
  aria-hidden="true"
>
  <line x1="6" y1="3" x2="6" y2="29" />
  <line x1="6" y1="16" x2="24" y2="3" />
  <line x1="6" y1="16" x2="24" y2="29" />
  <circle cx="6" cy="16" r="1.7" fill="var(--accent)" stroke="none" />
</svg>
