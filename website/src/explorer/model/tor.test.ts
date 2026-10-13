import { describe, expect, it } from "vitest";
import { node as baseNode } from "./node-fixtures";
import type { ChainNode } from "./nodes";
import { relayRows, torNodes, unregisteredRelayNodes } from "./tor";
import type { TorRelay } from "./tor";

const node = (id: string, over: Partial<ChainNode> = {}): ChainNode => baseNode(id, { roles: ["relay"], ...over });

const relay = (nodeId: string): TorRelay => ({ fingerprint: "AB".repeat(20), nodeId, operator: "orama1op", prefix16: "10.1.0.0/16", exit: false, jailed: false });

describe("relayRows", () => {
  it("TestRelayRows_joins_each_relay_to_its_node_and_leaves_a_missing_node_null", () => {
    const rows = relayRows([relay("a"), relay("gone")], [node("a")]);
    expect(rows[0]?.node?.id).toBe("a");
    expect(rows[1]?.node).toBeNull();
  });

  it("TestRelayRows_attaches_what_each_relay_was_paid_and_null_for_one_that_was_not", () => {
    const rows = relayRows([relay("a")], [node("a")], new Map([["AB".repeat(20), "400"]]));
    expect(rows[0]?.paid).toBe("400");
    expect(relayRows([relay("a")], [node("a")])[0]?.paid).toBeNull();
  });

  it("TestRelayRows_no_relays_is_empty", () => {
    expect(relayRows([], [node("a")])).toEqual([]);
  });
});

describe("torNodes", () => {
  it("TestTorNodes_splits_live_nodes_by_tor_role_and_leaves_retired_ones_out", () => {
    const got = torNodes([
      node("r", { roles: ["relay"] }),
      node("x", { roles: ["relay", "exit"] }),
      node("d", { roles: ["dirauth", "validator"] }),
      node("old", { roles: ["dirauth"], status: "retired" }),
      node("s", { roles: ["storage"] }),
    ]);
    expect(got.relays.map((n) => n.id)).toEqual(["r", "x"]);
    expect(got.exits.map((n) => n.id)).toEqual(["x"]);
    expect(got.authorities.map((n) => n.id)).toEqual(["d"]);
  });
});

describe("unregisteredRelayNodes", () => {
  it("TestUnregisteredRelayNodes_are_live_relay_or_exit_nodes_with_no_relay_record", () => {
    const nodes = [node("a"), node("b", { roles: ["exit"] }), node("c", { roles: ["storage"] }), node("d", { status: "retired" })];
    expect(unregisteredRelayNodes([relay("a")], nodes).map((n) => n.id)).toEqual(["b"]);
    expect(unregisteredRelayNodes([], nodes).map((n) => n.id)).toEqual(["a", "b"]);
  });
});
