// UI feature flags.
//
// SHOW_REFLECTIONS — reversible dark-launch toggle for the reflection /
// worklog UI. When false, every reflection surface is hidden (the Reflect
// button, the reflection status pill, the legacy reflection bullets, the
// Worklog sidebar tab + project-panel tab, the projects-list state pill, and
// the /worklog routes). The backend, MCP tools, and the worklog_reflections
// table are untouched — flip this to true to restore the UI instantly.
//
// Rationale + scope: docs/superpowers/specs/2026-05-29-hide-reflection-ui-design.md
export const SHOW_REFLECTIONS = false;
