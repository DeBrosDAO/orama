import { describe, expect, it } from "vitest";
import { buildRegistry } from "./registry";
import { customHome, customQuery, networkQuery, pinnedChain, selectNetwork, switchTargets, withPinnedChain } from "./selection";

const SHA = "84ee8d46628343b85c464185e1009974beb34f91c99477a75a5dfb66a5918b67";
const manifest = (name: string, id: number) => ({
  name,
  chain_id: `orama-${name}-${id}`,
  genesis_sha256: SHA,
  seeds: [`ns1.${name}.orama.network`, `ns2.${name}.orama.network`],
});
const REGISTRY = buildRegistry({
  "../../../../networks/stagenet/manifest.json": manifest("stagenet", 7),
  "../../../../networks/testnet/manifest.json": manifest("testnet", 2),
});
const HASH = "A".repeat(64);
const GATEWAY = "https://gateway.example.org";

describe("selectNetwork: registry networks", () => {
  it("TestSelectNetwork_the_bare_explorer_and_every_old_url_are_the_default_network", () => {
    for (const path of ["/explorer", "/explorer/", "/explorer/validators", `/explorer/tx/${HASH}`, "/explorer/block/42", "/explorer/wallet/orama1x"]) {
      const s = selectNetwork(path, "", REGISTRY);
      expect(s).toMatchObject({ key: "stagenet", custom: false, gateway: "https://stagenet.orama.network", expectedChainId: "orama-stagenet-7" });
      expect(s.routePath).toBe(path);
    }
  });

  it("TestSelectNetwork_a_network_segment_picks_that_network_and_routes_the_rest", () => {
    const s = selectNetwork(`/explorer/testnet/tx/${HASH}`, "", REGISTRY);
    expect(s).toMatchObject({ key: "testnet", gateway: "https://testnet.orama.network", expectedChainId: "orama-testnet-2" });
    expect(s.routePath).toBe(`/explorer/tx/${HASH}`);
    expect(selectNetwork("/explorer/testnet", "", REGISTRY).routePath).toBe("/explorer");
  });

  it("TestSelectNetwork_the_default_may_also_be_named_explicitly", () => {
    const s = selectNetwork("/explorer/stagenet/validators", "", REGISTRY);
    expect(s).toMatchObject({ key: "stagenet", routePath: "/explorer/validators" });
  });

  it("TestSelectNetwork_an_unknown_first_segment_is_a_page_of_the_default_network_and_so_not_found", () => {
    const s = selectNetwork("/explorer/no-such-net/tx/x", "", REGISTRY);
    expect(s.key).toBe("stagenet");
    expect(s.routePath).toBe("/explorer/no-such-net/tx/x");
  });

  it("TestSelectNetwork_links_stay_in_the_network_being_read", () => {
    const def = selectNetwork("/explorer", "", REGISTRY).paths;
    const other = selectNetwork("/explorer/testnet", "", REGISTRY).paths;
    expect(def.tx(HASH)).toBe(`/explorer/tx/${HASH}`);
    expect(other.tx(HASH)).toBe(`/explorer/testnet/tx/${HASH}`);
    expect(other.home).toBe("/explorer/testnet");
    expect(other.validators).toBe("/explorer/testnet/validators");
    expect(other.block(5)).toBe("/explorer/testnet/block/5");
    expect(other.wallet("orama1x")).toBe("/explorer/testnet/wallet/orama1x");
    expect(other.validator("orama1x")).toBe("/explorer/testnet/validator/orama1x");
  });
});

