import { execFileSync, spawnSync } from "node:child_process";
import { mkdtempSync, readFileSync, readdirSync, rmSync, statSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { describe, expect, it } from "vitest";

/**
 * src/chain/gen is generated from chain/proto. When protoc and go are here, a
 * proto change that was not followed by `pnpm gen:chain` fails this test. On a
 * machine without them it skips, and CI's job with the toolchain runs it.
 */
const root = resolve(__dirname, "../../..");
const committed = join(root, "src/chain/gen");

const has = (cmd: string, arg: string) => spawnSync(cmd, [arg]).status === 0;
const toolchain = has("protoc", "--version") && has("go", "version");

function files(dir: string, prefix = ""): Map<string, string> {
  const out = new Map<string, string>();
  for (const name of readdirSync(dir)) {
    const path = join(dir, name);
    if (statSync(path).isDirectory()) {
      for (const [k, v] of files(path, `${prefix}${name}/`)) out.set(k, v);
    } else {
      out.set(`${prefix}${name}`, readFileSync(path, "utf8"));
    }
  }
  return out;
}

describe("generated protobuf code", () => {
  it.skipIf(!toolchain)("matches what gen-chain-proto.sh produces from chain/proto", () => {
    const out = mkdtempSync(join(tmpdir(), "orama-gen-"));
    try {
      execFileSync("bash", [join(root, "scripts/gen-chain-proto.sh")], { env: { ...process.env, OUT_DIR: out }, stdio: "pipe" });
      const fresh = files(out);
      const have = files(committed);
      expect([...have.keys()].sort()).toEqual([...fresh.keys()].sort());
      for (const [name, text] of fresh) {
        expect(have.get(name), `${name} is stale: run pnpm gen:chain`).toBe(text);
      }
    } finally {
      rmSync(out, { recursive: true, force: true });
    }
  }, 120_000);
});
