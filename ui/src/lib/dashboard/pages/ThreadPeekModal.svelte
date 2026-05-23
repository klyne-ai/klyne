<!--
  Thread peek modal — opens from a Live tile's "expand" button. Shows
  the full message tail for one session in a centered modal, pop-animated
  from the button's origin (computed via getBoundingClientRect). Each
  message slides+fades in sequentially (~55ms apart) for the staggered
  reveal the design specifies. Click scrim, press Esc, or hit ✕ to
  dismiss; closing plays the reverse fade/scale.
-->
<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { sessionUrl } from '$lib/dashboard/url-state';
  import { relAgo } from '$lib/format';
  import { isConversationalMessage } from '$lib/messageFilters';
  import type { CockpitThread, Message } from '$lib/types';

  interface Props {
    thread: CockpitThread;
    messages: Message[];
    origin: { x: number; y: number } | null;
    tickMs: number;
    onClose: () => void;
  }
  const { thread, messages, origin, tickMs, onClose }: Props = $props();

  let closing = $state(false);
  const visible = $derived(messages.filter(isConversationalMessage));
  const isLive = $derived(tickMs - thread.last_msg_at < 60_000);

  function close(): void {
    closing = true;
    // match the CSS animation duration in dashboard.css
    setTimeout(onClose, 140);
  }

  function openFullSession(): void {
    onClose();
    void goto(sessionUrl($page.url.pathname + $page.url.search, thread.session_id));
  }

  function onKey(e: KeyboardEvent): void {
    if (e.key === 'Escape') {
      e.preventDefault();
      close();
    }
  }

  let prevOverflow = '';
  onMount(() => {
    window.addEventListener('keydown', onKey);
    prevOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
  });
  onDestroy(() => {
    window.removeEventListener('keydown', onKey);
    document.body.style.overflow = prevOverflow;
  });

  const cssOrigin = $derived(
    origin
      ? `--tm-origin-x:${origin.x}vw;--tm-origin-y:${origin.y}vh;`
      : ''
  );

  const projectName = $derived(
    thread.project_path.split('/').filter(Boolean).pop() ?? thread.project_path
  );
</script>

<div
  class="thread-scrim"
  class:is-closing={closing}
  onclick={close}
  role="presentation"
></div>

<div
  class="thread-modal"
  class:is-closing={closing}
  style={cssOrigin}
  role="dialog"
  aria-modal="true"
  aria-label={`Thread for ${projectName} · ${thread.session_id.slice(0, 8)}`}
>
  <header class="thread-modal__head">
    <span class="dot" class:ok={isLive}></span>
    <div class="meta">
      <h3>
        <span>{projectName}</span>
        <span
          class="pill"
          class:pill-claude={thread.cli === 'claude'}
          class:pill-codex={thread.cli === 'codex'}
          style:font-size="10px"
        >{thread.cli}</span>
      </h3>
      <div class="sub">
        <span>{thread.session_id.slice(0, 8)}</span>
        <span>·</span>
        <span>{thread.git_branch ?? '—'}</span>
        <span>·</span>
        <span>{visible.length} msg{visible.length === 1 ? '' : 's'}</span>
        <span>·</span>
        <span>last {relAgo(tickMs - thread.last_msg_at)} ago</span>
      </div>
    </div>
    <button class="k-btn k-btn--ghost" onclick={close} aria-label="Close">✕</button>
  </header>

  <div class="thread-modal__body">
    {#each visible as m, i (m.id)}
      <div class="msg" style:animation-delay="{i * 55}ms">
        <div class="role">{m.role}</div>
        <p>{m.content}</p>
      </div>
    {/each}
    {#if visible.length === 0}
      <p class="mono dim">no conversational turns captured yet</p>
    {/if}
  </div>

  <footer class="thread-modal__foot">
    <span class="mono dim" style:font-size="11px">
      {isLive ? 'streaming' : 'idle'} · {relAgo(tickMs - thread.last_msg_at)} ago
    </span>
    <button class="k-btn" onclick={openFullSession}>open full session →</button>
  </footer>
</div>