describe("selectNetwork: a custom gateway", () => {
  it("TestSelectNetwork_custom_reads_the_gateway_from_the_query_and_every_link_keeps_it", () => {
    const s = selectNetwork("/explorer/custom/tx/x", `?gateway=${encodeURIComponent(GATEWAY)}`, REGISTRY);
    expect(s).toMatchObject({ key: "custom", custom: true, gateway: GATEWAY, problem: null, expectedChainId: null });
    expect(s.routePath).toBe("/explorer/tx/x");
    expect(s.paths.block(9)).toBe(`/explorer/custom/block/9?gateway=${encodeURIComponent(GATEWAY)}`);
    expect(s.paths.keep("/explorer/custom/tx/x")).toBe(`/explorer/custom/tx/x?gateway=${encodeURIComponent(GATEWAY)}`);
  });

  it("TestSelectNetwork_custom_without_a_gateway_asks_for_one", () => {
    const s = selectNetwork("/explorer/custom", "", REGISTRY);
    expect(s).toMatchObject({ custom: true, gateway: null, problem: null });
    expect(s.paths.home).toBe("/explorer/custom");
  });

  it.each([
    ["clear http on a public host", "http://gateway.example.org"],
    ["a path", "https://gateway.example.org/v1/chain"],
    ["credentials", "https://user:pw@gateway.example.org"],
    ["a bare host", "gateway.example.org"],
    ["a script address", "javascript:alert(1)"],
  ])("TestSelectNetwork_custom_refuses_%s_before_anything_is_requested", (_what, gateway) => {
    const s = selectNetwork("/explorer/custom", `?gateway=${encodeURIComponent(gateway)}`, REGISTRY);
    expect(s.gateway).toBeNull();
    expect(s.problem).not.toBeNull();
  });

  it("TestSelectNetwork_custom_allows_a_gateway_on_this_machine_over_http", () => {
    const s = selectNetwork("/explorer/custom", `?gateway=${encodeURIComponent("http://localhost:6001")}`, REGISTRY);
    expect(s.gateway).toBe("http://localhost:6001");
  });
});

describe("customHome and the switcher", () => {
  it("TestCustomHome_builds_the_url_and_refuses_a_bad_gateway", () => {
    expect(customHome(GATEWAY)).toBe(`/explorer/custom?gateway=${encodeURIComponent(GATEWAY)}`);
    expect(() => customHome("http://gateway.example.org")).toThrow(/https gateway origin/);
    expect(() => customHome("")).toThrow();
  });

  it("TestCustomQuery_encodes_the_gateway_so_it_cannot_add_parameters", () => {
    expect(customQuery("https://a.example.org:8443")).toBe("?gateway=https%3A%2F%2Fa.example.org%3A8443");
  });

  it("TestSwitchTargets_default_opens_the_short_url_and_others_their_segment", () => {
    expect(switchTargets(REGISTRY)).toEqual([
      { key: "stagenet", label: "stagenet", to: "/explorer" },
      { key: "testnet", label: "testnet", to: "/explorer/testnet" },
    ]);
  });
});

describe("the chain pin", () => {
  it("TestPinnedChain_reads_a_chain_id_and_ignores_anything_else", () => {
    expect(pinnedChain("?chain=orama-stagenet-7")).toBe("orama-stagenet-7");
    expect(pinnedChain("")).toBeNull();
    expect(pinnedChain("?chain=")).toBeNull();
    expect(pinnedChain("?chain=<script>")).toBeNull();
    expect(pinnedChain("?chain=Orama-Stagenet")).toBeNull();
    expect(pinnedChain(`?chain=${"a".repeat(49)}`)).toBeNull();
  });

  it("TestWithPinnedChain_sets_chain_and_keeps_the_other_parameters", () => {
    expect(withPinnedChain("", "orama-stagenet-7")).toBe("?chain=orama-stagenet-7");
    expect(withPinnedChain("?trail=a&chain=old", "orama-stagenet-7")).toBe("?trail=a&chain=orama-stagenet-7");
    expect(withPinnedChain(`?gateway=${encodeURIComponent(GATEWAY)}`, "x-1")).toBe(`?gateway=${encodeURIComponent(GATEWAY)}&chain=x-1`);
  });

  it("TestNetworkQuery_keeps_only_the_gateway_and_the_chain_for_a_shared_link", () => {
    expect(networkQuery("?trail=a|b&gateway=https%3A%2F%2Fg.example.org&chain=x-1&other=1").toString()).toBe(
      "gateway=https%3A%2F%2Fg.example.org&chain=x-1",
    );
    expect(networkQuery("").toString()).toBe("");
  });
});
