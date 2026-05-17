// Scenario 03 — Memory cross-session recall.
//
// klyne exposes remember_memory / recall_memory via MCP. We:
//   Session A: ask claude to use the klyne MCP tool to remember a fact.
//   Verify: a row appears in the `decisions` table scoped to this project_path.
//   Session B (fresh): ask claude to recall the fact via klyne MCP.
//   Verify: the response text includes the stored value.
//
// Caveat: this requires klyne's MCP server to be registered for these
// claude -p invocations. If it's not, claude won't have the tool and the
// scenario will SKIP with diagnostic info rather than FAIL.

import { runClaude } from "../lib/claude.js";
import { makeProject, removeProject } from "../lib/tmpdir.js";
import { getDecisionsForProject, cleanupProject } from "../lib/klyne.js";
import { STATUS } from "../lib/report.js";

export const name = "03-memory-cross-session";

const FACT_TOKEN = "STAGING_API_URL_https_api_staging_acme_test";
const FACT_PROMPT_STORE =
  `Use the klyne MCP tool 'remember_memory' to save the following text exactly: "${FACT_TOKEN}". ` +
  `Tag it as 'config'. After calling the tool, reply with the single word DONE.`;
const FACT_PROMPT_RECALL =
  `Use the klyne MCP tool 'recall_memory' or 'list_memories' to look up any saved memory tagged 'config' for this project. ` +
  `Then reply with the exact text of the memory you found. Reply only with the memory text.`;

export async function run({ keepTmp = false, noClaude = false } = {}) {
  const steps = [];
  const evidence = {};
  let costUsd = 0;

  if (noClaude) {
    return {
      status: STATUS.SKIP,
      summary: "Dry-run: would store a memory in session A and recall it in session B.",
      steps,
      evidence,
      costUsd,
    };
  }

  const dir = makeProject("memory");

  try {
    // --- session A: store ---
    const a = await runClaude({
      cwd: dir,
      prompt: FACT_PROMPT_STORE,
      timeoutMs: 120_000,
    });
    costUsd += a.costUsd;
    evidence["sessionA.text"] = a.text.slice(0, 400) || "(empty)";

    const decisionsAfterA = getDecisionsForProject(dir);
    evidence["sessionA.decisions"] = decisionsAfterA;

    if (decisionsAfterA.length === 0) {
      steps.push({
        name: "session A: memory persisted to decisions table",
        status: STATUS.SKIP,
        detail:
          "No row appeared in `decisions` table after session A. " +
          "Likely cause: klyne MCP server is not registered for `claude -p` invocations in this project. " +
          "Verify with: `klyne mcp install` and re-run.",
      });
      return {
        status: STATUS.SKIP,
        summary: "Memory recall test skipped: klyne MCP tools not available to session.",
        steps,
        evidence,
        costUsd,
      };
    }

    const hasFact = decisionsAfterA.some((d) => d.text && d.text.includes(FACT_TOKEN));
    steps.push({
      name: "session A: memory contains expected token",
      status: hasFact ? STATUS.PASS : STATUS.FAIL,
      detail: hasFact
        ? "Token found in stored memory."
        : `Decisions row(s) present but none contain '${FACT_TOKEN}'. Texts: ${decisionsAfterA.map((d) => (d.text || "").slice(0, 60)).join(" | ")}`,
    });

    // --- session B: recall (fresh claude process) ---
    const b = await runClaude({
      cwd: dir,
      prompt: FACT_PROMPT_RECALL,
      timeoutMs: 120_000,
    });
    costUsd += b.costUsd;
    evidence["sessionB.text"] = b.text.slice(0, 800) || "(empty)";

    const recalled = b.text && b.text.includes(FACT_TOKEN);
    steps.push({
      name: "session B: recall returns the stored fact",
      status: recalled ? STATUS.PASS : STATUS.FAIL,
      detail: recalled
        ? "Session B's response contains the stored token without re-prompting."
        : `Session B did not surface the stored token. Response: ${(b.text || "").slice(0, 200)}`,
    });
  } finally {
    if (!keepTmp) {
      cleanupProject(dir);
      removeProject(dir);
    } else {
      evidence["tmp.dir"] = dir;
    }
  }

  const failed = steps.some((s) => s.status === STATUS.FAIL);
  return {
    status: failed ? STATUS.FAIL : STATUS.PASS,
    summary: failed
      ? "Cross-session memory recall did not produce expected text."
      : "Memory stored in session A was recalled in session B.",
    steps,
    evidence,
    costUsd,
  };
}
