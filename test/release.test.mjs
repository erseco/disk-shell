import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

const workflow = readFileSync(new URL("../.github/workflows/release.yml", import.meta.url), "utf8");
const validation = workflow.split("      - name: Validate release tag\n")[1]
  .split("        run: |\n")[1].split("\n      - name:")[0]
  .replace(/^          /gm, "");

test("fork beta tags select package revisions and reject stable or invalid tags", () => {
  const directory = mkdtempSync(join(tmpdir(), "diskshell-release-"));
  const version = JSON.parse(readFileSync(new URL("../package.json", import.meta.url))).version;
  try {
    for (const [tag, revision] of [[`v${version}-beta.16`, "16"], [`v${version}-beta.14`, "14"],
      [`v${version}`, null], [`v${version}-beta.0`, null],
      [`v${version}-rc.1`, null], ["v0.0.0-beta.16", null]]) {
      const envPath = join(directory, tag);
      const result = spawnSync("bash", ["-c", validation], {
        cwd: new URL("..", import.meta.url),
        env: { ...process.env, GITHUB_REF_NAME: tag, GITHUB_ENV: envPath, GITHUB_OUTPUT: envPath + ".output" },
        encoding: "utf8",
      });
      assert.equal(result.status === 0, revision !== null, `${tag}: ${result.stderr}`);
      if (revision !== null) {
        assert.ok(readFileSync(envPath, "utf8").includes(`DISKSHELL_PACKAGE_REVISION=${revision}\n`));
        assert.equal(readFileSync(envPath + ".output", "utf8"), `spk-path=build/DiskShell-${version}-${revision}.spk\n`);
      }
    }
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});
