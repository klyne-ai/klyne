import { writeFileSync, mkdirSync } from "node:fs";
import path from "node:path";

export const STATUS = Object.freeze({
  PASS: "PASS",
  FAIL: "FAIL",
  SKIP: "SKIP",
  ERROR: "ERROR",
});

const ICON = { PASS: "✅", FAIL: "❌", SKIP: "⏭️", ERROR: "💥" };

export function renderScenarioSection(result) {
  const { name, status, summary, steps, elapsedMs, costUsd, evidence } = result;
  const lines = [];
  lines.push(`## ${ICON[status] || "·"} ${name} — ${status}`);
  lines.push("");
  if (summary) {
    lines.push(summary);
    lines.push("");
  }
  lines.push(`- elapsed: ${(elapsedMs / 1000).toFixed(1)}s`);
  lines.push(`- cost: $${(costUsd || 0).toFixed(4)}`);
  lines.push("");
  if (steps && steps.length > 0) {
    lines.push("### Steps");
    lines.push("");
    for (const s of steps) {
      lines.push(`- ${ICON[s.status] || "·"} **${s.name}** — ${s.status}`);
      if (s.detail) lines.push(`  - ${s.detail}`);
    }
    lines.push("");
  }
  if (evidence) {
    lines.push("### Evidence");
    lines.push("");
    for (const [label, body] of Object.entries(evidence)) {
      lines.push(`<details><summary>${label}</summary>`);
      lines.push("");
      lines.push("```");
      lines.push(typeof body === "string" ? body : JSON.stringify(body, null, 2));
      lines.push("```");
      lines.push("");
      lines.push(`</details>`);
      lines.push("");
    }
  }
  return lines.join("\n");
}

export function renderReport({ results, startedAt, finishedAt }) {
  const pass = results.filter((r) => r.status === STATUS.PASS).length;
  const fail = results.filter((r) => r.status === STATUS.FAIL).length;
  const err = results.filter((r) => r.status === STATUS.ERROR).length;
  const skip = results.filter((r) => r.status === STATUS.SKIP).length;
  const totalCost = results.reduce((s, r) => s + (r.costUsd || 0), 0);
  const totalElapsed = finishedAt - startedAt;

  const head = [];
  head.push(`# klyne scenario harness report`);
  head.push("");
  head.push(`- run: ${new Date(startedAt).toISOString()}`);
  head.push(`- duration: ${(totalElapsed / 1000).toFixed(1)}s`);
  head.push(`- total cost: $${totalCost.toFixed(4)}`);
  head.push("");
  head.push(`## Summary`);
  head.push("");
  head.push(`| Status | Count |`);
  head.push(`|--------|-------|`);
  head.push(`| ✅ PASS | ${pass} |`);
  head.push(`| ❌ FAIL | ${fail} |`);
  head.push(`| 💥 ERROR | ${err} |`);
  head.push(`| ⏭️ SKIP | ${skip} |`);
  head.push("");
  head.push(`| Scenario | Status | Cost |`);
  head.push(`|----------|--------|------|`);
  for (const r of results) {
    head.push(`| ${r.name} | ${ICON[r.status]} ${r.status} | $${(r.costUsd || 0).toFixed(4)} |`);
  }
  head.push("");
  head.push("---");
  head.push("");

  const body = results.map(renderScenarioSection).join("\n---\n\n");
  return head.join("\n") + body;
}

export function writeReport(report, reportsDir) {
  mkdirSync(reportsDir, { recursive: true });
  const ts = new Date().toISOString().replace(/[:.]/g, "-");
  const file = path.join(reportsDir, `${ts}.md`);
  writeFileSync(file, report);
  return file;
}
