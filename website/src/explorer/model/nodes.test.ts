import { describe, expect, it } from "vitest";
import {
  asnShares,
  bondOf,
  capacityTotals,
  isLive,
  operatorCount,
  roleBonded,
  roleCounts,
  statusCounts,
  totalBond,
  utilisation,
} from "./nodes";
import { node, nodeParams as params } from "./node-fixtures";
import type { ChainNode } from "./nodes";

describe("isLive", () => {
  it("TestIsLive_only_retired_and_tombstoned_nodes_are_out", () => {
    expect(["registered", "active", "jailed"].map((status) => isLive(node("n", { status: status as ChainNode["status"] })))).toEqual([true, true, true]);
    expect(isLive(node("n", { status: "retired" }))).toBe(false);
    expect(isLive(node("n", { status: "tombstoned" }))).toBe(false);
  });
});

describe("roleCounts and statusCounts", () => {
  it("TestRoleCounts_counts_each_role_of_the_live_nodes_only", () => {
    const counts = roleCounts([
      node("a", { roles: ["validator", "storage"] }),
      node("b", { roles: ["storage", "relay"] }),
      node("c", { roles: ["storage"], status: "retired" }),
    ]);
    expect(counts).toEqual({ validator: 1, storage: 2, relay: 1, exit: 0, dirauth: 0, archiver: 0 });
  });

  it("TestStatusCounts_counts_every_status_including_the_ones_that_are_gone", () => {
    const counts = statusCounts([node("a"), node("b", { status: "jailed" }), node("c", { status: "retired" })]);
    expect(counts).toEqual({ registered: 0, active: 1, jailed: 1, retired: 1, tombstoned: 0 });
  });

  it("TestRoleCounts_no_nodes_is_all_zero", () => {
    expect(Object.values(roleCounts([])).every((n) => n === 0)).toBe(true);
  });
});

describe("operatorCount", () => {
  it("TestOperatorCount_counts_distinct_operators_of_live_nodes", () => {
    expect(operatorCount([node("a"), node("b"), node("c", { operator: "orama1other" }), node("d", { operator: "orama1gone", status: "retired" })])).toBe(2);
    expect(operatorCount([])).toBe(0);
  });
});

describe("asnShares", () => {
  it("TestAsnShares_groups_live_nodes_by_declared_asn_largest_first_and_undeclared_last", () => {
    const shares = asnShares([
      node("a", { asn: 24940 }),
      node("b", { asn: 24940 }),
      node("c", { asn: 16276 }),
      node("d"),
      node("e", { asn: 3320 }),
      node("f", { asn: 3320, status: "retired" }),
    ]);
    expect(shares.map((s) => [s.asn, s.nodes])).toEqual([
      [24940, 2],
      [3320, 1],
      [16276, 1],
      [0, 1],
    ]);
    expect(shares[0]?.share).toBeCloseTo(0.4);
    expect(shares.reduce((n, s) => n + s.share, 0)).toBeCloseTo(1);
  });

  it("TestAsnShares_no_live_nodes_is_empty", () => {
    expect(asnShares([])).toEqual([]);
    expect(asnShares([node("a", { status: "retired" })])).toEqual([]);
  });
});

describe("bonds", () => {
  const n = node("a", { roles: ["storage", "validator"], bonds: [{ role: "storage", amount: "150" }, { role: "validator", amount: "499" }] });

  it("TestBondOf_is_zero_for_a_role_with_no_bond_and_totals_add_up", () => {
    expect(bondOf(n, "storage")).toBe("150");
    expect(bondOf(n, "relay")).toBe("0");
    expect(totalBond(n)).toBe("649");
    expect(totalBond(node("b"))).toBe("0");
  });

  it("TestRoleBonded_a_bond_equal_to_the_floor_counts_and_one_below_does_not", () => {
    expect(roleBonded(n, params, "storage")).toBe(true);
    expect(roleBonded(n, params, "validator")).toBe(false);
    expect(roleBonded(node("c", { bonds: [{ role: "storage", amount: "100" }] }), params, "storage")).toBe(true);
    expect(roleBonded(n, params, "relay")).toBe(true);
  });
});

describe("capacity", () => {
  it("TestCapacityTotals_adds_the_live_storage_providers_only", () => {
    const totals = capacityTotals([
      node("a", { declaredCapacityBytes: "1000", reservedCapacityBytes: "250" }),
      node("b", { declaredCapacityBytes: "500", reservedCapacityBytes: "500", roles: ["storage", "relay"] }),
      node("c", { declaredCapacityBytes: "9000", roles: ["relay"] }),
      node("d", { declaredCapacityBytes: "9000", status: "retired" }),
    ]);
    expect(totals).toEqual({ declared: "1500", reserved: "750" });
  });

  it("TestUtilisation_is_the_reserved_share_and_null_without_declared_capacity", () => {
    expect(utilisation(node("a", { declaredCapacityBytes: "1000", reservedCapacityBytes: "250" }))).toBe(0.25);
    expect(utilisation(node("b", { declaredCapacityBytes: "3", reservedCapacityBytes: "1" }))).toBeCloseTo(0.3333, 3);
    expect(utilisation(node("c"))).toBeNull();
  });
});
