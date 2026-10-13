import { readFileSync } from "node:fs";
import { join } from "node:path";
import { runInNewContext } from "node:vm";
import { describe, expect, it } from "vitest";
import { mismatchGuard } from "./mismatch-guard.mjs";

const NOINDEX = "noindex, follow";
const INDEX = "index, follow";
const normalizePath = (p) => (p.length > 1 ? p.replace(/\/+$/, "") || "/" : p);

/** Runs the guard's script in a stand-in document and returns what it did to it. */
function run(prerendered, pathname) {
  const state = { cleared: false, robots: INDEX };
  const root = { getAttribute: () => prerendered, replaceChildren: () => (state.cleared = true) };
  const meta = { setAttribute: (name, value) => name === "content" && (state.robots = value) };
  const script = mismatchGuard(normalizePath, NOINDEX).replace(/^<script>|<\/script>$/g, "");
  runInNewContext(script, {
    document: { getElementById: () => root, querySelector: (sel) => (sel === 'meta[name="robots"]' ? meta : null) },
    location: { pathname },
  });
  return state;
}

describe("mismatchGuard", () => {
  it("TestMismatchGuard_a_page_served_for_its_own_path_is_left_alone", () => {
    expect(run("/explorer", "/explorer")).toEqual({ cleared: false, robots: INDEX });
    expect(run("/explorer", "/explorer/")).toEqual({ cleared: false, robots: INDEX });
  });

  it("TestMismatchGuard_the_explorer_landing_served_for_a_deep_link_is_cleared_and_marked_noindex", () => {
    for (const path of ["/explorer/tx/ab", "/explorer/custom/wallet/orama1x", "/explorer/stagenet/block/9"]) {
      expect(run("/explorer", path)).toEqual({ cleared: true, robots: NOINDEX });
    }
  });

  it("TestMismatchGuard_a_document_without_a_robots_tag_is_still_cleared", () => {
    const state = { cleared: false };
    const root = { getAttribute: () => "/explorer", replaceChildren: () => (state.cleared = true) };
    const script = mismatchGuard(normalizePath, NOINDEX).replace(/^<script>|<\/script>$/g, "");
    runInNewContext(script, { document: { getElementById: () => root, querySelector: () => null }, location: { pathname: "/nope" } });
    expect(state.cleared).toBe(true);
  });
});

describe("the explorer in nginx", () => {
  const conf = readFileSync(join(import.meta.dirname, "..", "deploy", "orama.network.nginx.conf"), "utf8");
  const block = (start) => {
    const at = conf.indexOf(start);
    return at === -1 ? "" : conf.slice(at, conf.indexOf("\n    }\n", at));
  };

  it("TestExplorerNginx_every_page_under_explorer_is_sent_with_a_noindex_header", () => {
    expect(block("location /explorer/ {")).toContain('add_header X-Robots-Tag "noindex" always;');
  });

  it("TestExplorerNginx_the_landing_itself_is_served_without_one", () => {
    // /explorer has no location of its own: it is the prerendered, indexed landing page.
    expect(conf).not.toMatch(/location\s+=\s+\/explorer\s*\{/);
    expect(block("location / {")).not.toContain("X-Robots-Tag");
  });
});
