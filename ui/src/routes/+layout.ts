// Pure SPA mode for adapter-static.
//
// Without these flags, SvelteKit tries to prerender every route and fails on
// dynamic routes like /projects/[name] and /sessions/[id] (no entries declared).
// `ssr: false` makes pages render entirely on the client; `prerender: false`
// disables build-time HTML generation. The fallback `index.html` from the
// adapter then handles every request and the client router takes over.

export const ssr = false;
export const prerender = false;
export const trailingSlash = 'never';
