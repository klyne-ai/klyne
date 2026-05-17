import { execFileSync } from "node:child_process";
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";

const KLYNE_BIN = process.env.KLYNE_BIN || "/Users/mohitpatel/.local/bin/klyne";

/**
 * Seed `.claude/settings.json` inside the project dir with klyne's Stop hook.
 *
 * Why: `claude -p` only fires Stop hooks that are registered in a settings file
 * Claude Code can see for that cwd. Klyne's regular install wires the hook in
 * the user's primary config (location varies), but ephemeral tmp dirs don't
 * inherit it. Writing a project-level settings file makes the harness
 * self-sufficient: it works as long as the `klyne` binary is on disk.
 */
function seedKlyneHook(dir) {
  const settings = {
    hooks: {
      Stop: [
        {
          hooks: [
            {
              type: "command",
              command: KLYNE_BIN + " session-end",
              timeout: 30,
            },
          ],
        },
      ],
    },
  };
  mkdirSync(path.join(dir, ".claude"), { recursive: true });
  writeFileSync(
    path.join(dir, ".claude/settings.json"),
    JSON.stringify(settings, null, 2),
  );
}

/**
 * Create an isolated tmp project dir, git-init it, and wire the klyne Stop hook.
 * Returns the absolute path (this becomes klyne `project_path`).
 */
export function makeProject(name) {
  const safe = name.replace(/[^a-z0-9-]/gi, "-");
  const dir = mkdtempSync(path.join(tmpdir(), `klyne-showcase-${safe}-`));
  execFileSync("git", ["init", "-q"], { cwd: dir });
  execFileSync("git", ["config", "user.email", "showcase@klyne.test"], { cwd: dir });
  execFileSync("git", ["config", "user.name", "showcase"], { cwd: dir });
  seedKlyneHook(dir);
  writeFileSync(
    path.join(dir, "README.md"),
    `# ${safe}\n\nThrowaway project for klyne scenario harness.\n`,
  );
  execFileSync("git", ["add", "-A"], { cwd: dir });
  execFileSync("git", ["commit", "-q", "-m", "init"], { cwd: dir });
  return dir;
}

export function removeProject(dir) {
  if (!dir || !dir.startsWith(tmpdir())) {
    throw new Error(`refusing to remove non-tmp dir: ${dir}`);
  }
  rmSync(dir, { recursive: true, force: true });
}
