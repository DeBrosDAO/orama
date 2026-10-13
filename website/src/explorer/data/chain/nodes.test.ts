import { describe, expect, it } from "vitest";
import { ChainReadError, createClient } from "./client";
import { FakeGateway } from "./fake-gateway";
import { NODES_METHOD, NODE_PARAMS, byId, nodeJson, page, pagePath } from "./fake-network";
import { loadNodeDetail, loadNodeSet, readNode, readNodeParams } from "./nodes";
import { createChainSource } from "./source";

const PARAMS_PATH = "query/orama.nodes.v1.Query/Params";

function gateway(nodes: unknown[], nextKey = ""): FakeGateway {
  return new FakeGateway().on(pagePath(NODES_METHOD), page("nodes", nodes, nextKey)).on(PARAMS_PATH, NODE_PARAMS);
}

const client = (gw: FakeGateway) => createChainSource({ fetcher: gw.fetch });
const raw = (gw: FakeGateway) => createClient(gw.fetch);

describe("readNode", () => {
  it("TestReadNode_maps_roles_status_bonds_capacity_and_the_declared_asn", () => {
    const node = readNode(nodeJson(1, { roles: ["ROLE_VALIDATOR", "ROLE_RELAY"], status: "NODE_STATUS_JAILED" }));
    expect(node.id).toBe("node-1");
    expect(node.roles).toEqual(["validator", "relay"]);
    expect(node.status).toBe("jailed");
    expect(node.bonds).toEqual([{ role: "storage", amount: "150000000000" }]);
    expect(node.services).toEqual(["ipfs"]);
    expect(node.declaredCapacityBytes).toBe("2147483648");
    expect(node.asn).toBe(24940);
    expect(node.registeredAtHeight).toBe(5);
  });

  it("TestReadNode_text_an_operator_wrote_is_cleaned", () => {
    const node = readNode(nodeJson(1, { endpoints: ["https://a.example‮:443\u0007"], region_hint: "eu​-1", bindings: [{ service: "to‮r" }] }));
    expect(node.endpoints).toEqual(["https://a.example:443"]);
    expect(node.regionHint).toBe("eu-1");
    expect(node.services).toEqual(["tor"]);
  });

  it("TestReadNode_an_undeclared_asn_is_zero", () => {
    expect(readNode(nodeJson(1, { asn: 0 })).asn).toBe(0);
  });

  it.each([
    ["an unknown role", { roles: ["ROLE_WIZARD"] }],
    ["an unspecified role", { roles: ["ROLE_UNSPECIFIED"] }],
    ["an unknown status", { status: "NODE_STATUS_LOST" }],
    ["a node id with a slash", { node_id: "a/b" }],
    ["an empty node id", { node_id: "" }],
    ["an operator that is not an account", { operator: "cosmos1abc" }],
    ["a negative capacity", { declared_capacity_bytes: "-1" }],
  ])("TestReadNode_rejects_%s", (_, over) => {
    expect(() => readNode(nodeJson(1, over))).toThrow(ChainReadError);
  });
});

describe("readNodeParams", () => {
  it("TestReadNodeParams_reads_the_floors_the_unbonding_time_and_the_bond_per_gib", () => {
    const p = readNodeParams(NODE_PARAMS);
    expect(p.minBond).toEqual([
      { role: "validator", amount: "500000000000" },
      { role: "storage", amount: "100000000000" },
    ]);
    expect(p.unbondingSeconds).toBe(86_400);
    expect(p.bondPerGib).toBe("1000000000");
  });
});

