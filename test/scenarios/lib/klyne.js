// Query helpers against ~/.klyne/klyne.db via the sqlite3 CLI.
// No native bindings — keeps this harness install-free.

import { execFileSync } from "node:child_process";
import { existsSync } from "node:fs";
import { homedir } from "node:os";
import path from "node:path";

const DB_PATH = process.env.KLYNE_DB_PATH || path.join(homedir(), ".klyne/klyne.db");

function assertDB() {
  if (!existsSync(DB_PATH)) {
    throw new Error(
      `klyne DB not found at ${DB_PATH}. Run klyne at least once, or set KLYNE_DB_PATH.`,
    );
  }
}

/**
 * Run a SELECT and return rows as objects. Uses sqlite3 -json.
 * Params are inlined with simple escaping (sqlite3 CLI lacks proper parameter binding;
 * we restrict to string params and quote-escape).
 */
export function sql(query, params = {}) {
  assertDB();
  let q = query;
  for (const [k, v] of Object.entries(params)) {
    const escaped = String(v).replace(/'/g, "''");
    q = q.replaceAll(`:${k}`, `'${escaped}'`);
  }
  const out = execFileSync("sqlite3", ["-json", DB_PATH, q], {
    encoding: "utf8",
    maxBuffer: 32 * 1024 * 1024,
  });
  if (!out.trim()) return [];
  return JSON.parse(out);
}

export function exec(query, params = {}) {
  assertDB();
  let q = query;
  for (const [k, v] of Object.entries(params)) {
    const escaped = String(v).replace(/'/g, "''");
    q = q.replaceAll(`:${k}`, `'${escaped}'`);
  }
  // Swallow stderr — caller decides whether to surface failures.
  execFileSync("sqlite3", [DB_PATH, q], { encoding: "utf8", stdio: ["ignore", "ignore", "ignore"] });
}

export function getStopSummariesForProject(projectPath) {
  return sql(
    `SELECT session_id, ts, project_path, cli, recap_visible, recap_topic,
            importance, signature, files_json, summary
       FROM stop_summaries
      WHERE project_path = :p
      ORDER BY ts ASC`,
    { p: projectPath },
  );
}

export function getReflectionsForProject(projectPath) {
  return sql(
    `SELECT id, ts, project_path, tier, title, body_md,
            evidence_entry_ids_json, importance, state
       FROM worklog_reflections
      WHERE project_path = :p
      ORDER BY ts ASC`,
    { p: projectPath },
  );
}

export function getDecisionsForProject(projectPath) {
  // klyne `decisions` table holds remember_memory / recall_memory entries
  // (verified via internal/mcpserver/tool_memory.go). project_path = "" for globals.
  return sql(
    `SELECT id, ts, project_path, session_id, text, tags_json
       FROM decisions
      WHERE project_path = :p
      ORDER BY ts ASC`,
    { p: projectPath },
  );
}

/**
 * Delete all rows scoped to a given project_path. Best-effort cleanup.
 */
export function cleanupProject(projectPath) {
  const tables = [
    "stop_summaries",
    "worklog_reflections",
    "decisions",
    "sessions",
    "session_summaries",
  ];
  for (const t of tables) {
    try {
      exec(`DELETE FROM ${t} WHERE project_path = :p`, { p: projectPath });
    } catch {
      // table may not have project_path column — skip
    }
  }
}

/**
 * Poll for new stop_summary rows after a given timestamp.
 * Returns the rows when count threshold met, or [] on timeout.
 *
 * Important: the klyne Stop hook is invoked by Claude Code asynchronously
 * after the claude -p process exits. There can be a 5-60s lag before the
 * row is committed to SQLite. Callers should be generous with timeoutMs.
 */
export async function waitForStopSummaries(projectPath, sinceTs, { minCount = 1, timeoutMs = 90_000 } = {}) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const rows = sql(
      `SELECT session_id, ts, recap_visible, importance, recap_topic, files_json, summary
         FROM stop_summaries
        WHERE project_path = :p AND ts >= :since
        ORDER BY ts ASC`,
      { p: projectPath, since: String(sinceTs) },
    );
    if (rows.length >= minCount) return rows;
    await new Promise((r) => setTimeout(r, 1000));
  }
  return [];
}

/**
 * Unfiltered diagnostic query: returns ALL stop_summaries rows for the project,
 * regardless of timestamp. Use AFTER a poll times out to see whether the hook
 * eventually fired (just outside the window).
 */
export function getAllStopSummariesForProject(projectPath) {
  return sql(
    `SELECT session_id, ts, recap_visible, importance, recap_topic, files_json, summary
       FROM stop_summaries
      WHERE project_path = :p
      ORDER BY ts ASC`,
    { p: projectPath },
  );
}

export async function waitForReflections(projectPath, sinceTs, { timeoutMs = 30_000 } = {}) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const rows = sql(
      `SELECT id, ts, title, body_md, evidence_entry_ids_json, state
         FROM worklog_reflections
        WHERE project_path = :p AND ts >= :since
        ORDER BY ts ASC`,
      { p: projectPath, since: String(sinceTs) },
    );
    if (rows.length > 0) return rows;
    await new Promise((r) => setTimeout(r, 500));
  }
  return [];
}

export function nowMs() {
  return Date.now();
}

export { DB_PATH };
