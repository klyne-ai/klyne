// Scenario 06 — Runbook auto-recall via MCP server Instructions.
//
// This is the end-to-end smoke for the feature shipped in commits
// 6e75a9b / 7287d50 / b2403f7 / ff073ca: the klyne MCP server now
// surfaces a directive + titled runbook inventory via ServerOptions.
// Instructions, so the model should auto-call mcp__klyne__recall when
// it sees a topical match — no user prompting needed.
//
// Plan:
//   1. Make a tempdir project (standard harness setup seeds .mcp.json
//      pointing at the installed klyne binary).
//   2. Seed ONE project-scoped runbook via `klyne decisions add` whose
//      BODY (not title) contains a unique secret token the user prompt
//      never mentions. The instructions-inventory only exposes the
//      first-line title — the secret is only reachable via recall.
//   3. Run one fresh `claude -p` turn asking a topically-matching
//      question that contains zero overlap with the secret.
//   4. PASS iff the response text contains the secret token. That
//      proves the model called mcp__klyne__recall autonomously and
//      applied the runbook body content.
//
// If this test passes, the auto-recall promise of the feature
// ("user adds a runbook once, the model picks it up forever") holds.

import { execFileSync } from "node:child_process";
import { runClaude } from "../lib/claude.js";
import { makeProject, removeProject } from "../lib/tmpdir.js";
import { cleanupProject, nowMs, getDecisionsForProject } from "../lib/klyne.js";
import { STATUS } from "../lib/report.js";

export const name = "06-runbook-auto-recall";

const KLYNE_BIN = process.env.KLYNE_BIN || "/Users/mohitpatel/.local/bin/klyne";

// Secret token the model can ONLY know by recalling the runbook body.
// Distinctive enough not to appear in any model's training data.
const SECRET = "QUIRKWORD-93XQ";

// Title (first non-empty line) — what the instructions-inventory will
// expose to the model at session boot. It must be topically clear so
// the model recognises a match against the prompt.
const RUNBOOK_TITLE = "Labstack lab-orders webhook 404 — troubleshooting";

// Full runbook body — title on first line, then the steps. The SECRET
// lives in step 3, never in the title.
const RUNBOOK_BODY = [
  RUNBOOK_TITLE,
  "",
  'When you see "Cannot POST /v1/webhooks/labstack/lab-orders" in dev:',
  "  1. The webhook lives in consultation-service, NOT oms-service.",
  "  2. Branch feat/labstack-integration must be deployed to dev.",
  `  3. Confirmation token after applying the fix: ${SECRET}.`,
].join("\n");

// Question that topically matches the title but contains zero overlap
// with the secret or the runbook's specific advice.
const PROMPT =
  "I'm hitting 'Cannot POST /v1/webhooks/labstack/lab-orders' in dev — what should I do?";

