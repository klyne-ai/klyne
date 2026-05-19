/**
 * Context-aware internal link builders.
 *
 * `basePath` is '' in the Work-tab route tree and '/insights' under the
 * Insights tab. Threading it through every internal link is what keeps a
 * drill chain (project → session → back) inside whichever top-level tab
 * the user started from. See
 * docs/superpowers/specs/2026-05-19-insights-self-contained-drill-in-design.md
 */

export function sessionHref(basePath: string, sessionId: string): string {
  return `${basePath}/sessions/${encodeURIComponent(sessionId)}`;
}

export function projectHref(basePath: string, projectName: string): string {
  return `${basePath}/projects/${encodeURIComponent(projectName)}`;
}

/**
 * Back target from a session view. With a known project, go to that
 * project's detail in the same tab; otherwise fall back to the tab's
 * project index (Work: '/projects'; Insights: '/insights', since the
 * ranked list lives on the Insights page itself).
 */
export function sessionBackHref(basePath: string, projectName: string): string {
  return projectName ? projectHref(basePath, projectName) : (basePath || '/projects');
}
