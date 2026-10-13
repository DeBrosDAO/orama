import { describe, expect, it } from "vitest";
import { ChainIndexUnavailable, ChainReadError } from "./client";
import { pageLimit, pageNumber, readEpochRow, readEpochValidator, readSupplyPoint } from "./aggregates";
import { addr, chainFixture, HEAD, valoper } from "./fake-gateway";
import { createChainSource } from "./source";

const ORAMA = 1_000_000_000n;
const norama = (orama: bigint) => (orama * ORAMA).toString();

function epochJson(epoch: number, over: Record<string, unknown> = {}) {
  return {
    epoch,
    start_height: (epoch - 1) * 100 + 1,
    start_time: "2026-10-08T00:00:00Z",
    end_height: epoch * 100,
    end_time: "2026-10-09T00:00:00Z",
    blocks: 100,
    partial: false,
    max_mintable: norama(14_848n),
    minted_validators: "8908800000000",
    minted_service: "5000000000",
    minted_development: "0",
    minted_faucet: "0",
    minted_total: "8913800000000",
    burned: "300",
    fees_collected: "900",
    fees_burned: "300",
    fees_distributed: "600",
    rewards_credited: "8900000000000",
    rewards_force_bonded: "8800000000",
    ...over,
  };
}

function supplyJson(epoch: number, over: Record<string, unknown> = {}) {
  return {
    epoch,
    height: epoch * 100,
    time: "2026-10-09T00:00:00Z",
    total_supply: norama(1_000_000n),
    minted_to_date: norama(500n),
    burned_to_date: "42",
    genesis_supply: norama(999_000n),
    bonded: norama(400_000n),
    unbonding: norama(1_000n),
    earnings_pools: norama(2_000n),
    storage_escrow: norama(3_000n),
    other_protocol: norama(4_000n),
    circulating: norama(590_000n),
    ...over,
  };
}

async function setup() {
  const gw = await chainFixture();
  return { gw, source: createChainSource({ fetcher: gw.fetch }) };
}

describe("readEpochRow", () => {
  it("TestReadEpochRow_maps_every_field_and_keeps_amounts_as_exact_strings", () => {
    const big = "123456789012345678901234567890";
    const row = readEpochRow(epochJson(7, { minted_total: big, partial: true }));
    expect(row).toMatchObject({
      epoch: 7,
      startHeight: 601,
      endHeight: 700,
      blocks: 100,
      partial: true,
      maxMintable: norama(14_848n),
      mintedValidators: "8908800000000",
      mintedService: "5000000000",
      mintedTotal: big,
      feesDistributed: "600",
      rewardsForceBonded: "8800000000",
    });
    expect(row.startTime).toBe("2026-10-08T00:00:00.000Z");
  });

  it("TestReadEpochRow_a_missing_partial_flag_means_a_complete_epoch", () => {
    const { partial: _partial, ...rest } = epochJson(3);
    expect(readEpochRow(rest).partial).toBe(false);
  });

  it.each([
    ["an amount that is not an integer", { burned: "1.5" }],
    ["a negative amount", { minted_total: "-1" }],
    ["an end before the start", { end_height: 0 }],
    ["a time that is not a time", { end_time: "tomorrow" }],
    ["a partial flag that is not a boolean", { partial: "yes" }],
  ])("TestReadEpochRow_refuses_%s", (_name, over) => {
    expect(() => readEpochRow(epochJson(3, over))).toThrow(ChainReadError);
  });

  it("TestReadEpochRow_refuses_something_that_is_not_an_object", () => {
    expect(() => readEpochRow(null)).toThrow("malformed epoch");
    expect(() => readEpochRow([])).toThrow("malformed epoch");
  });
});

describe("readSupplyPoint", () => {
  it("TestReadSupplyPoint_maps_the_breakdown", () => {
    expect(readSupplyPoint(supplyJson(5))).toMatchObject({
      epoch: 5,
      height: 500,
      totalSupply: norama(1_000_000n),
      burnedToDate: "42",
      bonded: norama(400_000n),
      circulating: norama(590_000n),
    });
  });

  it("TestReadSupplyPoint_refuses_a_missing_figure", () => {
    const { circulating: _c, ...rest } = supplyJson(5);
    expect(() => readSupplyPoint(rest)).toThrow("malformed circulating supply");
  });
});

