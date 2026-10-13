import { readFileSync, readdirSync } from "node:fs";
import path from "node:path";
import { build } from "vite";
import type { Rollup } from "vite";
import react from "@vitejs/plugin-react";
import { describe, expect, it } from "vitest";
import { createChainSource } from "../data/chain/source";
import { FakeStream, addr } from "../data/chain/fake-gateway";
import { devAnswer } from "./dev-gateway";
import { DEV_FAILURE_BODY, DEV_GATEWAY_ENV, explorerDevGatewayPlugin, failed } from "./vite-plugin";

const SITE_ROOT = path.resolve(import.meta.dirname, "../../..");
const DEV_DIR = import.meta.dirname;
const CHAIN_BASE = "http://localhost:5173/v1/chain";

/** A chain source over the dev gateway's answers, as the browser would read them over HTTP. */
function devSource() {
  const fetcher = async (input: string) => {
    const answer = await devAnswer(input.slice(`${CHAIN_BASE}/`.length));
    return new Response(JSON.stringify(answer.body), { status: answer.status, headers: { "Content-Type": "application/json" } });
  };
  return createChainSource({ fetcher, base: CHAIN_BASE, eventSource: new FakeStream().factory });
}

describe("devAnswer", () => {
  it("TestDevAnswer_answers_the_chain_status_and_the_home_snapshot", async () => {
    expect((await devAnswer("status")).status).toBe(200);
    const home = (await devAnswer("index/home")).body as { blocks: unknown[]; txs: unknown[] };
    expect(home.blocks.length).toBeGreaterThan(0);
    expect(home.txs.length).toBeGreaterThan(0);
  });

  it("TestDevAnswer_a_path_it_does_not_know_is_a_404_like_the_gateways", async () => {
    expect((await devAnswer("index/nothing-here")).status).toBe(404);
  });

  it("TestDevAnswer_an_epoch_that_is_not_closed_is_a_404_not_an_empty_row", async () => {
    expect((await devAnswer("index/epochs/11")).status).toBe(200);
    expect((await devAnswer("index/epochs/12")).status).toBe(404);
    expect((await devAnswer("index/epochs/0")).status).toBe(404);
  });

  it("TestDevAnswer_a_block_at_any_height_has_a_header", async () => {
    const answer = (await devAnswer("block?height=250")).body as { result: { block: { header: { height: string } } } };
    expect(answer.result.block.header.height).toBe("250");
  });
});

describe("the dev chain parses with the explorer's own readers", () => {
  // If the explorer's readers change shape, the dev chain must change with them: a page that cannot read it
  // would show an error and hide the page's real states from whoever is looking at them.
  it("TestDevChain_every_page_of_the_explorer_finds_what_it_reads", async () => {
    const source = devSource();
    const wallet = addr(1);
    const results = await Promise.all([
      source.getNetwork(),
      source.getLatestActivity("all", 10),
      source.getRecentBlocks(24),
      source.getValidators(),
      source.getEpochs({ page: 1, limit: 20 }),
      source.getEpoch(11),
      source.getEpochValidators(11, { page: 1, limit: 20 }),
      source.getEpochRewards(11, { page: 1, limit: 25 }),
      source.getSupplyHistory(100),
      source.getWallet(wallet),
      source.getNetworkNodes(),
      source.getNode("node-1"),
      source.getDeals(),
      source.getTorRelays(),
      source.getArchiveHealth(),
      source.getSettlementQueue(),
    ]);
    expect(results.every((r) => r !== null && r !== undefined)).toBe(true);
    expect((await source.getEpoch(12))).toBeNull();
  });
});

describe("a read that fails inside the dev gateway", () => {
  function fakeResponse() {
    const sent = { status: 0, body: "", headers: {} as Record<string, string> };
    const res = {
      statusCode: 0,
      setHeader: (name: string, value: string) => void (sent.headers[name] = value),
      end: (body: string) => {
        sent.body = body;
        sent.status = res.statusCode;
      },
    };
    return { res: res as never, sent };
  }

  it("TestDevFailure_the_client_gets_a_fixed_body_and_the_error_goes_to_the_console", () => {
    const { res, sent } = fakeResponse();
    const logged: string[] = [];
    failed(res, new Error("open /Users/someone/secret/path: no such file"), (line) => logged.push(line));
    expect(sent.status).toBe(500);
    expect(JSON.parse(sent.body)).toEqual(DEV_FAILURE_BODY);
    expect(sent.body).not.toContain("secret");
    expect(logged).toHaveLength(1);
    expect(logged[0]).toContain("explorer-dev-gateway");
    expect(logged[0]).toContain("/Users/someone/secret/path");
  });

  it("TestDevFailure_a_thrown_value_that_is_not_an_error_is_logged_too", () => {
    const { res, sent } = fakeResponse();
    const logged: string[] = [];
    failed(res, "boom", (line) => logged.push(line));
    expect(sent.status).toBe(500);
    expect(logged[0]).toContain("boom");
  });
});

