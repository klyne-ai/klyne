<script lang="ts">
  import { ICON_PATHS, type IconName } from './icons';
  interface Props { name: IconName; size?: number; }
  const { name, size = 16 }: Props = $props();
</script>

<svg
  width={size} height={size} viewBox="0 0 16 16"
  fill="none" stroke="currentColor" stroke-width="1.4"
  stroke-linecap="round" stroke-linejoin="round"
>
  {@html ICON_PATHS[name]}
</svg>
