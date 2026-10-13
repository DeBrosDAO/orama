import { describe, expect, it } from "vitest";
import { ALICE, BOB, decode, fieldsOf, said } from "./fixtures";

const NODE = "/orama.nodes.v1.";

describe("x/nodes decoders", () => {
  it("TestNodes_register_operator", () => {
    expect(said(decode(`${NODE}MsgRegisterOperator`, { operator: ALICE }))).toBe("Alice registered as a node operator");
  });

  it("TestNodes_register_node_states_roles_and_keeps_the_facts", () => {
    const m = decode(`${NODE}MsgRegisterNode`, {
      operator: ALICE,
      node_id: "n1",
      roles: ["ROLE_STORAGE", "ROLE_DIRAUTH"],
      hot_key: BOB,
      endpoints: ["a.example:443", "b.example:443"],
      region_hint: "eu",
      asn: 64512,
    });
    expect(said(m)).toBe("Alice registered node n1 with roles storage, directory authority");
    expect(fieldsOf(m)).toEqual({
      "Node ID": "n1",
      Roles: "storage, directory authority",
      "Hot key": BOB,
      Endpoints: "a.example:443, b.example:443",
      "Region hint": "eu",
      ASN: "64,512",
    });
    expect(m.module).toBe("Nodes");
    expect(m.actor).toEqual({ address: ALICE, label: "Alice" });
  });

  it("TestNodes_a_failed_registration_says_what_it_tried", () => {
    expect(said(decode(`${NODE}MsgRegisterNode`, { operator: ALICE, node_id: "n1", roles: ["ROLE_RELAY"] }), true)).toBe(
      "Alice tried to register node n1 with role relay",
    );
  });

  it("TestNodes_update_lists_only_what_the_message_replaces", () => {
    const quiet = decode(`${NODE}MsgUpdateNode`, { operator: ALICE, node_id: "n1" });
    expect(fieldsOf(quiet)).toEqual({ "Node ID": "n1" });
    const loud = decode(`${NODE}MsgUpdateNode`, {
      operator: ALICE,
      node_id: "n1",
      hot_key: BOB,
      bindings: [{}, {}],
      set_endpoints: true,
      set_region_hint: true,
      set_asn: true,
      asn: 0,
    });
    expect(said(loud)).toBe("Alice updated node n1");
    expect(fieldsOf(loud)).toEqual({
      "Node ID": "n1",
      "New hot key": BOB,
      "Bindings replaced": "2",
      Endpoints: "none",
      "Region hint": "cleared",
      ASN: "undeclared",
    });
  });

  it("TestNodes_bond_and_unbond_state_the_amount_and_the_role", () => {
    const bond = decode(`${NODE}MsgBondNode`, { operator: ALICE, node_id: "n1", role: "ROLE_STORAGE", amount: "2500000000" });
    expect(said(bond)).toBe("Alice bonded 2.5 ORAMA on node n1 for the storage role");
    expect(bond.amount).toBe("2500000000");
    expect(bond.category).toBe("staking");
    expect(said(decode(`${NODE}MsgUnbondNode`, { operator: ALICE, node_id: "n1", role: "ROLE_VALIDATOR", amount: "1000000000" }))).toBe(
      "Alice unbonded 1 ORAMA from node n1 for the validator role",
    );
  });

  it("TestNodes_fund_hot_key_names_the_source", () => {
    const base = { operator: ALICE, node_id: "n1", amount: "1000000000" };
    expect(said(decode(`${NODE}MsgFundHotKey`, { ...base, source: "FUND_SOURCE_BANK" }))).toBe(
      "Alice funded the hot key of node n1 with 1 ORAMA from their bank balance",
    );
    expect(said(decode(`${NODE}MsgFundHotKey`, { ...base, source: "FUND_SOURCE_EARNINGS" }))).toContain("from their earnings");
    expect(said(decode(`${NODE}MsgFundHotKey`, base))).toContain("from their earnings");
    expect(fieldsOf(decode(`${NODE}MsgFundHotKey`, { ...base, source: "FUND_SOURCE_BANK" }))["Taken from"]).toBe("their bank balance");
  });

  it("TestNodes_fund_hot_key_does_not_guess_an_unknown_source", () => {
    const m = decode(`${NODE}MsgFundHotKey`, { operator: ALICE, node_id: "n1", amount: "1", source: "FUND_SOURCE_MOON" });
    expect(said(m)).toContain("from an unrecognised source (moon)");
  });

  it("TestNodes_declare_capacity_is_in_bytes_and_a_readable_size", () => {
    const m = decode(`${NODE}MsgDeclareCapacity`, { operator: ALICE, node_id: "n1", capacity_bytes: "1610612736" });
    expect(said(m)).toBe("Alice declared 1.5 GiB (1,610,612,736 bytes) of storage capacity on node n1");
  });

  it("TestNodes_node_names_and_clusters", () => {
    expect(said(decode(`${NODE}MsgClaimNodeName`, { operator: ALICE, node_id: "n1", name: "atlas" }))).toBe(
      "Alice claimed the name “atlas” for node n1",
    );
    expect(said(decode(`${NODE}MsgReleaseNodeName`, { operator: ALICE, node_id: "n1" }))).toBe("Alice released the name of node n1");
    const cluster = decode(`${NODE}MsgRegisterCluster`, {
      operator: ALICE,
      cluster_id: "c1",
      base_domain: "c1.example",
      public_endpoints: ["https://c1.example"],
      metadata_uri: "ipfs://x",
    });
    expect(said(cluster)).toBe("Alice registered cluster c1");
    expect(fieldsOf(cluster)).toEqual({
      "Cluster ID": "c1",
      "Base domain": "c1.example",
      "Public endpoints": "https://c1.example",
      "Metadata URI": "ipfs://x",
    });
    expect(said(decode(`${NODE}MsgUpdateCluster`, { operator: ALICE, cluster_id: "c1" }))).toBe("Alice updated cluster c1");
    expect(said(decode(`${NODE}MsgRetireCluster`, { operator: ALICE, cluster_id: "c1" }))).toBe("Alice retired cluster c1");
    expect(said(decode(`${NODE}MsgRetireNode`, { operator: ALICE, node_id: "n1" }))).toBe("Alice retired node n1");
  });

  it("TestNodes_a_message_with_no_node_id_still_reads", () => {
    expect(said(decode(`${NODE}MsgRetireNode`, { operator: ALICE }))).toBe("Alice retired node (empty)");
  });
});