describe("loadNodeSet", () => {
  it("TestLoadNodeSet_reads_every_node_with_the_parameters", async () => {
    const set = await loadNodeSet(raw(gateway([nodeJson(1), nodeJson(2)])));
    expect(set.nodes.map((n) => n.id)).toEqual(["node-1", "node-2"]);
    expect(set.truncated).toBe(false);
    expect(set.params.unbondingSeconds).toBe(86_400);
  });

  it("TestLoadNodeSet_follows_the_page_key_to_the_last_page", async () => {
    const gw = new FakeGateway()
      .on(pagePath(NODES_METHOD), page("nodes", [nodeJson(1)], "a2V5"))
      .on(pagePath(NODES_METHOD, "a2V5"), page("nodes", [nodeJson(2)]))
      .on(PARAMS_PATH, NODE_PARAMS);
    const set = await client(gw).getNetworkNodes();
    expect(set.nodes.map((n) => n.id)).toEqual(["node-1", "node-2"]);
    expect(set.truncated).toBe(false);
    expect(gw.asked.filter((p) => p.startsWith(NODES_METHOD))).toHaveLength(2);
  });

  it("TestLoadNodeSet_says_it_is_cut_when_the_chain_has_more_pages_than_are_read", async () => {
    let n = 0;
    const get = async (path: string) => {
      if (path === PARAMS_PATH) return NODE_PARAMS;
      n += 1;
      return page("nodes", [nodeJson(n)], `k${n}`);
    };
    const set = await loadNodeSet({ get });
    expect(set.nodes).toHaveLength(5);
    expect(set.truncated).toBe(true);
  });

  it("TestLoadNodeSet_a_gateway_that_does_not_serve_the_list_says_so_instead_of_not_on_the_chain", async () => {
    const gw = new FakeGateway().on(PARAMS_PATH, NODE_PARAMS);
    const err = await client(gw).getNetworkNodes().catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ChainReadError);
    expect((err as Error).message).toContain("does not serve the list of nodes");
    expect((err as Error).message).not.toContain("not on the chain");
  });

  it("TestLoadNodeSet_a_chain_with_no_nodes_is_an_empty_list_not_an_error", async () => {
    const set = await client(gateway([])).getNetworkNodes();
    expect(set.nodes).toEqual([]);
    expect(set.truncated).toBe(false);
  });

  it("TestLoadNodeSet_a_failing_read_rejects_without_what_the_node_said", async () => {
    const gw = gateway([nodeJson(1)]);
    gw.failing.add(PARAMS_PATH);
    const err = await client(gw).getNetworkNodes().catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ChainReadError);
    expect((err as Error).message).not.toContain("oramad");
  });

  it("TestLoadNodeSet_one_malformed_node_rejects_the_whole_read", async () => {
    await expect(client(gateway([nodeJson(1), nodeJson(2, { status: "NODE_STATUS_LOST" })])).getNetworkNodes()).rejects.toThrow(ChainReadError);
  });

  it("TestGetNetworkNodes_a_second_read_inside_the_ttl_is_not_asked_again", async () => {
    const gw = gateway([nodeJson(1)]);
    const source = client(gw);
    await source.getNetworkNodes();
    await source.getNetworkNodes();
    expect(gw.asked.filter((p) => p.startsWith(NODES_METHOD))).toHaveLength(1);
  });

  it("TestGetNetworkNodes_a_failed_read_is_not_kept", async () => {
    const gw = gateway([nodeJson(1)]);
    gw.failing.add(PARAMS_PATH);
    const source = client(gw);
    await expect(source.getNetworkNodes()).rejects.toThrow();
    gw.failing.clear();
    await expect(source.getNetworkNodes()).resolves.toBeDefined();
  });
});

describe("loadNodeDetail", () => {
  const detailGateway = () =>
    new FakeGateway()
      .on(byId("query/orama.nodes.v1.Query/Node", "node_id", "node-1"), { node: nodeJson(1) })
      .on(byId("query/orama.nodes.v1.Query/NameOfNode", "node_id", "node-1"), { name: { name: "alpha", node_id: "node-1", operator: "", deposit: "1" } })
      .on(byId("query/orama.nodes.v1.Query/NodeUnbondings", "node_id", "node-1"), {
        unbondings: [{ id: "4", node_id: "node-1", operator: "", role: "ROLE_STORAGE", amount: "5000000000", completion_unix: "1791000000" }],
      })
      .on(PARAMS_PATH, NODE_PARAMS);

  it("TestLoadNodeDetail_joins_the_name_and_the_unbonding_bonds", async () => {
    const detail = await loadNodeDetail(raw(detailGateway()), "node-1");
    expect(detail?.node.id).toBe("node-1");
    expect(detail?.name).toBe("alpha");
    expect(detail?.unbondings).toEqual([{ id: 4, role: "storage", amount: "5000000000", completesAt: "2026-10-03T04:00:00.000Z" }]);
    expect(detail?.params.bondPerGib).toBe("1000000000");
  });

  it("TestLoadNodeDetail_a_node_with_no_name_has_a_null_name", async () => {
    const gw = detailGateway();
    gw.routes.delete(byId("query/orama.nodes.v1.Query/NameOfNode", "node_id", "node-1"));
    const detail = await client(gw).getNode("node-1");
    expect(detail?.name).toBeNull();
  });

  it("TestLoadNodeDetail_an_unknown_node_is_null_not_an_error", async () => {
    expect(await client(detailGateway()).getNode("node-9")).toBeNull();
  });

  it("TestLoadNodeDetail_an_id_that_cannot_be_a_node_id_is_never_asked_for", async () => {
    const gw = detailGateway();
    for (const id of ["", "a/b", "../x", "x".repeat(65), "-a"]) expect(await client(gw).getNode(id)).toBeNull();
    expect(gw.asked).toEqual([]);
  });

  it("TestLoadNodeDetail_a_failing_unbonding_read_rejects", async () => {
    const gw = detailGateway();
    gw.failing.add(byId("query/orama.nodes.v1.Query/NodeUnbondings", "node_id", "node-1"));
    await expect(client(gw).getNode("node-1")).rejects.toThrow(ChainReadError);
  });
});
