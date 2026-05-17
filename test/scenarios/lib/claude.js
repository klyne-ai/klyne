// Spawn `claude -p` and capture structured output.
// Each invocation is a fresh session — that's intentional: we want the Stop
// hook to fire between turns so worklog entries get written.

import { spawn } from "node:child_process";
import { existsSync } from "node:fs";
import path from "node:path";

const DEFAULT_TIMEOUT_MS = 180_000; // 3 min per turn
const DEFAULT_BUDGET_USD = "0.50";

/**
 * Run `claude -p` once. Returns parsed JSON result.
 *
 * @param {object} opts
 * @param {string} opts.cwd - Working directory (also becomes klyne project_path)
 * @param {string} opts.prompt - User prompt
 * @param {string[]} [opts.allowedTools] - Optional tool allowlist
 * @param {number} [opts.timeoutMs]
 * @param {string} [opts.model] - e.g. "claude-haiku-4-5" to cut cost
 * @param {string} [opts.settingsPath] - Path to a settings JSON file (used by harness to inject the klyne Stop hook)
 * @returns {Promise<{sessionId:string|null,text:string,costUsd:number,raw:object,exitCode:number,stderr:string}>}
 */
export async function runClaude(opts) {
  const {
    cwd,
    prompt,
    allowedTools,
    timeoutMs = DEFAULT_TIMEOUT_MS,
    model,
    settingsPath,
  } = opts;

  if (!cwd) throw new Error("runClaude: cwd is required");
  if (!prompt) throw new Error("runClaude: prompt is required");

  // Auto-detect a project-level settings file (where tmpdir.js seeds the
  // klyne Stop hook) if the caller didn't pass one explicitly.
  let effectiveSettingsPath = settingsPath;
  if (!effectiveSettingsPath) {
    const candidate = path.join(cwd, ".claude/settings.json");
    if (existsSync(candidate)) effectiveSettingsPath = candidate;
  }

  const args = [
    "-p",
    "--output-format",
    "json",
    "--max-budget-usd",
    DEFAULT_BUDGET_USD,
    "--permission-mode",
    "bypassPermissions",
    // Force-load user + project + local settings (in -p mode the default is
    // narrower and may skip project-level hook registration).
    "--setting-sources",
    "user,project,local",
  ];
  if (effectiveSettingsPath) {
    // `--settings <file>` adds an additional, fully-trusted settings file.
    // This is how we inject the klyne Stop hook into ephemeral test projects.
    args.push("--settings", effectiveSettingsPath);
  }
  if (model) args.push("--model", model);
  if (allowedTools && allowedTools.length > 0) {
    args.push("--allowed-tools", allowedTools.join(","));
  }
  args.push(prompt);

  return new Promise((resolve) => {
    const child = spawn("claude", args, {
      cwd,
      env: { ...process.env },
      stdio: ["ignore", "pipe", "pipe"],
    });

    let stdout = "";
    let stderr = "";
    let killed = false;

    const timer = setTimeout(() => {
      killed = true;
      child.kill("SIGKILL");
    }, timeoutMs);

    child.stdout.on("data", (b) => (stdout += b.toString()));
    child.stderr.on("data", (b) => (stderr += b.toString()));

    child.on("close", (exitCode) => {
      clearTimeout(timer);
      let raw = null;
      let text = "";
      let costUsd = 0;
      let sessionId = null;

      try {
        raw = JSON.parse(stdout);
        // Claude Code -p JSON output shape (best-effort; tolerate variation):
        // { result, session_id, total_cost_usd, ... }
        text = raw.result ?? raw.text ?? raw.message?.content ?? "";
        if (Array.isArray(text)) {
          text = text.map((p) => p.text ?? "").join("\n");
        }
        sessionId = raw.session_id ?? raw.sessionId ?? null;
        costUsd = raw.total_cost_usd ?? raw.cost_usd ?? 0;
      } catch (e) {
        // Output wasn't JSON — keep raw stdout as text for debugging.
        text = stdout;
      }

      resolve({
        sessionId,
        text,
        costUsd,
        raw,
        exitCode: killed ? -1 : exitCode ?? 0,
        stderr,
        killed,
      });
    });

    child.on("error", (err) => {
      clearTimeout(timer);
      resolve({
        sessionId: null,
        text: "",
        costUsd: 0,
        raw: null,
        exitCode: -1,
        stderr: `spawn error: ${err.message}`,
        killed: false,
      });
    });
  });
}

/**
 * Run a sequence of fresh `claude -p` invocations against the same cwd.
 * Each turn = new session = Stop hook fires.
 */
export async function runClaudeTurns({ cwd, turns, model, allowedTools, timeoutMs }) {
  const results = [];
  for (const prompt of turns) {
    const r = await runClaude({ cwd, prompt, model, allowedTools, timeoutMs });
    results.push(r);
    if (r.exitCode !== 0) break; // abort chain on failure
  }
  return results;
}

export function totalCost(results) {
  return results.reduce((sum, r) => sum + (r.costUsd || 0), 0);
}
