<!--
  TabFiles — Files & Tools tab for a project.
  Uses data from ProjectInsight if available.

  TODO: ProjectInsightResponse does NOT expose `files_top` or `tools_top` fields
  as of the current W0-frozen contract (internal/api/contracts.go). Once those
  fields are added to the backend, wire them here. Until then, render a
  "data pending" empty state.

  Fields needed from the backend:
    - ProjectInsight.files_top: Array<{ path: string; count: number; last_touched: string }>
    - ProjectInsight.tools_top: Array<{ name: string; count: number }>
-->
<script lang="ts">
  import type { ProjectInsight } from '$lib/types.js';

  interface Props {
    insight: ProjectInsight | null;
  }
  const { insight }: Props = $props();

  // insight is available but files_top / tools_top are not part of the
  // frozen contract yet — render empty state and surface what we do have.
</script>

<div style="display: flex; flex-direction: column; gap: 14px;">
  {#if insight}
    <!-- Show available insight stats while files/tools await backend support -->
    <div
      style="
        background: var(--ad-bg-2);
        border: 1px solid var(--ad-border);
        border-radius: 8px;
        padding: 14px 16px;
      "
    >
      <div class="ad-mono" style="font-size: 11px; color: var(--ad-faint); text-transform: uppercase; letter-spacing: 0.06em; margin-bottom: 10px;">
        Cache performance
      </div>
      <div style="display: flex; gap: 20px; flex-wrap: wrap;">
        <div style="display: flex; flex-direction: column; gap: 2px;">
          <span style="font-size: 11px; color: var(--ad-faint);">Cache hit %</span>
          <span class="ad-mono" style="font-size: 18px; font-weight: 600; color: var(--ad-fg);">
            {insight.cache_hit_pct.toFixed(1)}%
          </span>
        </div>
        <div style="display: flex; flex-direction: column; gap: 2px;">
          <span style="font-size: 11px; color: var(--ad-faint);">Compacts</span>
          <span class="ad-mono" style="font-size: 18px; font-weight: 600; color: var(--ad-fg);">
            {insight.compact_count}
          </span>
        </div>
        <div style="display: flex; flex-direction: column; gap: 2px;">
          <span style="font-size: 11px; color: var(--ad-faint);">Tokens / message</span>
          <span class="ad-mono" style="font-size: 18px; font-weight: 600; color: var(--ad-fg);">
            {Math.round(insight.tokens_per_message).toLocaleString()}
          </span>
        </div>
      </div>
    </div>
  {/if}

  <!-- Top files placeholder -->
  <div
    style="
      background: var(--ad-bg-2);
      border: 1px solid var(--ad-border);
      border-radius: 8px;
      padding: 14px 16px;
    "
  >
    <div class="ad-mono" style="font-size: 11px; color: var(--ad-faint); text-transform: uppercase; letter-spacing: 0.06em; margin-bottom: 10px;">
      Top files touched
    </div>
    <div style="padding: 20px 0; text-align: center; color: var(--ad-faint); font-size: 12px;">
      Data pending — backend field <code class="ad-mono">files_top</code> not yet in contract.
    </div>
  </div>

  <!-- Tools used placeholder -->
  <div
    style="
      background: var(--ad-bg-2);
      border: 1px solid var(--ad-border);
      border-radius: 8px;
      padding: 14px 16px;
    "
  >
    <div class="ad-mono" style="font-size: 11px; color: var(--ad-faint); text-transform: uppercase; letter-spacing: 0.06em; margin-bottom: 10px;">
      Tools used
    </div>
    <div style="padding: 20px 0; text-align: center; color: var(--ad-faint); font-size: 12px;">
      Data pending — backend field <code class="ad-mono">tools_top</code> not yet in contract.
    </div>
  </div>
</div>
