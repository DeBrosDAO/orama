import { describe, expect, it } from "vitest";
import { explorerPathsFor, parseExplorerPath } from "./routes";
import { crumbLabel, isTrailPath, nextTrail, shareUrl } from "./trail";

describe("trails across networks", () => {
  it("TestNextTrail_another_networks_home_clears_the_trail_too", () => {
    expect(nextTrail(["/explorer/testnet/tx/AB"], "/explorer/testnet")).toEqual([]);
  });

  it("TestCrumbLabel_reads_the_page_after_the_network_segment", () => {
    expect(crumbLabel("/explorer/testnet/block/42")).toBe("Block 42");
    expect(crumbLabel("/explorer/testnet/validators")).toBe("Validators");
    expect(crumbLabel("/explorer/validator/orama1abcdefghijklmnop")).toMatch(/^Validator /);
    expect(crumbLabel("/explorer/custom")).toBe("Explorer");
    expect(crumbLabel("/explorer/nodes")).toBe("Nodes");
    expect(crumbLabel("/explorer/node/node-1")).toBe("Node node-1");
    expect(crumbLabel("/explorer/node/a%3Ab")).toBe("Node a:b");
    expect(crumbLabel("/explorer/node/%E0%A4%A")).toBe("Node %E0%A4%A");
    expect(crumbLabel("/explorer/storage")).toBe("Storage");
    expect(crumbLabel("/explorer/tor")).toBe("Tor");
  });

  it("TestIsTrailPath_accepts_a_network_segment_and_nothing_that_climbs_out", () => {
    expect(isTrailPath("/explorer/testnet/tx/AB12")).toBe(true);
    expect(isTrailPath("/explorer/testnet/validators")).toBe(true);
    expect(isTrailPath("/explorer/validator/orama1abc")).toBe(true);
    expect(isTrailPath("/explorer/nodes")).toBe(true);
    expect(isTrailPath("/explorer/testnet/storage")).toBe(true);
    expect(isTrailPath("/explorer/tor")).toBe(true);
    expect(isTrailPath("/explorer/node/node-1")).toBe(true);
    expect(isTrailPath("/explorer/node/a%3Ab")).toBe(true);
    expect(isTrailPath("/explorer/node/../validators")).toBe(false);
    expect(isTrailPath("/explorer/node/x/y")).toBe(false);
    expect(isTrailPath("/explorer/Test/tx/AB12")).toBe(false);
    expect(isTrailPath("/explorer/a/b/tx/AB12")).toBe(false);
    expect(isTrailPath("https://evil.example/explorer/tx/AB12")).toBe(false);
  });

  it("TestShareUrl_carries_the_gateway_and_chain_beside_the_trail", () => {
    const query = new URLSearchParams({ gateway: "https://g.example.org", chain: "orama-x-1" });
    const url = new URL(shareUrl("https://orama.network", "/explorer/custom/block/3", ["/explorer/custom/block/3"], query));
    expect(url.searchParams.get("gateway")).toBe("https://g.example.org");
    expect(url.searchParams.get("chain")).toBe("orama-x-1");
    expect(url.searchParams.get("trail")).toBe("/explorer/custom/block/3");
    expect(shareUrl("https://orama.network", "/explorer", [], query)).toBe(
      "https://orama.network/explorer?gateway=https%3A%2F%2Fg.example.org&chain=orama-x-1",
    );
  });
});

describe("parseExplorerPath", () => {
  it("TestParseExplorerPath_splits_network_page_and_id", () => {
    expect(parseExplorerPath("/explorer")).toEqual({ network: null, kind: "home", id: "" });
    expect(parseExplorerPath("/explorer/tx/AB")).toEqual({ network: null, kind: "tx", id: "AB" });
    expect(parseExplorerPath("/explorer/testnet/wallet/orama1x")).toEqual({ network: "testnet", kind: "wallet", id: "orama1x" });
    expect(parseExplorerPath("/explorer/testnet")).toEqual({ network: "testnet", kind: "home", id: "" });
    expect(parseExplorerPath("/explorer/testnet/nope")).toEqual({ network: "testnet", kind: "other", id: "" });
    expect(parseExplorerPath("/explorer/node/node-1")).toEqual({ network: null, kind: "node", id: "node-1" });
    expect(parseExplorerPath("/explorer/testnet/storage")).toEqual({ network: "testnet", kind: "storage", id: "" });
  });

  it("TestExplorerPathsFor_the_network_pages_are_per_network_and_a_node_id_is_encoded", () => {
    const p = explorerPathsFor("testnet", "?x=1");
    expect([p.nodes, p.storage, p.tor]).toEqual(["/explorer/testnet/nodes?x=1", "/explorer/testnet/storage?x=1", "/explorer/testnet/tor?x=1"]);
    expect(p.node("a:b/../c")).toBe("/explorer/testnet/node/a%3Ab%2F..%2Fc?x=1");
    expect(explorerPathsFor(null).node("node-1")).toBe("/explorer/node/node-1");
  });

  it("TestExplorerPathsFor_a_custom_query_rides_on_every_url", () => {
    const p = explorerPathsFor("custom", "?gateway=g");
    expect([p.home, p.validators, p.block(1)]).toEqual([
      "/explorer/custom?gateway=g",
      "/explorer/custom/validators?gateway=g",
      "/explorer/custom/block/1?gateway=g",
    ]);
  });

  it("TestExplorerPathsFor_encodes_identifiers_in_a_network_too", () => {
    expect(explorerPathsFor("testnet").wallet("x/../y")).toBe("/explorer/testnet/wallet/x%2F..%2Fy");
  });
});

describe("the rewards page", () => {
  it("TestExplorerPaths_rewards_is_a_page_of_every_network", () => {
    expect(explorerPathsFor(null).rewards).toBe("/explorer/rewards");
    expect(explorerPathsFor("testnet", "?x=1").rewards).toBe("/explorer/testnet/rewards?x=1");
  });

  it("TestParseExplorerPath_rewards_is_a_page_not_a_network_segment", () => {
    expect(parseExplorerPath("/explorer/rewards")).toEqual({ network: null, kind: "rewards", id: "" });
    expect(parseExplorerPath("/explorer/testnet/rewards")).toEqual({ network: "testnet", kind: "rewards", id: "" });
  });
});
