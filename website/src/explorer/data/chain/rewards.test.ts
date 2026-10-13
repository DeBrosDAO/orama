import { describe, expect, it } from "vitest";
import { addr, chainFixture, FakeStream, valoper } from "./fake-gateway";
import { createChainSource } from "./source";
import { addressQuery } from "./wallet";

async function setup() {
  const gw = await chainFixture();
  return { gw, source: createChainSource({ fetcher: gw.fetch, eventSource: new FakeStream().factory }) };
}

const reward = (epoch: number, who: number, over: Record<string, unknown> = {}) => ({
  epoch,
  operator: valoper(who),
  amount: "1000",
  commission: "100",
  operator_share: "730",
  delegators: "270",
  force_bonded: "0",
  ...over,
});

describe("getEpochRewards", () => {
  it("TestGetEpochRewards_names_the_validators_and_splits_commission_from_the_delegators", async () => {
    const { gw, source } = await setup();
    gw.on("index/epochs/9/rewards?page=1&limit=25", { epoch: 9, recorded: true, page: 1, limit: 25, rewards: [reward(9, 7), reward(9, 8, { amount: "500", commission: "0", operator_share: "500", delegators: "0" })] });
    const rewards = await source.getEpochRewards(9, { page: 1, limit: 25 });
    expect(rewards?.recorded).toBe(true);
    expect(rewards?.items).toHaveLength(2);
    expect(rewards?.items[0]).toEqual({
      epoch: 9,
      ref: { moniker: "Alpha", operator: addr(7) },
      amount: "1000",
      commission: "100",
      operatorShare: "730",
      delegators: "270",
      forceBonded: "0",
    });
    expect(rewards?.hasMore).toBe(false);
  });

  it("TestGetEpochRewards_an_epoch_before_the_chain_reported_them_is_not_recorded_not_zero", async () => {
    const { gw, source } = await setup();
    gw.on("index/epochs/2/rewards?page=1&limit=25", { epoch: 2, recorded: false, page: 1, limit: 25, rewards: [] });
    const rewards = await source.getEpochRewards(2, { page: 1, limit: 25 });
    expect(rewards).toMatchObject({ epoch: 2, recorded: false, items: [] });
  });

  it("TestGetEpochRewards_a_validator_the_directory_does_not_know_is_still_shown_by_its_address", async () => {
    const { gw, source } = await setup();
    gw.on("index/epochs/9/rewards?page=1&limit=25", { epoch: 9, recorded: true, rewards: [reward(9, 30)] });
    const rewards = await source.getEpochRewards(9, { page: 1, limit: 25 });
    expect(rewards?.items[0]?.ref.operator).toBe(addr(30));
  });

  it("TestGetEpochRewards_an_epoch_not_closed_in_the_index_is_null", async () => {
    const { source } = await setup();
    expect(await source.getEpochRewards(99, { page: 1, limit: 25 })).toBeNull();
    expect(await source.getEpochRewards(0, { page: 1, limit: 25 })).toBeNull();
    expect(await source.getEpochRewards(1.5, { page: 1, limit: 25 })).toBeNull();
  });

  it("TestGetEpochRewards_pages_and_limits_are_clamped_to_the_indexers_bounds", async () => {
    const { gw, source } = await setup();
    gw.on("index/epochs/9/rewards?page=1000&limit=100", { epoch: 9, recorded: true, rewards: [] });
    expect((await source.getEpochRewards(9, { page: 5000, limit: 500 }))?.items).toEqual([]);
    expect(gw.asked[0]).toBe("index/epochs/9/rewards?page=1000&limit=100");
  });

  it("TestGetEpochRewards_a_full_page_has_more", async () => {
    const { gw, source } = await setup();
    gw.on("index/epochs/9/rewards?page=1&limit=2", { epoch: 9, recorded: true, rewards: [reward(9, 7), reward(9, 8)] });
    expect((await source.getEpochRewards(9, { page: 1, limit: 2 }))?.hasMore).toBe(true);
  });

  it.each([
    ["parts that do not add up", { operator_share: "800" }],
    ["an amount that is not a number", { amount: "lots" }],
    ["a negative part", { delegators: "-1" }],
    ["an operator that is no address", { operator: "nobody" }],
    ["a missing commission", { commission: undefined }],
  ])("TestGetEpochRewards_refuses_%s", async (_name, over) => {
    const { gw, source } = await setup();
    gw.on("index/epochs/9/rewards?page=1&limit=25", { epoch: 9, recorded: true, rewards: [reward(9, 7, over)] });
    await expect(source.getEpochRewards(9, { page: 1, limit: 25 })).rejects.toThrow(/malformed|do not add up/);
  });

  it("TestGetEpochRewards_rewards_for_an_epoch_that_says_it_is_not_recorded_are_refused", async () => {
    const { gw, source } = await setup();
    gw.on("index/epochs/9/rewards?page=1&limit=25", { epoch: 9, recorded: false, rewards: [reward(9, 7)] });
    await expect(source.getEpochRewards(9, { page: 1, limit: 25 })).rejects.toThrow(/did not record/);
  });

  it("TestGetEpochRewards_a_node_failure_is_a_readable_error", async () => {
    const { gw, source } = await setup();
    gw.failing.add("index/epochs/9/rewards?page=1&limit=25");
    await expect(source.getEpochRewards(9, { page: 1, limit: 25 })).rejects.toThrow("The chain could not be read (HTTP 500).");
  });
});

