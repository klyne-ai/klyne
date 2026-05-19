<script lang="ts">
  import { page } from '$app/stores';
  import SessionDetail from '$lib/ui/SessionDetail.svelte';

  const sessionId = $derived($page.params.id ?? '');
</script>

<SessionDetail {sessionId} basePath="/insights" />