describe("paging bounds", () => {
  it("TestPageBounds_clamp_to_what_the_indexer_accepts", () => {
    expect([pageNumber(0), pageNumber(-4), pageNumber(2.9), pageNumber(5000), pageNumber(Number.NaN)]).toEqual([1, 1, 2, 1000, 1]);
    expect([pageLimit(0), pageLimit(25), pageLimit(500), pageLimit(Number.NaN)]).toEqual([1, 25, 100, 1]);
  });
});

describe("getEpochs", () => {
  it("TestGetEpochs_reads_one_page_and_says_whether_a_full_page_may_have_more", async () => {
    const { gw, source } = await setup();
    gw.on("index/epochs?page=1&limit=2", { page: 1, limit: 2, epochs: [epochJson(9), epochJson(8)] });
    gw.on("index/epochs?page=2&limit=2", { page: 2, limit: 2, epochs: [epochJson(7)] });
    const first = await source.getEpochs({ page: 1, limit: 2 });
    expect(first.items.map((e) => e.epoch)).toEqual([9, 8]);
    expect(first.hasMore).toBe(true);
    const second = await source.getEpochs({ page: 2, limit: 2 });
    expect(second.items.map((e) => e.epoch)).toEqual([7]);
    expect(second.hasMore).toBe(false);
  });

  it("TestGetEpochs_an_index_with_no_closed_epoch_is_an_empty_page_not_an_error", async () => {
    const { gw, source } = await setup();
    gw.on("index/epochs?page=1&limit=20", { page: 1, limit: 20, epochs: null });
    expect(await source.getEpochs({ page: 1, limit: 20 })).toEqual({ items: [], page: 1, limit: 20, hasMore: false });
  });

  it("TestGetEpochs_asks_the_indexer_only_for_values_it_accepts", async () => {
    const { gw, source } = await setup();
    gw.on("index/epochs?page=1000&limit=100", { epochs: [] });
    await source.getEpochs({ page: 99999, limit: 5000 });
    expect(gw.asked).toContain("index/epochs?page=1000&limit=100");
  });
});

describe("getEpoch", () => {
  it("TestGetEpoch_reads_a_closed_epoch", async () => {
    const { gw, source } = await setup();
    gw.on("index/epochs/12", epochJson(12));
    expect((await source.getEpoch(12))?.endHeight).toBe(1200);
  });

  it("TestGetEpoch_an_epoch_the_index_does_not_hold_is_null", async () => {
    const { source } = await setup();
    expect(await source.getEpoch(99)).toBeNull();
  });

  it("TestGetEpoch_a_number_that_is_no_epoch_is_null_without_asking", async () => {
    const { gw, source } = await setup();
    for (const n of [0, -1, 1.5, Number.NaN]) expect(await source.getEpoch(n)).toBeNull();
    expect(gw.asked).toEqual([]);
  });

  it("TestGetEpoch_an_answer_for_another_epoch_is_refused", async () => {
    const { gw, source } = await setup();
    gw.on("index/epochs/12", epochJson(13));
    await expect(source.getEpoch(12)).rejects.toThrow("different epoch");
  });
});

describe("getEpochValidators", () => {
  it("TestGetEpochValidators_names_the_ones_the_chain_still_knows_and_shortens_the_rest", async () => {
    const { gw, source } = await setup();
    gw.on("index/epochs/12/validators?page=1&limit=20", {
      epoch: 12,
      validators: [row(valoper(7)), row(valoper(99), { comet_power: 5, slashes: 1, slashed_burned: "77", jailed: true, status: "unbonding" })],
    });
    const page = await source.getEpochValidators(12, { page: 1, limit: 20 });
    expect(page?.items[0]).toMatchObject({ ref: { moniker: "Alpha", operator: addr(7) }, power: 60, uptimeBps: 9000, jailed: false });
    expect(page?.items[1]?.ref.moniker).toBe(`${addr(99).slice(0, 10)}…${addr(99).slice(-4)}`);
    expect(page?.items[1]).toMatchObject({ slashes: 1, slashedBurned: "77", jailed: true, status: "unbonding" });
  });

  it("TestGetEpochValidators_an_epoch_not_closed_in_the_index_is_null", async () => {
    const { source } = await setup();
    expect(await source.getEpochValidators(500, { page: 1, limit: 20 })).toBeNull();
  });

  it("TestReadEpochValidator_refuses_an_uptime_above_100_percent_and_a_bad_operator", () => {
    const dir = { byOperator: new Map() } as never;
    expect(() => readEpochValidator(row(valoper(7), { uptime_bps: 10_001 }), dir)).toThrow("uptime");
    expect(() => readEpochValidator(row("oramavaloper1nonsense"), dir)).toThrow("operator");
  });
});