describe("getValidatorRewards", () => {
  const path = (page = 1, limit = 20) => `index/validators/${valoper(7)}/rewards?page=${page}&limit=${limit}`;

  it("TestGetValidatorRewards_each_epoch_is_recorded_with_a_reward_or_not_recorded_or_paid_nothing", async () => {
    const { gw, source } = await setup();
    gw.on(path(), {
      rewards: [
        { epoch: 9, recorded: true, reward: reward(9, 7) },
        { epoch: 8, recorded: true, reward: null },
        { epoch: 7, recorded: false, reward: null },
      ],
    });
    const history = await source.getValidatorRewards(addr(7), { page: 1, limit: 20 });
    expect(history?.items.map((i) => [i.epoch, i.recorded, i.reward?.amount ?? null])).toEqual([
      [9, true, "1000"],
      [8, true, null],
      [7, false, null],
    ]);
  });

  it("TestGetValidatorRewards_an_unknown_validator_or_one_the_index_has_not_seen_is_null", async () => {
    const { source } = await setup();
    expect(await source.getValidatorRewards(addr(99), { page: 1, limit: 20 })).toBeNull();
    expect(await source.getValidatorRewards(addr(7), { page: 1, limit: 20 })).toBeNull();
  });

  it("TestGetValidatorRewards_an_empty_history_is_an_empty_list", async () => {
    const { gw, source } = await setup();
    gw.on(path(), { rewards: null });
    expect((await source.getValidatorRewards(addr(7), { page: 1, limit: 20 }))?.items).toEqual([]);
  });

  it("TestGetValidatorRewards_a_reward_in_an_epoch_that_is_not_recorded_is_refused", async () => {
    const { gw, source } = await setup();
    gw.on(path(), { rewards: [{ epoch: 9, recorded: false, reward: reward(9, 7) }] });
    await expect(source.getValidatorRewards(addr(7), { page: 1, limit: 20 })).rejects.toThrow(/did not record/);
  });

  it("TestGetValidatorRewards_an_address_is_read_in_any_case", async () => {
    const { gw, source } = await setup();
    gw.on(path(), { rewards: [] });
    expect(await source.getValidatorRewards(addr(7).toUpperCase(), { page: 1, limit: 20 })).not.toBeNull();
  });
});

