import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterAll, describe, expect, it } from "vitest";
import { distFileFor } from "./dist-file.mjs";

const root = mkdtempSync(join(tmpdir(), "dist-file-"));
const dist = join(root, "dist");
mkdirSync(join(dist, "investors"), { recursive: true });
mkdirSync(join(dist, "assets", "odd.js"), { recursive: true });
writeFileSync(join(dist, "index.html"), "home");
writeFileSync(join(dist, "investors", "index.html"), "investors");
writeFileSync(join(dist, "assets", "app.js"), "js");
writeFileSync(join(dist, "a b.pdf"), "pdf");
writeFileSync(join(root, "secret.conf"), "outside dist");

describe("distFileFor", () => {
  afterAll(() => rmSync(root, { recursive: true, force: true }));

  it("TestDistFileFor_files_and_page_directories", () => {
    expect(distFileFor(dist, "/assets/app.js")).toBe(join(dist, "assets", "app.js"));
    expect(distFileFor(dist, "/investors")).toBe(join(dist, "investors", "index.html"));
    expect(distFileFor(dist, "/investors/")).toBe(join(dist, "investors", "index.html"));
    expect(distFileFor(dist, "/")).toBe(join(dist, "index.html"));
    expect(distFileFor(dist, "/a%20b.pdf")).toBe(join(dist, "a b.pdf"));
  });

  it("TestDistFileFor_missing_and_directories_named_like_files", () => {
    expect(distFileFor(dist, "/nope.js")).toBeNull();
    expect(distFileFor(dist, "/platform")).toBeNull();
    expect(distFileFor(dist, "/assets/odd.js")).toBeNull();
  });

  it("TestDistFileFor_never_leaves_dist", () => {
    expect(distFileFor(dist, "/../secret.conf")).toBeNull();
    expect(distFileFor(dist, "/..%2fsecret.conf")).toBeNull();
    expect(distFileFor(dist, "/%2e%2e%2fsecret.conf")).toBeNull();
  });

  it("TestDistFileFor_malformed_escape_is_unservable", () => {
    expect(distFileFor(dist, "/%E0%A4%A")).toBeNull();
  });
});
