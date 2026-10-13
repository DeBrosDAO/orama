import { describe, expect, it } from "vitest";
import { createAssetsSource } from "./assets-source";
import { createClient } from "./client";
import { FakeGateway, addr } from "./fake-gateway";
import { proposalJson } from "./governance-fixtures";
import { idsDescending, newestId } from "./newest-id";
import { queryPath } from "./module-query";

/** An `exists` over ids 1..count that records how many times it was asked. */
function counting(count: number) {
  const asked: number[] = [];
  return {
    asked,
    exists: async (id: number) => {
      asked.push(id);
      return id >= 1 && id <= count;
    },
  };
}

describe("newestId", () => {
  it.each([0, 1, 2, 3, 7, 8, 9, 100, 1023, 1024, 100_000])("TestNewestId_finds_%i", async (count) => {
    const c = counting(count);
    expect(await newestId(c.exists)).toBe(count);
  });

  it("TestNewestId_asks_about_twice_log2_of_the_count_and_nothing_when_nothing_exists", async () => {
    const empty = counting(0);
    await newestId(empty.exists);
    expect(empty.asked).toEqual([1]);
    const many = counting(100_000);
    await newestId(many.exists);
    expect(many.asked.length).toBeLessThanOrEqual(36);
  });

  it("TestNewestId_stops_on_a_source_that_says_yes_to_everything", async () => {
    expect(await newestId(async () => true)).toBeLessThanOrEqual(2 ** 40);
  });
});

describe("idsDescending", () => {
  it("TestIdsDescending_ends_at_one_and_never_goes_below", () => {
    expect(idsDescending(5, 3)).toEqual([5, 4, 3]);
    expect(idsDescending(2, 5)).toEqual([2, 1]);
    expect(idsDescending(0, 5)).toEqual([]);
  });
});

describe("assets source, newest first", () => {
  const upgrade = { software_upgrade: { name: "v", height: "1" } };
  const gatewayWith = (count: number) => {
    const gw = new FakeGateway();
    for (let id = 1; id <= count; id += 1) gw.on(queryPath("orama.houses.v1.Query/Proposal", { proposal_id: id }), { proposal: proposalJson(id, upgrade, { proposer: addr(1) }) });
    return gw;
  };
  const source = (gw: FakeGateway, now = () => 0) => createAssetsSource(createClient(gw.fetch, "https://gw.example/v1/chain"), now);

  it("TestGetProposals_lists_the_newest_first_and_says_where_the_older_page_starts", async () => {
    const page = await source(gatewayWith(7)).getProposals(null, 3);
    expect(page.newest).toBe(7);
    expect(page.items.map((p) => p.id)).toEqual([7, 6, 5]);
    expect(page.next).toBe(4);
  });

  it("TestGetProposals_the_last_page_has_no_older_one_and_a_from_above_the_newest_is_clamped", async () => {
    const s = source(gatewayWith(4));
    expect((await s.getProposals(2, 3)).items.map((p) => p.id)).toEqual([2, 1]);
    expect((await s.getProposals(2, 3)).next).toBeNull();
    expect((await s.getProposals(900, 2)).items.map((p) => p.id)).toEqual([4, 3]);
  });

  it("TestGetProposals_a_chain_with_none_is_an_empty_page", async () => {
    expect(await source(new FakeGateway()).getProposals(null, 10)).toEqual({ newest: 0, items: [], next: null });
  });

  it("TestGetProposals_paging_back_asks_the_chain_nothing_new_for_a_few_seconds_then_asks_again", async () => {
    const gw = gatewayWith(6);
    let now = 0;
    const s = source(gw, () => now);
    await s.getProposals(null, 3);
    const afterFirst = gw.asked.length;
    await s.getProposals(null, 3);
    await s.getProposals(5, 2);
    expect(gw.asked.length).toBe(afterFirst);
    now = 60_000;
    await s.getProposals(null, 3);
    expect(gw.asked.length).toBeGreaterThan(afterFirst);
  });

  it("TestGetProposals_a_failed_read_is_not_remembered", async () => {
    const gw = gatewayWith(2);
    gw.failing.add(queryPath("orama.houses.v1.Query/Proposal", { proposal_id: 1 }));
    const s = source(gw);
    await expect(s.getProposals(null, 2)).rejects.toThrow();
    gw.failing.clear();
    expect((await s.getProposals(null, 2)).items.map((p) => p.id)).toEqual([2, 1]);
  });
});