describe("getAccountRewards", () => {
  const path = (a: string) => `index/accounts/${a}/rewards?page=1&limit=20`;
  const FIRST = { limit: 20, key: null };
  /** A cursor of the indexer's rewards list: epoch 7 and an operator address, 79 characters. */
  const CURSOR = "AAAAAAAAAAdvcmFtYXZhbG9wZXIxcXFxcXFxcXFxcXFxcXFxcXFxcXFxcXFxcXFxcXFxcXFxcXFx";

  it("TestGetAccountRewards_lists_what_an_account_was_credited_and_from_which_epoch_that_is_known", async () => {
    const { gw, source } = await setup();
    gw.on(path(addr(1)), {
      recorded_from_epoch: 5,
      rewards: [
        { epoch: 9, validator: valoper(7), role: "delegator", amount: "270", force_bonded: "0" },
        { epoch: 8, validator: valoper(8), role: "operator", amount: "500", force_bonded: "100" },
      ],
    });
    const rewards = await source.getAccountRewards(addr(1), FIRST);
    expect(rewards.recordedFromEpoch).toBe(5);
    expect(rewards.items.map((i) => [i.epoch, i.validator.moniker, i.role, i.amount, i.forceBonded])).toEqual([
      [9, "Alpha", "delegator", "270", "0"],
      [8, "Beta", "operator", "500", "100"],
    ]);
  });

  it("TestGetAccountRewards_an_account_nothing_credited_is_an_empty_list_not_an_error", async () => {
    const { gw, source } = await setup();
    gw.on(path(addr(1)), { recorded_from_epoch: 0, rewards: [] });
    expect(await source.getAccountRewards(addr(1), FIRST)).toMatchObject({ recordedFromEpoch: 0, items: [], nextKey: null });
  });

  it("TestGetAccountRewards_refuses_a_role_it_does_not_know", async () => {
    const { gw, source } = await setup();
    gw.on(path(addr(1)), { rewards: [{ epoch: 9, validator: valoper(7), role: "admin", amount: "1", force_bonded: "0" }] });
    await expect(source.getAccountRewards(addr(1), FIRST)).rejects.toThrow("The chain sent a malformed reward role.");
  });

  it("TestGetAccountRewards_a_node_with_no_answer_is_a_readable_error", async () => {
    const { gw, source } = await setup();
    gw.failing.add(path(addr(1)));
    await expect(source.getAccountRewards(addr(1), FIRST)).rejects.toThrow("The chain could not be read (HTTP 500).");
  });

  it("TestGetAccountRewards_follows_the_cursor_of_a_full_page_to_a_short_one_and_never_asks_for_a_page_number_again", async () => {
    const { gw, source } = await setup();
    const credit = (epoch: number) => ({ epoch, validator: valoper(7), role: "delegator", amount: "270", force_bonded: "0" });
    gw.on(`index/accounts/${addr(1)}/rewards?page=1&limit=2`, { recorded_from_epoch: 5, rewards: [credit(9), credit(8)], next_cursor: CURSOR });
    gw.on(`index/accounts/${addr(1)}/rewards?cursor=${CURSOR}&limit=2`, { recorded_from_epoch: 5, limit: 2, rewards: [credit(7)] });
    const first = await source.getAccountRewards(addr(1), { limit: 2, key: null });
    expect(first.nextKey).toBe(CURSOR);
    const rest = await source.getAccountRewards(addr(1), { limit: 2, key: first.nextKey });
    expect(rest.items.map((i) => i.epoch)).toEqual([7]);
    expect(rest).toMatchObject({ recordedFromEpoch: 5, nextKey: null });
    expect(gw.asked.filter((p) => p.includes("/rewards"))).toEqual([
      `index/accounts/${addr(1)}/rewards?page=1&limit=2`,
      `index/accounts/${addr(1)}/rewards?cursor=${CURSOR}&limit=2`,
    ]);
  });

  it("TestGetAccountRewards_a_full_page_without_a_usable_cursor_is_refused_not_paged_by_number", async () => {
    const credit = (epoch: number) => ({ epoch, validator: valoper(7), role: "delegator", amount: "270", force_bonded: "0" });
    for (const next of [{}, { next_cursor: "AAAAAAAAAAoAAAAB" }, { next_cursor: CURSOR.slice(0, 38) }, { next_cursor: "A".repeat(581) }, { next_cursor: `${CURSOR.slice(0, 78)}+` }]) {
      const { gw, source } = await setup();
      gw.on(`index/accounts/${addr(1)}/rewards?page=1&limit=2`, { rewards: [credit(9), credit(8)], ...next });
      await expect(source.getAccountRewards(addr(1), { limit: 2, key: null })).rejects.toThrow(/cursor|number only|string/i);
    }
  });
});

