<script lang="ts">
  import { page } from '$app/stores';
  import ProjectDetail from '$lib/ui/ProjectDetail.svelte';

  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const projectName = $derived(decodeURIComponent(($page.params as any)['name'] ?? ''));
</script>

<ProjectDetail {projectName} basePath="" backHref="/projects" />