describe("getSupplyHistory", () => {
  it("TestGetSupplyHistory_reads_points_newest_first", async () => {
    const { gw, source } = await setup();
    gw.on("index/supply?page=1&limit=100", { page: 1, limit: 100, points: [supplyJson(3), supplyJson(2)] });
    const points = await source.getSupplyHistory(500);
    expect(points.map((p) => p.epoch)).toEqual([3, 2]);
  });
});

describe("getEpochClock", () => {
  it("TestGetEpochClock_reads_the_epoch_in_progress_and_its_closing_rule", async () => {
    const { gw, source } = await setup();
    gw.on("query/orama.emission.v1.Query/CurrentEpoch", {
      epoch_state: { current_epoch: "12", epoch_start_unix_nano: String(Date.parse("2026-10-08T06:00:00Z") * 1_000_000), blocks_in_epoch: "321" },
    });
    gw.on("query/orama.emission.v1.Query/Params", { params: { epoch_duration_seconds: "86400", min_blocks_per_epoch: "14400" } });
    expect(await source.getEpochClock()).toEqual({
      epoch: 12,
      startedAt: "2026-10-08T06:00:00.000Z",
      durationSeconds: 86_400,
      minBlocks: 14_400,
      blocksInEpoch: 321,
      readAtHeight: HEAD,
    });
  });

  it("TestGetEpochClock_a_fresh_epoch_has_no_block_count_yet", async () => {
    const { gw, source } = await setup();
    gw.on("query/orama.emission.v1.Query/Params", { params: { epoch_duration_seconds: "300", min_blocks_per_epoch: "10" } });
    expect((await source.getEpochClock()).blocksInEpoch).toBe(0);
  });

  it("TestGetEpochClock_refuses_a_zero_length_epoch", async () => {
    const { gw, source } = await setup();
    gw.on("query/orama.emission.v1.Query/Params", { params: { epoch_duration_seconds: "0", min_blocks_per_epoch: "10" } });
    await expect(source.getEpochClock()).rejects.toThrow("malformed epoch");
  });
});

describe("getBaseFee", () => {
  it("TestGetBaseFee_reads_the_current_value", async () => {
    const { source } = await setup();
    expect(await source.getBaseFee()).toBe(1000);
  });
});

describe("a node with no indexer", () => {
  it("TestIndexRoutes_a_503_from_a_node_with_no_indexer_is_a_clear_error_and_the_chain_reads_still_work", async () => {
    const gw = await chainFixture();
    gw.on("query/orama.emission.v1.Query/Params", { params: { epoch_duration_seconds: "86400", min_blocks_per_epoch: "14400" } });
    const source = createChainSource({
      fetcher: async (url) =>
        url.includes("/v1/chain/index/")
          ? new Response("this node runs no chain indexer; install it with orama global install --services chain,indexer\n", { status: 503 })
          : gw.fetch(url),
    });
    await expect(source.getEpochs({ page: 1, limit: 20 })).rejects.toBeInstanceOf(ChainIndexUnavailable);
    await expect(source.getEpoch(3)).rejects.toBeInstanceOf(ChainIndexUnavailable);
    await expect(source.getEpochValidators(3, { page: 1, limit: 20 })).rejects.toBeInstanceOf(ChainIndexUnavailable);
    await expect(source.getSupplyHistory(10)).rejects.toBeInstanceOf(ChainIndexUnavailable);
    expect((await source.getBaseFee())).toBe(1000);
    expect((await source.getEpochClock()).epoch).toBe(12);
  });
});

function row(operator: string, over: Record<string, unknown> = {}) {
  return {
    epoch: 12,
    operator,
    consensus_address: "AB".repeat(20),
    comet_power: 60,
    signed_blocks: 90,
    missed_blocks: 10,
    uptime_bps: 9000,
    slashes: 0,
    slashed_burned: "0",
    jailed: false,
    status: "bonded",
    ...over,
  };
}