describe("getEarnings", () => {
  const path = (a: string) => addressQuery("orama.fees.v1.Query/Earnings", "address", a);

  it("TestGetEarnings_is_the_balance_x_fees_holds", async () => {
    const { gw, source } = await setup();
    gw.on(path(addr(1)), { balance: "12345" });
    expect(await source.getEarnings(addr(1).toUpperCase())).toEqual({ address: addr(1), balance: "12345" });
  });

  it("TestGetEarnings_an_account_with_none_is_zero_because_protojson_leaves_the_field_out", async () => {
    const { gw, source } = await setup();
    gw.on(path(addr(2)), {});
    expect((await source.getEarnings(addr(2))).balance).toBe("0");
  });

  it("TestGetEarnings_a_balance_that_is_not_a_number_is_refused", async () => {
    const { gw, source } = await setup();
    gw.on(path(addr(3)), { balance: "-4" });
    await expect(source.getEarnings(addr(3))).rejects.toThrow("The chain sent a malformed earnings balance.");
  });
});

describe("epoch rows", () => {
  it("TestReadEpochRow_carries_the_split_when_recorded_and_nulls_when_not", async () => {
    const { gw, source } = await setup();
    const base = {
      epoch: 4, start_height: 1, end_height: 9, start_time: "2026-10-08T00:00:00Z", end_time: "2026-10-09T00:00:00Z", blocks: 9, partial: false,
      max_mintable: "10", minted_validators: "6", minted_service: "0", minted_development: "0", minted_faucet: "0", minted_total: "6",
      burned: "0", fees_collected: "0", fees_burned: "0", fees_distributed: "0", rewards_credited: "6", rewards_force_bonded: "0",
    };
    gw.on("index/epochs/4", { ...base, rewards_recorded: true, rewards_commission: "1", rewards_operator_share: "4", rewards_delegators: "2" });
    gw.on("index/epochs/5", { ...base, epoch: 5 });
    expect(await source.getEpoch(4)).toMatchObject({ rewardsRecorded: true, rewardsCommission: "1", rewardsOperatorShare: "4", rewardsDelegators: "2" });
    expect(await source.getEpoch(5)).toMatchObject({ rewardsRecorded: false, rewardsCommission: null, rewardsOperatorShare: null, rewardsDelegators: null });
  });

  it("TestReadEpochRow_a_commission_above_the_operators_share_is_refused", async () => {
    const { gw, source } = await setup();
    gw.on("index/epochs/4", {
      epoch: 4, start_height: 1, end_height: 9, start_time: "2026-10-08T00:00:00Z", end_time: "2026-10-09T00:00:00Z", blocks: 9, partial: false,
      max_mintable: "10", minted_validators: "6", minted_service: "0", minted_development: "0", minted_faucet: "0", minted_total: "6",
      burned: "0", fees_collected: "0", fees_burned: "0", fees_distributed: "0", rewards_credited: "6", rewards_force_bonded: "0",
      rewards_recorded: true, rewards_commission: "9", rewards_operator_share: "4", rewards_delegators: "2",
    });
    await expect(source.getEpoch(4)).rejects.toThrow(/commission above/);
  });
});
