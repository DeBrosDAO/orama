import { describe, expect, it } from "vitest";
import { ChainReadError } from "./client";
import { FakeGateway, addr } from "./fake-gateway";
import { ARCHIVE_PARAMS, rangeJson, rangePath, retainJson } from "./fake-network";
import { readRange } from "./archive";
import { createChainSource } from "./source";

const source = (gw: FakeGateway) => createChainSource({ fetcher: gw.fetch });

function archiveGateway(retain = retainJson()): FakeGateway {
  return new FakeGateway().on("query/orama.archive.v1.Query/Params", ARCHIVE_PARAMS).on("query/orama.archive.v1.Query/RetainHeight", retain);
}

describe("readRange", () => {
  it("TestReadRange_an_archived_range_counts_the_winning_operators_and_its_deals", () => {
    const range = readRange(rangeJson(1, 100, { archived: true, decided: true, operators: [addr(1), addr(2), addr(3)], deal_ids: ["4", "5", "6"] }));
    expect(range).toEqual({ start: 1, end: 100, state: "archived", attesters: 3, replicaDeals: 3 });
  });

  it("TestReadRange_a_decided_range_waits_for_its_replica_deals", () => {
    const range = readRange(rangeJson(1, 100, { decided: true, operators: [addr(1), addr(2), addr(3)], deal_ids: ["4"] }));
    expect(range).toMatchObject({ state: "decided", attesters: 3, replicaDeals: 1 });
  });

  it("TestReadRange_an_undecided_range_shows_its_best_candidate", () => {
    const range = readRange(
      rangeJson(1, 100, { candidates: [{ operators: [addr(1)] }, { operators: [addr(2), addr(3)] }] }),
    );
    expect(range).toMatchObject({ state: "attesting", attesters: 2, replicaDeals: 0 });
  });

  it("TestReadRange_the_same_operator_twice_counts_once", () => {
    expect(readRange(rangeJson(1, 100, { decided: true, operators: [addr(1), addr(1)] })).attesters).toBe(1);
  });

  it("TestReadRange_a_range_with_no_candidates_and_no_decision_has_no_attesters", () => {
    expect(readRange(rangeJson(1, 100))).toMatchObject({ state: "attesting", attesters: 0 });
  });

  it("TestReadRange_a_malformed_record_is_rejected", () => {
    expect(() => readRange({ range: { start_height: "1" } })).toThrow(ChainReadError);
  });
});

describe("getArchiveHealth", () => {
  it("TestGetArchiveHealth_reports_the_prefix_the_retain_lag_and_the_oldest_unarchived_ranges", async () => {
    // tip 1450, window 1000: the window alone would retain 450; the archive holds it at 400.
    const gw = archiveGateway()
      .on(rangePath(401, 500), rangeJson(401, 500, { decided: true, operators: [addr(1), addr(2), addr(3)], deal_ids: ["1"] }))
      .on(rangePath(501, 600), rangeJson(501, 600, { candidates: [{ operators: [addr(1)] }] }));
    const health = await source(gw).getArchiveHealth();
    expect(health).toMatchObject({
      tip: 1450,
      lastArchivedHeight: 400,
      retainHeight: 400,
      retentionWindowBlocks: 1000,
      rangeBlocks: 100,
      retainLag: 50,
      unarchivedRanges: 10,
    });
    expect(health.oldestUnarchived.map((r) => [r.start, r.end, r.state])).toEqual([
      [401, 500, "decided"],
      [501, 600, "attesting"],
      [601, 700, "none"],
      [701, 800, "none"],
    ]);
  });

  it("TestGetArchiveHealth_a_fully_archived_chain_has_no_lag_and_no_ranges_to_look_up", async () => {
    const gw = archiveGateway(retainJson({ tip: "1450", last_archived_height: "1400", retain_height: "450" }));
    const health = await source(gw).getArchiveHealth();
    expect(health).toMatchObject({ retainLag: 0, unarchivedRanges: 0, oldestUnarchived: [] });
    expect(gw.asked.some((p) => p.includes("Range"))).toBe(false);
  });

  it("TestGetArchiveHealth_a_young_chain_with_nothing_archived_looks_at_the_first_ranges", async () => {
    const gw = archiveGateway(retainJson({ tip: "250", last_archived_height: "0", retain_height: "0" }));
    const health = await source(gw).getArchiveHealth();
    expect(health.retainHeight).toBe(0);
    expect(health.retainLag).toBe(0);
    expect(health.oldestUnarchived.map((r) => [r.start, r.end])).toEqual([
      [1, 100],
      [101, 200],
    ]);
  });

  it("TestGetArchiveHealth_a_negative_retain_height_is_read_as_nothing_to_prune", async () => {
    const health = await source(archiveGateway(retainJson({ tip: "50", last_archived_height: "0", retain_height: "-950" }))).getArchiveHealth();
    expect(health.retainHeight).toBe(-950);
    expect(health.retainLag).toBe(0);
  });

  it("TestGetArchiveHealth_a_failing_range_read_rejects_instead_of_calling_it_unattested", async () => {
    const gw = archiveGateway().on(rangePath(401, 500), rangeJson(401, 500));
    gw.failing.add(rangePath(501, 600));
    await expect(source(gw).getArchiveHealth()).rejects.toThrow(ChainReadError);
  });

  it("TestGetArchiveHealth_a_failing_retention_read_rejects", async () => {
    const gw = archiveGateway();
    gw.failing.add("query/orama.archive.v1.Query/RetainHeight");
    await expect(source(gw).getArchiveHealth()).rejects.toThrow("HTTP 500");
  });
});