export async function run({ keepTmp = false, noClaude = false } = {}) {
  const steps = [];
  const evidence = {};
  let costUsd = 0;

  if (noClaude) {
    return {
      status: STATUS.SKIP,
      summary:
        "Dry-run: would seed a runbook via `klyne decisions add` then ask a topically-matching question and assert the secret token appears in the response.",
      steps,
      evidence,
      costUsd,
    };
  }

  const dir = makeProject("autorecall");
  const t0 = nowMs();

  try {
    // --- 1. Seed the runbook via the CLI (deterministic, no Claude cost) ---
    execFileSync(
      KLYNE_BIN,
      ["decisions", "add", RUNBOOK_BODY, "--project", dir, "--tags", "runbook"],
      { stdio: ["ignore", "ignore", "pipe"] },
    );

    const seeded = getDecisionsForProject(dir);
    evidence["seed.decisionsCount"] = seeded.length;
    evidence["seed.titleFirstLine"] = (seeded[0]?.text ?? "").split("\n")[0];

    if (seeded.length !== 1) {
      steps.push({
        name: "seed: exactly one runbook stored for project",
        status: STATUS.FAIL,
        detail: `Expected 1 decision, got ${seeded.length}.`,
      });
      return finish(steps, evidence, costUsd, dir, keepTmp);
    }
    if (!seeded[0].text.includes(SECRET)) {
      steps.push({
        name: "seed: stored runbook body contains the secret",
        status: STATUS.FAIL,
        detail: "Secret missing from stored decision text — seed failed.",
      });
      return finish(steps, evidence, costUsd, dir, keepTmp);
    }
    steps.push({
      name: "seed: runbook persisted with secret in body, title-only in first line",
      status: STATUS.PASS,
    });

    // --- 2. Ask the topically-matching question ---
    // A fresh `claude -p` spawns a new MCP subprocess, which runs
    // mcpserver.New() → buildServerInstructions() → instructions.Build
    // against the project dir. The model should see the directive +
    // inventory in its initialize response and auto-call recall.
    const r = await runClaude({
      cwd: dir,
      prompt: PROMPT,
      timeoutMs: 180_000,
    });
    costUsd += r.costUsd;

    evidence["claude.exitCode"] = r.exitCode;
    evidence["claude.sessionId"] = r.sessionId ?? "(none)";
    evidence["claude.costUsd"] = r.costUsd.toFixed(4);
    evidence["claude.responseExcerpt"] =
      (r.text || "").slice(0, 1500) || "(empty)";
    if (r.stderr) evidence["claude.stderr"] = r.stderr.slice(0, 600);

    if (r.exitCode !== 0) {
      steps.push({
        name: "claude turn: exited cleanly",
        status: STATUS.FAIL,
        detail: `Exit code ${r.exitCode}. See evidence.claude.stderr.`,
      });
      return finish(steps, evidence, costUsd, dir, keepTmp);
    }

    // --- 3. The core assertion: secret token in the response ---
    const responseHasSecret = (r.text || "").includes(SECRET);
    const responseMentionsConsultationService =
      (r.text || "").toLowerCase().includes("consultation-service");

    steps.push({
      name: "PRIMARY: response contains the secret token (proves auto-recall)",
      status: responseHasSecret ? STATUS.PASS : STATUS.FAIL,
      detail: responseHasSecret
        ? `Response includes "${SECRET}" — the model called mcp__klyne__recall autonomously and surfaced runbook body content.`
        : `Response does NOT contain "${SECRET}". Either the model never called recall, or recall returned no rows, or the model called recall but discarded the body. See evidence.claude.responseExcerpt.`,
    });

    steps.push({
      name: "SECONDARY: response mentions consultation-service (also runbook-only)",
      status: responseMentionsConsultationService ? STATUS.PASS : STATUS.SKIP,
      detail: responseMentionsConsultationService
        ? "Response includes 'consultation-service' (the runbook's key directive)."
        : "Response did not mention 'consultation-service'. Not a hard failure — the secret-token check is the load-bearing one.",
    });

    return finish(steps, evidence, costUsd, dir, keepTmp);
  } catch (err) {
    steps.push({
      name: "scenario: unexpected exception",
      status: STATUS.FAIL,
      detail: err instanceof Error ? `${err.message}\n${err.stack}` : String(err),
    });
    return finish(steps, evidence, costUsd, dir, keepTmp);
  }
}

function finish(steps, evidence, costUsd, dir, keepTmp) {
  if (!keepTmp) {
    cleanupProject(dir);
    removeProject(dir);
  } else {
    evidence["tmp.dir"] = dir;
  }
  const failed = steps.some((s) => s.status === STATUS.FAIL);
  return {
    status: failed ? STATUS.FAIL : STATUS.PASS,
    summary: failed
      ? "Runbook auto-recall did NOT fire end-to-end (see steps + evidence)."
      : "Runbook auto-recall verified: the model picked up the seeded runbook autonomously via MCP server Instructions.",
    steps,
    evidence,
    costUsd,
  };
}