describe("the dev gateway never reaches a build", () => {
  it("TestDevGatewayPlugin_is_absent_unless_the_environment_asks_for_it", () => {
    expect(explorerDevGatewayPlugin({})).toEqual([]);
    expect(explorerDevGatewayPlugin({ [DEV_GATEWAY_ENV]: "true" })).toEqual([]);
    expect(explorerDevGatewayPlugin({ [DEV_GATEWAY_ENV]: "0" })).toEqual([]);
  });

  it("TestDevGatewayPlugin_when_asked_for_applies_to_the_dev_server_only", () => {
    const [plugin, ...rest] = explorerDevGatewayPlugin({ [DEV_GATEWAY_ENV]: "1" });
    expect(rest).toEqual([]);
    expect(plugin?.apply).toBe("serve");
  });

  it("TestDevGatewayPlugin_refuses_to_start_in_production_mode", () => {
    const [plugin] = explorerDevGatewayPlugin({ [DEV_GATEWAY_ENV]: "1" });
    const configure = plugin?.configureServer as (server: unknown) => void;
    expect(() => configure({ config: { mode: "production" }, middlewares: { use: () => undefined } })).toThrow(/cannot be used in production mode/);
    expect(() => configure({ config: { mode: "development" }, middlewares: { use: () => undefined } })).not.toThrow();
  });

  it("TestDevGateway_nothing_outside_its_own_folder_imports_it", () => {
    const outside: string[] = [];
    const walk = (dir: string) => {
      for (const entry of readdirSync(dir, { withFileTypes: true })) {
        const full = path.join(dir, entry.name);
        if (entry.isDirectory()) {
          if (full !== DEV_DIR) walk(full);
        } else if (/\.(ts|tsx|mjs)$/.test(entry.name) && !entry.name.includes(".test.")) {
          if (/from\s+["'][^"']*explorer\/dev\/|import\(\s*["'][^"']*explorer\/dev\//.test(readFileSync(full, "utf8"))) outside.push(full);
        }
      }
    };
    walk(path.join(SITE_ROOT, "src"));
    expect(outside).toEqual([]);
    // The one place that names it is the build configuration, which loads the plugin and only the plugin.
    const config = readFileSync(path.join(SITE_ROOT, "vite.config.ts"), "utf8");
    expect(config.match(/explorer\/dev\/[a-z-]+/g)).toEqual(["explorer/dev/vite-plugin"]);
  });

  it("TestProductionBundle_of_the_explorer_holds_nothing_of_the_dev_gateway", async () => {
    // Even with the variable set for the process: a build applies no `serve` plugin, and nothing imports src/explorer/dev.
    const previous = process.env[DEV_GATEWAY_ENV];
    process.env[DEV_GATEWAY_ENV] = "1";
    try {
      const result = (await build({
        root: SITE_ROOT,
        configFile: false,
        mode: "production",
        logLevel: "silent",
        plugins: [react({ include: /\.(jsx|tsx)$/ }), ...explorerDevGatewayPlugin()],
        build: { write: false, minify: false, rollupOptions: { input: path.join(SITE_ROOT, "src/explorer/explorer-app.tsx") } },
      })) as Rollup.RollupOutput | Rollup.RollupOutput[];
      const outputs = (Array.isArray(result) ? result : [result]).flatMap((r) => r.output);
      const chunks = outputs.filter((o): o is Rollup.OutputChunk => o.type === "chunk");
      expect(chunks.length).toBeGreaterThan(0);
      const modules = chunks.flatMap((c) => Object.keys(c.modules));
      expect(modules.some((id) => id.includes("/src/explorer/data/chain/source.ts"))).toBe(true);
      expect(modules.filter((id) => id.includes("/src/explorer/dev/"))).toEqual([]);
      // fake-gateway.ts, the fixtures the dev chain is made of, are tests' and the dev server's alone.
      expect(modules.filter((id) => /fake-gateway|fake-network/.test(id))).toEqual([]);
      for (const chunk of chunks) {
        for (const marker of [DEV_GATEWAY_ENV, "explorer-dev-gateway", "devAnswer", "an invented chain"]) {
          expect(chunk.code, `${chunk.fileName} contains ${marker}`).not.toContain(marker);
        }
      }
    } finally {
      if (previous === undefined) delete process.env[DEV_GATEWAY_ENV];
      else process.env[DEV_GATEWAY_ENV] = previous;
    }
  }, 120_000);
});
