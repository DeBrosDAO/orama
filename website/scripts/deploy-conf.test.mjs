import { readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

const dir = join(import.meta.dirname, "..", "deploy");
const sites = readdirSync(dir).filter((f) => f.endsWith(".nginx.conf"));

describe("deploy/*.nginx.conf", () => {
  it("TestDeployConf_has_the_site_files", () => {
    expect(sites.length).toBeGreaterThanOrEqual(3);
  });

  for (const file of sites) {
    const host = file.replace(/\.nginx\.conf$/, "");
    it(`TestDeployConf_${host}_header_names_its_own_install_path`, () => {
      const header = readFileSync(join(dir, file), "utf8").split("\n").filter((l) => l.startsWith("#")).join("\n");
      const paths = [...header.matchAll(/sites-available\/([A-Za-z0-9.-]+)/g)].map((m) => m[1]);
      expect(paths.length).toBeGreaterThan(0);
      // Installing one site over another's file takes the other site down.
      expect(new Set(paths)).toEqual(new Set([host]));
    });
  }
});
