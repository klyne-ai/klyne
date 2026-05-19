<!-- PROTOTYPE: deliberately rough; real UI/UX is a separate brainstorm (spec scope) -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { fetchProductivity, type ProductivityReport } from '$lib/api.js';

  let rep = $state<ProductivityReport | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);

  async function load(): Promise<void> {
    loading = true;
    try {
      // No params → server defaults to today local 00:00 → now.
      rep = await fetchProductivity();
      error = null;
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'failed to load productivity';
    } finally {
      loading = false;
    }
  }

  onMount(() => { void load(); });

  function hm(min: number): string {
    const h = Math.floor(min / 60);
    const m = min % 60;
    return `~${h}h ${m}m`;
  }
</script>

<h1>Productivity (prototype "dirt dashboard")</h1>

{#if loading}
  <p>loading…</p>
{:else if error}
  <p style="color:red">error: {error}</p>
{:else if rep}
  <!-- Top banner: reflection status (L3) + nudge -->
  <div style="border:1px solid #999; padding:8px; margin-bottom:12px;">
    <strong>Day:</strong> {rep.day}
    &nbsp;|&nbsp;
    <strong>reflection_status:</strong> {rep.reflection_status}
    {#if rep.nudge}
      <div style="color:#a60; margin-top:4px;">{rep.nudge}</div>
    {/if}
  </div>

  {#if rep.services.length === 0}
    <p>No services in this window.</p>
  {/if}

  {#each rep.services as svc (svc.repo + svc.project_path)}
    <section style="margin-bottom:20px;">
      <h2>{svc.repo} {#if svc.manual_only}<small>(manual — no AI session)</small>{/if}</h2>
      <div style="font-size:12px; color:#666;">{svc.project_path}</div>

      {#if svc.risks.length > 0}
        <ul style="color:red;">
          {#each svc.risks as risk (risk.kind + risk.detail)}
            <li>
              <strong>{risk.kind}</strong>: {risk.detail}
              {#if risk.age_minutes > 0}({hm(risk.age_minutes)} ago){/if}
            </li>
          {/each}
        </ul>
      {/if}

      {#each svc.branches as br (br.name)}
        <div style="margin:8px 0; padding-left:12px; border-left:3px solid #ccc;">
          <div>
            <code>{br.ship}</code>
            &middot; {br.ticket_id || '(no ticket)'}
            &middot; {hm(br.attributed_minutes)}
            &middot; {br.narrative}
          </div>
          <details>
            <summary>{br.commits.length} commit(s)</summary>
            <ul>
              {#each br.commits as c (c.sha)}
                <li><code>{c.sha}</code> {c.subject}</li>
              {/each}
            </ul>
          </details>
        </div>
      {/each}
    </section>
  {/each}
{/if}
