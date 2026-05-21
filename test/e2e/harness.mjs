#!/usr/bin/env node
// e2e harness — verifies the daemon-free klyne flow end-to-end:
//
//   1. UserPromptSubmit hook injects the KLYNE_SUMMARY instruction.
//   2. The user's `claude --print` call (their subscription, their
//      interactive turn) emits the per-turn summary inline.
//   3. The Stop hook (klyne session-end) parses it out and writes
//      ai_drafted_summary to stop_summaries.
//   4. The daemon NEVER spawns a `claude --print` subprocess on its
//      own — the only `claude --print` we ever see during the test is
//      the one the harness itself runs.
//
// Usage:
//   node test/e2e/harness.mjs <scenario-name>
//
// Prerequisites:
//   - Binaries built: `make build`
//   - Hooks wired into the REAL ~/.claude/settings.json:
//       ./bin/klyne mcp install --platform claude
//   - klyne daemon running against the REAL ~/.klyne/klyne.db:
//       ./bin/klyne start &
//   - `claude` CLI on PATH and signed in.
//   - `sqlite3` CLI on PATH.

import { spawn, execFileSync } from "node:child_process";
import { mkdtempSync, existsSync, realpathSync } from "node:fs";
import { homedir, tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { setTimeout as sleep } from "node:timers/promises";

const REPO_ROOT = resolve(new URL("../..", import.meta.url).pathname);
const KLYNE_DB = join(homedir(), ".klyne", "klyne.db");

function die(msg) {
  console.error(`harness: ${msg}`);
  process.exit(1);
}

function sql(query) {
  // sqlite3 CLI avoids pulling in a node binding.
  return execFileSync("sqlite3", [KLYNE_DB, "-json", query], {
    encoding: "utf8",
    maxBuffer: 16 * 1024 * 1024,
  });
}

function listClaudeSubprocesses() {
  // Returns the cmdlines of any `claude --print` /
  // `claude --output-format stream-json` invocations currently
  // running. Used to detect the recursion that bit us before.
  try {
    const out = execFileSync("/bin/ps", ["-eo", "pid,command"], {
      encoding: "utf8",
    });
    return out
      .split("\n")
      .filter((l) => /claude (--print|--output-format stream-json)/.test(l))
      .map((l) => l.trim());
  } catch {
    return [];
  }
}

async function runClaudePrompt(cwd, prompt, timeoutMs = 180_000) {
  // Drive one interactive turn via `claude --print`. The user's
  // subscription handles the call. The UserPromptSubmit hook fires
  // before the turn; the Stop hook fires after.
  return new Promise((res, rej) => {
    const proc = spawn("claude", ["--print", "--dangerously-skip-permissions", prompt], {
      cwd,
      env: process.env,
      stdio: ["ignore", "pipe", "pipe"],
    });
    let stdout = "";
    let stderr = "";
    let myPid = proc.pid;
    proc.stdout.on("data", (b) => (stdout += b.toString()));
    proc.stderr.on("data", (b) => (stderr += b.toString()));
    const killer = setTimeout(() => {
      try {
        proc.kill("SIGTERM");
      } catch {}
      rej(new Error(`claude --print timed out after ${timeoutMs}ms`));
    }, timeoutMs);
    proc.on("close", (code) => {
      clearTimeout(killer);
      if (code !== 0) {
        rej(new Error(`claude --print exited ${code}: ${stderr.slice(0, 500)}`));
        return;
      }
      res({ stdout, stderr, pid: myPid });
    });
  });
}

async function main() {
  const scenarioName = process.argv[2];
  if (!scenarioName) die("usage: node test/e2e/harness.mjs <scenario>");

  const scenarioPath = join(REPO_ROOT, "test", "e2e", "scenarios", `${scenarioName}.mjs`);
  if (!existsSync(scenarioPath)) die(`scenario not found: ${scenarioPath}`);

  if (!existsSync(KLYNE_DB)) {
    die(`klyne DB not found at ${KLYNE_DB}. Run \`./bin/klyne start &\` first.`);
  }

  const scenario = (await import(scenarioPath)).default;
  if (!scenario || !Array.isArray(scenario.prompts)) {
    die("scenario must export { prompts: [...], verify: (rows) => {...} }");
  }

  // Unique temp git repo per run so we can scope DB queries.
  // realpath resolves /var/folders → /private/var/folders on macOS
  // so it matches the canonical project_path the Stop hook writes.
  const workdir = realpathSync(scenario.workdir || mkdtempSync(join(tmpdir(), "klyne-e2e-work-")));
  if (!existsSync(join(workdir, ".git"))) {
    execFileSync("git", ["init", "-q"], { cwd: workdir });
    execFileSync("git", ["config", "user.email", "test@example.com"], { cwd: workdir });
    execFileSync("git", ["config", "user.name", "Test"], { cwd: workdir });
    execFileSync("git", ["commit", "--allow-empty", "-q", "-m", "init"], { cwd: workdir });
  }
  console.error(`harness: scenario=${scenarioName} workdir=${workdir}`);

  // Baseline subprocess snapshot: anything claude-like already
  // running is noise.
  const baseline = listClaudeSubprocesses();
  console.error(`harness: baseline claude procs: ${baseline.length}`);

  const ourPids = new Set();
  const unauthorizedSpawns = [];

  for (const [i, prompt] of scenario.prompts.entries()) {
    console.error(`\n── prompt ${i + 1}/${scenario.prompts.length} ──`);
    console.error(`> ${prompt.slice(0, 200)}${prompt.length > 200 ? "…" : ""}`);
    const t0 = Date.now();
    const { stdout, pid } = await runClaudePrompt(workdir, prompt);
    ourPids.add(pid);
    const dt = Date.now() - t0;
    const preview = stdout.trim().slice(0, 300).replace(/\n/g, " ⏎ ");
    console.error(`< ${dt}ms: ${preview}${stdout.length > 300 ? "…" : ""}`);

    // Look for KLYNE_SUMMARY in the visible response.
    const summaryLine = stdout
      .split("\n")
      .map((l) => l.trim())
      .find((l) => l.startsWith("KLYNE_SUMMARY:"));
    console.error(`harness: visible KLYNE_SUMMARY: ${summaryLine || "(none)"}`);

    // Snapshot subprocess list — anything new that ISN'T our pid is
    // a klyne-side recursion bug.
    const now = listClaudeSubprocesses();
    const extra = now.filter((l) => !baseline.includes(l));
    if (extra.length > 0) {
      unauthorizedSpawns.push({ at: `prompt ${i + 1}`, procs: extra });
    }
  }

  // Let Stop hook finish writing.
  await sleep(2500);

  // Pull rows for this workdir only.
  const escaped = workdir.replace(/'/g, "''");
  const rowsJson = sql(
    `SELECT session_id, ts, ai_drafted_summary, last_user, importance,
            recap_visible, draft_state
       FROM stop_summaries
      WHERE project_path = '${escaped}'
      ORDER BY ts ASC`
  );
  const rows = JSON.parse(rowsJson || "[]");
  console.error(`\nharness: collected ${rows.length} stop_summaries rows for ${workdir}`);
  for (const r of rows) {
    console.error(
      `  row: importance=${r.importance} visible=${r.recap_visible} summary=${(r.ai_drafted_summary || "").slice(0, 80)}`
    );
  }

  const report = scenario.verify(rows);
  if (unauthorizedSpawns.length > 0) {
    report.failures = report.failures || [];
    report.failures.push({ kind: "unauthorized-spawn", detail: unauthorizedSpawns });
  }
  report.workdir = workdir;

  console.error("\n── REPORT ──");
  console.log(JSON.stringify(report, null, 2));

  if (report.failures && report.failures.length > 0) {
    process.exit(1);
  }
}

main().catch((err) => {
  console.error("harness: fatal", err);
  process.exit(2);
});
