import { execFileSync } from "node:child_process";
import { mkdtempSync, mkdirSync, realpathSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";

const KLYNE_BIN = process.env.KLYNE_BIN || "/Users/mohitpatel/.local/bin/klyne";

/**
 * Seed two config files inside the project dir:
 *   .claude/settings.json — registers klyne's Stop hook (per-turn worklog write)
 *   .claude/mcp.json      — registers klyne as an MCP server so /klyne:reflect,
 *                            /klyne:runbooks, remember_memory, recall_memory etc.
 *                            actually resolve to live tools
 *
 * Why both: in `claude -p`, hooks fire from settings but MCP servers must be
 * registered via --mcp-config OR a project-level mcp.json that Claude auto-loads.
 * Without the MCP config, `klyne MCP` tools silently aren't available (which is
 * the exact failure mode scenarios 03/04/05 hit in the first full sweep).
 */
function seedKlyneConfig(dir) {
  const settings = {
    hooks: {
      Stop: [
        {
          hooks: [
            { type: "command", command: KLYNE_BIN + " session-end", timeout: 30 },
          ],
        },
      ],
    },
  };
  const mcp = {
    mcpServers: {
      klyne: {
        command: KLYNE_BIN,
        args: ["mcp"],
      },
    },
  };
  mkdirSync(path.join(dir, ".claude"), { recursive: true });
  writeFileSync(
    path.join(dir, ".claude/settings.json"),
    JSON.stringify(settings, null, 2),
  );
  writeFileSync(
    path.join(dir, ".mcp.json"),
    JSON.stringify(mcp, null, 2),
  );
}

/**
 * Create an isolated tmp project dir, git-init it, and wire the klyne Stop hook.
 * Returns the absolute path (this becomes klyne `project_path`).
 */
export function makeProject(name) {
  const safe = name.replace(/[^a-z0-9-]/gi, "-");
  const rawDir = mkdtempSync(path.join(tmpdir(), `klyne-showcase-${safe}-`));
  // On macOS, mkdtempSync returns `/var/folders/...` but klyne (via Claude
  // Code) stores the canonical `/private/var/folders/...` as project_path.
  // We must use the canonical form everywhere or our SQL queries miss the row.
  const dir = realpathSync(rawDir);
  execFileSync("git", ["init", "-q"], { cwd: dir });
  execFileSync("git", ["config", "user.email", "showcase@klyne.test"], { cwd: dir });
  execFileSync("git", ["config", "user.name", "showcase"], { cwd: dir });
  seedKlyneConfig(dir);
  writeFileSync(
    path.join(dir, "README.md"),
    `# ${safe}\n\nThrowaway project for klyne scenario harness.\n`,
  );
  execFileSync("git", ["add", "-A"], { cwd: dir });
  execFileSync("git", ["commit", "-q", "-m", "init"], { cwd: dir });
  return dir;
}

export function removeProject(dir) {
  // Allow both `/var/folders/...` and the canonical `/private/var/folders/...`.
  const canonicalTmp = realpathSync(tmpdir());
  if (!dir || (!dir.startsWith(tmpdir()) && !dir.startsWith(canonicalTmp))) {
    throw new Error(`refusing to remove non-tmp dir: ${dir}`);
  }
  rmSync(dir, { recursive: true, force: true });
}
