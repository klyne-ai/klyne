#!/usr/bin/env node
// klyne scenario harness runner.
// Discovers scenarios in ./scenarios/, runs each, aggregates results to a
// markdown report and exits 0/1.

import { readdirSync } from "node:fs";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { renderReport, writeReport, STATUS } from "./lib/report.js";

const __dirname = path.dirname(fileURLToPath(import.meta.url));

function parseArgs(argv) {
  const opts = { smoke: false, only: null, keepTmp: false, noClaude: false };
  for (const a of argv.slice(2)) {
    if (a === "--smoke") opts.smoke = true;
    else if (a === "--keep-tmp") opts.keepTmp = true;
    else if (a === "--no-claude") opts.noClaude = true;
    else if (a.startsWith("--only=")) opts.only = a.slice("--only=".length);
    else if (a === "--help" || a === "-h") {
      console.log(`Usage: node runner.js [--smoke] [--only=<glob>] [--keep-tmp] [--no-claude]`);
      process.exit(0);
    } else {
      console.error(`unknown flag: ${a}`);
      process.exit(2);
    }
  }
  return opts;
}

function globMatch(name, pattern) {
  if (!pattern) return true;
  const re = new RegExp(
    "^" + pattern.replace(/[.+^${}()|[\]\\]/g, "\\$&").replace(/\*/g, ".*") + "$",
  );
  return re.test(name);
}

async function loadScenarios(opts) {
  const dir = path.join(__dirname, "scenarios");
  const files = readdirSync(dir)
    .filter((f) => f.endsWith(".js"))
    .sort();
  const loaded = [];
  for (const f of files) {
    if (!globMatch(f, opts.only)) continue;
    if (opts.smoke && !f.startsWith("01-")) continue;
    const mod = await import(pathToFileURL(path.join(dir, f)).href);
    if (!mod.run) {
      console.warn(`skipping ${f} — no exported run() function`);
      continue;
    }
    loaded.push({ name: mod.name || f.replace(/\.js$/, ""), run: mod.run, file: f });
  }
  return loaded;
}

async function main() {
  const opts = parseArgs(process.argv);
  const scenarios = await loadScenarios(opts);

  if (scenarios.length === 0) {
    console.error("no scenarios matched");
    process.exit(2);
  }

  console.log(`klyne scenario harness — running ${scenarios.length} scenario(s)`);
  if (opts.noClaude) console.log(`  [dry-run mode: no claude -p invocations]`);
  console.log("");

  const startedAt = Date.now();
  const results = [];
  for (const s of scenarios) {
    process.stdout.write(`▶ ${s.name} ... `);
    const t0 = Date.now();
    try {
      const r = await s.run({ keepTmp: opts.keepTmp, noClaude: opts.noClaude });
      results.push({
        name: s.name,
        status: r.status,
        summary: r.summary,
        steps: r.steps || [],
        evidence: r.evidence || {},
        costUsd: r.costUsd || 0,
        elapsedMs: Date.now() - t0,
      });
      console.log(r.status);
    } catch (err) {
      console.log("ERROR");
      results.push({
        name: s.name,
        status: STATUS.ERROR,
        summary: `Unhandled error: ${err.message}`,
        steps: [],
        evidence: { stack: err.stack || String(err) },
        costUsd: 0,
        elapsedMs: Date.now() - t0,
      });
    }
  }
  const finishedAt = Date.now();

  const reportMd = renderReport({ results, startedAt, finishedAt });
  const reportPath = writeReport(reportMd, path.join(__dirname, "reports"));

  console.log("");
  console.log("─".repeat(60));
  const pass = results.filter((r) => r.status === STATUS.PASS).length;
  const fail = results.filter((r) => r.status !== STATUS.PASS).length;
  const totalCost = results.reduce((s, r) => s + (r.costUsd || 0), 0);
  console.log(`pass: ${pass}   fail/skip/error: ${fail}   cost: $${totalCost.toFixed(4)}`);
  console.log(`report: ${reportPath}`);

  process.exit(fail > 0 ? 1 : 0);
}

main().catch((err) => {
  console.error("fatal:", err);
  process.exit(2);
});
