import { redirect } from '@sveltejs/kit';
import { SHOW_REFLECTIONS } from '$lib/featureFlags';

// The worklog views are reflection-centric. When reflections are hidden
// (SHOW_REFLECTIONS=false) the sidebar tab is removed, but guard the routes
// too so a stale bookmark to /worklog or /worklog/project doesn't surface
// reflection UI — send it to the productivity dashboard instead.
export const load = () => {
  if (!SHOW_REFLECTIONS) {
    throw redirect(307, '/productivity');
  }
};
