import { describe, expect, it } from "vitest";
import { ChainReadError } from "./client";
import { FakeGateway } from "./fake-gateway";
import { DEALS_METHOD, STORAGE_PARAMS, dealJson, page, pagePath, slotJson } from "./fake-network";
import { MAX_REPLICAS_READ, readDeal } from "./storage";
import { createChainSource } from "./source";
import { withQuery } from "./client";

const PARAMS_PATH = "query/orama.storage.v1.Query/Params";
const SLOT = "query/orama.storage.v1.Query/Slot";
const slotPath = (deal: number, index: number) => withQuery(SLOT, { json: JSON.stringify({ deal_id: String(deal), slot: index }) });
const source = (gw: FakeGateway) => createChainSource({ fetcher: gw.fetch });

function dealsGateway(deals: unknown[], nextKey = ""): FakeGateway {
  return new FakeGateway().on(pagePath(DEALS_METHOD, "", true), page("deals", deals, nextKey)).on(PARAMS_PATH, STORAGE_PARAMS);
}

describe("readDeal", () => {
  it("TestReadDeal_maps_class_status_price_replicas_and_the_protocol_flag", () => {
    const deal = readDeal(dealJson(7, { class: "DEAL_CLASS_ARCHIVE", protocol: true, replicas: 5, status: "DEAL_STATUS_EXPIRED" }));
    expect(deal).toMatchObject({ id: 7, class: "archive", protocol: true, replicas: 5, status: "expired", pricePerEpoch: "2000000000", durationEpochs: 30, escrow: "60000000000" });
    expect(deal.repairDelegate).toBeNull();
  });

  it("TestReadDeal_a_repair_delegate_is_kept_as_cleaned_text", () => {
    expect(readDeal(dealJson(1, { repair_delegate: "op‮-1" })).repairDelegate).toBe("op-1");
  });

  it.each([
    ["an unknown class", { class: "DEAL_CLASS_SECRET" }],
    ["an unknown status", { status: "DEAL_STATUS_CANCELLED" }],
    ["a client that is not an account", { client: "x" }],
    ["a missing id", { id: undefined }],
    ["a non-boolean protocol flag", { protocol: "yes" }],
  ])("TestReadDeal_rejects_%s", (_, over) => {
    expect(() => readDeal(dealJson(1, over))).toThrow(ChainReadError);
  });
});

describe("getDeals", () => {
  it("TestGetDeals_asks_for_the_newest_page_and_reads_the_parameters", async () => {
    const gw = dealsGateway([dealJson(9), dealJson(8)]);
    const list = await source(gw).getDeals();
    expect(list.deals.map((d) => d.id)).toEqual([9, 8]);
    expect(list.hasOlder).toBe(false);
    expect(list.params).toMatchObject({ dealFee: "1000000", missThreshold: 2, protocolEveryEpochs: 7, protocolDurationEpochs: 365 });
    expect(gw.asked).toContain(pagePath(DEALS_METHOD, "", true));
  });

  it("TestGetDeals_says_there_are_older_deals_when_the_page_has_a_next_key", async () => {
    expect((await source(dealsGateway([dealJson(9)], "a2V5")).getDeals()).hasOlder).toBe(true);
  });

  it("TestGetDeals_no_deals_is_an_empty_list_not_an_error", async () => {
    const list = await source(dealsGateway([])).getDeals();
    expect(list.deals).toEqual([]);
    expect(list.hasOlder).toBe(false);
  });

  it("TestGetDeals_a_failing_read_rejects_with_a_reader_message", async () => {
    const gw = dealsGateway([dealJson(1)]);
    gw.failing.add(PARAMS_PATH);
    await expect(source(gw).getDeals()).rejects.toThrow("HTTP 500");
  });
});

describe("getSettlementQueue", () => {
  it("TestGetSettlementQueue_reads_the_pending_count", async () => {
    const gw = new FakeGateway().on("query/orama.storage.v1.Query/Queue", { pending: "12", head: "3", tail: "15" });
    expect(await source(gw).getSettlementQueue()).toEqual({ pending: 12 });
  });

  it("TestGetSettlementQueue_a_malformed_count_rejects", async () => {
    const gw = new FakeGateway().on("query/orama.storage.v1.Query/Queue", { pending: "-1" });
    await expect(source(gw).getSettlementQueue()).rejects.toThrow(ChainReadError);
  });
});

describe("getDealReplicas", () => {
  it("TestGetDealReplicas_reads_one_slot_per_replica_and_a_slot_with_no_provider_is_unassigned", async () => {
    const gw = new FakeGateway()
      .on(slotPath(4, 0), slotJson(4, 0))
      .on(slotPath(4, 1), slotJson(4, 1, { status: "SLOT_STATUS_EVICTED", consecutive_misses: 2 }))
      .on(slotPath(4, 2), slotJson(4, 2, { node_id: "", status: "SLOT_STATUS_UNASSIGNED" }));
    const replicas = await source(gw).getDealReplicas(4, 3);
    expect(replicas.map((r) => [r.index, r.nodeId, r.status, r.consecutiveMisses])).toEqual([
      [0, "node-1", "active", 0],
      [1, "node-2", "evicted", 2],
      [2, null, "unassigned", 0],
    ]);
  });

  it("TestGetDealReplicas_a_slot_the_chain_cannot_answer_is_a_failure_not_an_unassigned_replica", async () => {
    const gw = new FakeGateway().on(slotPath(4, 0), slotJson(4, 0));
    await expect(source(gw).getDealReplicas(4, 2)).rejects.toThrow(ChainReadError);
  });

  it("TestGetDealReplicas_a_replica_node_that_cannot_be_a_node_id_is_rejected", async () => {
    const gw = new FakeGateway().on(slotPath(4, 0), slotJson(4, 0, { node_id: ".." }));
    await expect(source(gw).getDealReplicas(4, 1)).rejects.toThrow(ChainReadError);
  });

  it("TestGetDealReplicas_reads_at_most_a_dozen_slots", async () => {
    const gw = new FakeGateway();
    for (let i = 0; i < MAX_REPLICAS_READ; i++) gw.on(slotPath(4, i), slotJson(4, i));
    const replicas = await source(gw).getDealReplicas(4, 500);
    expect(replicas).toHaveLength(MAX_REPLICAS_READ);
    expect(gw.asked).toHaveLength(MAX_REPLICAS_READ);
  });

  it("TestGetDealReplicas_an_id_that_cannot_be_a_deal_is_never_asked_for", async () => {
    const gw = new FakeGateway();
    expect(await source(gw).getDealReplicas(0, 3)).toEqual([]);
    expect(await source(gw).getDealReplicas(1.5, 3)).toEqual([]);
    expect(gw.asked).toEqual([]);
  });

  it("TestGetDealReplicas_a_failing_slot_read_rejects", async () => {
    const gw = new FakeGateway().on(slotPath(4, 0), slotJson(4, 0));
    gw.failing.add(slotPath(4, 1));
    await expect(source(gw).getDealReplicas(4, 2)).rejects.toThrow(ChainReadError);
  });
});
