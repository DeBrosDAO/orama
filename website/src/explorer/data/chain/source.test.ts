import { afterEach, describe, expect, it, vi } from "vitest";
import { addr, CHAIN_ID, chainFixture, CURSOR_A, CURSOR_B, FakeGateway, FakeStream, HEAD, HEAD_TIME, hashOf, homeJson, indexTx, rpc, stakingValidator, valoper } from "./fake-gateway";
import { createChainSource } from "./source";
import { addressQuery } from "./wallet";

async function setup() {
  const gw = await chainFixture();
  const stream = new FakeStream();
  return { gw, stream, source: createChainSource({ fetcher: gw.fetch, eventSource: stream.factory }) };
}

/** Lets the promise chains an event started finish. */
async function flush() {
  for (let i = 0; i < 20; i++) await Promise.resolve();
}

afterEach(() => {
  vi.useRealTimers();
});

describe("head and network", () => {
  it("TestGetHead_is_the_head_the_stream_opens_with", async () => {
    const { source, stream } = await setup();
    expect(await source.getHead()).toEqual({ height: HEAD, time: HEAD_TIME.replace("Z", ".000Z"), chainId: CHAIN_ID });
    expect(stream.opened).toEqual(["https://stagenet.orama.network/v1/chain/head/stream"]);
    expect(stream.open).toBe(0);
  });

  it("TestGetNetwork_is_one_read_of_the_home_snapshot_and_the_head", async () => {
    const { gw, source } = await setup();
    const n = await source.getNetwork();
    expect(gw.asked).toEqual(["index/home"]);
    expect(n).toMatchObject({
      chainId: "orama-test-1",
      height: HEAD,
      validatorsSigning: 2,
      validatorsTotal: 3,
      supply: "41000000000000000",
      baseFee: 1000,
      transactions24h: 360,
      failed24h: 24,
      burned24h: "4800",
      blockTimeSeconds: 2,
      index: { cursor: HEAD, tip: HEAD, lag: 0, stuck: null },
    });
    expect(n.transactionsChangePct).toBe(50);
    expect(n.transactionsSeries).toHaveLength(24);
    expect(n.epoch.number).toBe(12);
    expect(n.epoch.progress).toBeCloseTo(0.25);
    expect(n.epoch.endsAt).toBe("2026-10-09T06:00:00.000Z");
  });

  it("TestGetNetwork_the_tiles_feed_blocks_and_examples_share_one_read", async () => {
    const { gw, source } = await setup();
    await Promise.all([source.getNetwork(), source.getRecentBlocks(24), source.getLatestActivity("all", 12), source.getExamples()]);
    expect(gw.asked.filter((p) => p === "index/home")).toHaveLength(1);
  });

  it("TestGetNetwork_a_fresh_index_has_no_baseline_to_compare_with", async () => {
    const { gw, source } = await setup();
    gw.on("index/home", homeJson({ hours: [{ hour: "2026-10-08T12:00:00Z", txs: 4, failed: 0, burned: "9" }] }));
    const n = await source.getNetwork();
    expect(n.transactionsChangePct).toBeNull();
    expect(n.transactions24h).toBe(4);
  });

  it("TestGetNetwork_an_index_that_is_behind_says_so", async () => {
    const { gw, source } = await setup();
    gw.on("index/home", homeJson({ status: { start_height: 1, cursor: HEAD - 40, earliest: 1, tip: HEAD, lag: 40, aggregates_from: 1, stuck: { height: HEAD - 39, reason: "state pruned", state_pruned: true, since: "2026-10-08T11:00:00Z", failures: 3 } } }));
    const n = await source.getNetwork();
    expect(n.index).toEqual({ startHeight: 1, cursor: HEAD - 40, tip: HEAD, lag: 40, aggregatesFrom: 1, stuck: { height: HEAD - 39, reason: "state pruned", statePruned: true, failures: 3 } });
  });

  it("TestGetNetwork_a_node_failure_is_a_readable_error_without_the_nodes_words", async () => {
    const { gw, source } = await setup();
    gw.failing.add("index/home");
    await expect(source.getNetwork()).rejects.toThrow("The chain could not be read (HTTP 500).");
    await expect(source.getNetwork()).rejects.not.toThrow(/oramad/);
  });

  it("TestGetNetwork_a_malformed_home_is_refused_by_field", async () => {
    const { gw, source } = await setup();
    gw.on("index/home", homeJson({ supply: "lots" }));
    await expect(source.getNetwork()).rejects.toThrow("The chain sent a malformed supply.");
  });

  it("TestGetIndexStatus_reads_the_light_status_route", async () => {
    const { gw, source } = await setup();
    gw.on("index/status", { start_height: 1, cursor: HEAD - 2, earliest: 1, tip: HEAD, aggregates_from: 1 });
    expect(await source.getIndexStatus()).toEqual({ startHeight: 1, cursor: HEAD - 2, tip: HEAD, lag: 2, aggregatesFrom: 1, stuck: null });
  });
});

describe("subscribeHead", () => {
  it("TestSubscribeHead_calls_the_listener_only_when_the_head_moves_and_reports_failures", async () => {
    const { source, stream } = await setup();
    const heads: number[] = [];
    const errors: string[] = [];
    const stop = source.subscribeHead((h) => heads.push(h.height), (e) => errors.push(e.message));
    await flush();
    expect(heads).toEqual([]);

    stream.push(HEAD + 1);
    expect(heads).toEqual([HEAD + 1]);

    stream.push(HEAD + 1);
    stream.push(HEAD);
    expect(heads).toEqual([HEAD + 1]);

    stream.unavailable();
    stream.drop();
    expect(errors).toHaveLength(2);
    expect(errors[0]).toContain("The chain could not be reached. This browser could not read the gateway at stagenet.orama.network.");
    expect(errors[1]).toBe("The live connection to the chain was lost; it reconnects by itself.");

    stop();
    expect(stream.open).toBe(0);
    stream.push(HEAD + 2);
    expect(heads).toEqual([HEAD + 1]);
  });

  it("TestSubscribeHead_readers_share_one_stream", async () => {
    const { source, stream } = await setup();
    const a: number[] = [];
    const b: number[] = [];
    const stopA = source.subscribeHead((h) => a.push(h.height), () => undefined);
    const stopB = source.subscribeHead((h) => b.push(h.height), () => undefined);
    await source.getHead();
    expect(stream.opened).toHaveLength(1);
    stream.push(HEAD + 1);
    expect([a, b]).toEqual([[HEAD + 1], [HEAD + 1]]);
    stopA();
    expect(stream.open).toBe(1);
    stopB();
    expect(stream.open).toBe(0);
  });

  it("TestSubscribeHead_a_browser_with_no_event_source_says_so_and_does_not_poll", async () => {
    const gw = await chainFixture();
    const source = createChainSource({ fetcher: gw.fetch, eventSource: null });
    await expect(source.getHead()).rejects.toThrow("This browser cannot follow the chain live.");
    const errors: string[] = [];
    source.subscribeHead(() => undefined, (e) => errors.push(e.message));
    expect(errors).toEqual(["This browser cannot follow the chain live."]);
    expect(gw.asked).not.toContain("status");
  });

  it("TestSubscribeHead_a_malformed_head_event_is_an_error_not_a_head", async () => {
    const { source, stream } = await setup();
    const heads: number[] = [];
    const errors: string[] = [];
    source.subscribeHead((h) => heads.push(h.height), (e) => errors.push(e.message));
    await source.getHead();
    stream.emitRaw("head", '{"height":"x"}');
    expect(heads).toEqual([]);
    expect(errors).toEqual(["The chain sent a malformed block height."]);
  });
});

describe("subscribeHead across a reset", () => {
  it("TestSubscribeHead_a_new_chain_id_is_announced_although_its_height_is_lower", async () => {
    const { source, stream } = await setup();
    const heads: { height: number; chainId: string }[] = [];
    const stop = source.subscribeHead((h) => heads.push({ height: h.height, chainId: h.chainId }), () => undefined);
    await flush();

    stream.push(3, "orama-test-2");
    expect(heads).toEqual([{ height: 3, chainId: "orama-test-2" }]);

    stream.push(2, "orama-test-2");
    expect(heads).toHaveLength(1);
    stop();
  });
});

describe("validators", () => {
  it("TestGetValidators_joins_staking_comet_and_power", async () => {
    const { source } = await setup();
    const set = await source.getValidators();
    expect(set.lambda).toBe(0.25);
    expect(set.handover.lambda).toBe(0.25);
    expect(set.totalStaked).toBe("7000000000000");
    expect(set.jailed).toBe(1);
    expect(set.nakamoto).toBe(1);
    const byName = Object.fromEntries(set.validators.map((v) => [v.ref.moniker, v]));
    expect(byName.Alpha).toMatchObject({ type: "committee", power: 0.6, jailed: false, ref: { operator: addr(7) } });
    expect(byName.Beta).toMatchObject({ type: "community", power: 0.4, jailed: true });
  });

  it("TestGetValidators_a_moniker_cannot_carry_a_bidi_override", async () => {
    const { source } = await setup();
    const names = (await source.getValidators()).validators.map((v) => v.ref.moniker);
    expect(names).toContain("Beta");
  });

  it("TestSearchLabels_ranks_a_prefix_before_a_match_inside_a_name", async () => {
    const { gw, source } = await setup();
    gw.on("staking/validators", {
      validators: [
        stakingValidator(7, 11, { description: { moniker: "Big Alpha" } }),
        stakingValidator(8, 12, { description: { moniker: "Alphabet" } }),
      ],
    });
    const found = await source.searchLabels("alpha", 5);
    expect(found.map((w) => w.label)).toEqual(["Alphabet", "Big Alpha"]);
    expect(await source.searchLabels("", 5)).toEqual([]);
    expect(await source.searchLabels("alpha", 0)).toEqual([]);
  });
});

describe("blocks", () => {
  it("TestGetBlock_above_the_head_or_not_a_height_is_null", async () => {
    const { source } = await setup();
    expect(await source.getBlock(HEAD + 1)).toBeNull();
    expect(await source.getBlock(0)).toBeNull();
    expect(await source.getBlock(1.5)).toBeNull();
  });

  it("TestGetBlock_the_head_has_no_signatures_yet", async () => {
    const { gw, source } = await setup();
    gw.on(`index/blocks/${HEAD}`, { hash: "ab".repeat(32), tx_count: 1, tx_hashes: [hashOf(1)], gas_used: 80000, burned: "100", height: HEAD });
    gw.on(`index/txs/${hashOf(1)}`, indexTx(1, { height: HEAD }));
    const block = await source.getBlock(HEAD);
    expect(block?.signatures).toBeNull();
    expect(block).toMatchObject({ height: HEAD, txCount: 1, gasUsed: 80000, burned: "100", proposer: { moniker: "Alpha" } });
    expect(block?.txs).toHaveLength(1);
  });

  it("TestGetBlock_signatures_come_from_the_next_blocks_last_commit", async () => {
    const { gw, source } = await setup();
    gw.on(`index/blocks/${HEAD - 1}`, { hash: "ab".repeat(32), tx_count: 0, tx_hashes: [], gas_used: 0, burned: "0" });
    gw.on(`block?height=${HEAD - 1}`, rpc({ block: { header: { height: String(HEAD - 1), time: HEAD_TIME, proposer_address: "00".repeat(20) } } }));
    const block = await source.getBlock(HEAD - 1);
    expect(block?.signatures).toEqual({ signed: 2, total: 3 });
    expect(block?.proposer).toBeNull();
  });

  it("TestGetBlock_a_block_the_indexer_has_not_reached_is_an_error_not_a_missing_block", async () => {
    const { gw, source } = await setup();
    gw.on(`block?height=${HEAD - 5}`, rpc({ block: { header: { height: String(HEAD - 5), time: HEAD_TIME, proposer_address: "00".repeat(20) } } }));
    gw.on(`block?height=${HEAD - 4}`, rpc({ block: { last_commit: { signatures: [] } } }));
    await expect(source.getBlock(HEAD - 5)).rejects.toThrow(/not on the chain/);
  });

  it("TestGetRecentBlocks_the_newest_few_come_from_the_home_snapshot", async () => {
    const { gw, source } = await setup();
    const blocks = await source.getRecentBlocks(24);
    expect(blocks).toHaveLength(24);
    expect(blocks[0]).toMatchObject({ height: HEAD, txCount: 0, proposer: null });
    expect(blocks.map((b) => b.height)).toEqual([...blocks.map((b) => b.height)].sort((a, b) => b - a));
    expect(gw.asked).toEqual(["index/home"]);
    expect(await source.getRecentBlocks(0)).toEqual([]);
    expect(await source.getRecentBlocks(3)).toHaveLength(3);
  });

  it("TestGetRecentBlocks_more_than_the_snapshot_pages_through_the_block_list", async () => {
    const { gw, source } = await setup();
    const page = (from: number, count: number) => ({
      blocks: Array.from({ length: count }, (_, i) => ({ height: from - i, hash: "cd".repeat(32), time: HEAD_TIME, proposer: "ab".repeat(20), tx_count: 0, gas_used: 0, burned: "0", commit_signed: 0, commit_size: 0 })),
    });
    gw.on("index/blocks?page=1&limit=100", { ...page(1000, 100), next_cursor: CURSOR_A });
    gw.on(`index/blocks?cursor=${CURSOR_A}&limit=100`, page(900, 20));
    const blocks = await source.getRecentBlocks(150);
    expect(blocks).toHaveLength(120);
    expect(blocks[119]?.height).toBe(900 - 19);
    expect(gw.asked).toEqual(["index/blocks?page=1&limit=100", `index/blocks?cursor=${CURSOR_A}&limit=100`]);
  });

  it("TestGetRecentBlocks_a_full_page_without_a_cursor_is_refused_not_paged_by_number", async () => {
    const { gw, source } = await setup();
    const full = Array.from({ length: 100 }, (_, i) => ({ height: 1000 - i, hash: "cd".repeat(32), time: HEAD_TIME, proposer: "ab".repeat(20), tx_count: 0, gas_used: 0, burned: "0", commit_signed: 0, commit_size: 0 }));
    gw.on("index/blocks?page=1&limit=100", { blocks: full });
    await expect(source.getRecentBlocks(150)).rejects.toThrow(/needs its cursor/);
    expect(gw.asked).not.toContain("index/blocks?page=2&limit=100");
  });
});

describe("transactions", () => {
  it("TestGetTx_unknown_hash_is_null_not_an_error", async () => {
    const { source } = await setup();
    expect(await source.getTx(hashOf(99))).toBeNull();
  });

  it("TestGetTx_builds_the_page_from_the_indexed_record", async () => {
    const { gw, source } = await setup();
    const tx = indexTx(1, {
      events: [
        { type: "tx", attributes: [{ key: "base_fee", value: "100" }, { key: "tip", value: "5" }] },
        { type: "coin_spent", attributes: [{ key: "spender", value: addr(1) }, { key: "amount", value: "5000000105norama" }] },
        { type: "coin_received", attributes: [{ key: "receiver", value: addr(2) }, { key: "amount", value: "5000000000norama" }] },
        { type: "coin_received", attributes: [{ key: "receiver", value: addr(5) }, { key: "amount", value: "105norama" }] },
        { type: "burn", attributes: [{ key: "burner", value: addr(5) }, { key: "amount", value: "100norama" }] },
        { type: "coin_spent", attributes: [{ key: "spender", value: addr(5) }, { key: "amount", value: "100norama" }] },
      ],
    });
    gw.on(`index/txs/${hashOf(1)}`, tx);
    gw.on(`index/blocks/${HEAD - 1}`, { tx_count: 4 });
    gw.on(`index/accounts/${addr(1)}/txs?limit=100`, { txs: [tx, indexTx(0, { height: HEAD - 3 })] });
    const detail = await source.getTx(hashOf(1).toUpperCase());
    expect(detail?.fee).toEqual({ burned: "100", tip: "5", gasUsed: 80000, gasWanted: 200000 });
    expect(detail?.messages[0]).toMatchObject({ type: "send", amount: "5000000000" });
    expect(detail?.context.otherInBlock).toBe(3);
    expect(detail?.context.previousFromSigner?.hash).toBe(hashOf(0).toUpperCase());
    expect(detail?.context.amountPercentile).toBeNull();
    expect(detail?.balanceChanges.reduce((n, c) => n + BigInt(c.delta), 0n)).toBe(0n);
    expect(JSON.parse(detail?.rawJson ?? "{}").body.messages[0]["@type"]).toBe("/cosmos.bank.v1beta1.MsgSend");
  });

  it("TestGetLatestActivity_the_newest_few_come_from_the_home_snapshot", async () => {
    const { gw, source } = await setup();
    expect((await source.getLatestActivity("all", 2)).map((t) => t.hash)).toEqual([hashOf(1).toUpperCase(), hashOf(2).toUpperCase()]);
    expect(await source.getLatestActivity("all", 0)).toEqual([]);
    expect(gw.asked.filter((p) => p.startsWith("index/txs"))).toEqual([]);
  });

  it("TestGetLatestActivity_a_filter_is_the_indexers_own_list", async () => {
    const { gw, source } = await setup();
    const failed = indexTx(2, { code: 5, log: "insufficient funds" });
    gw.on("index/txs?filter=failed&limit=10", { txs: [failed] });
    gw.on("index/txs?filter=staking&limit=10", { txs: [] });
    gw.on("index/txs?limit=100", { txs: [indexTx(1), failed] });
    const rows = await source.getLatestActivity("failed", 10);
    expect(rows.map((t) => t.hash)).toEqual([hashOf(2).toUpperCase()]);
    expect(rows[0]?.status).toEqual({ ok: false, reason: "insufficient funds" });
    expect(await source.getLatestActivity("staking", 10)).toEqual([]);
    expect((await source.getLatestActivity("all", 500)).map((t) => t.hash)).toEqual([hashOf(1).toUpperCase(), hashOf(2).toUpperCase()]);
    expect(gw.asked).toContain("index/txs?limit=100");
  });

  it("TestGetLatestActivity_a_list_the_index_cannot_give_is_an_error", async () => {
    const { gw, source } = await setup();
    gw.failing.add("index/txs?filter=transfers&limit=5");
    await expect(source.getLatestActivity("transfers", 5)).rejects.toThrow("The chain could not be read (HTTP 500).");
  });

  it("TestGetExamples_come_from_the_home_snapshot", async () => {
    const { source } = await setup();
    expect(await source.getExamples()).toEqual({ latestTxHash: hashOf(1).toUpperCase(), busyWalletAddress: addr(1) });
  });

  it("TestGetExamples_an_empty_chain_has_none", async () => {
    const { gw, source } = await setup();
    gw.on("index/home", homeJson({ txs: [], examples: { latest_tx: "", busy_account: "" } }));
    expect(await source.getExamples()).toEqual({ latestTxHash: null, busyWalletAddress: null });
  });
});

describe("wallets", () => {
  const holds = (gw: FakeGateway, who: string, bank: string, staked: string, unbonding: string) => {
    gw.on(addressQuery("cosmos.bank.v1beta1.Query/AllBalances", "address", who), { balances: bank === "0" ? [] : [{ denom: "norama", amount: bank }, { denom: "factory/x/gold", amount: "99" }] });
    gw.on(addressQuery("cosmos.staking.v1beta1.Query/DelegatorDelegations", "delegator_addr", who), { delegation_responses: staked === "0" ? [] : [{ delegation: {}, balance: { denom: "norama", amount: staked } }] });
    gw.on(addressQuery("cosmos.staking.v1beta1.Query/DelegatorUnbondingDelegations", "delegator_addr", who), { unbonding_responses: unbonding === "0" ? [] : [{ entries: [{ balance: unbonding }, { balance: "1" }] }] });
  };

  it("TestGetWallet_an_address_nothing_names_and_nothing_holds_is_null", async () => {
    const { gw, source } = await setup();
    holds(gw, addr(9), "0", "0", "0");
    expect(await source.getWallet(addr(9))).toBeNull();
  });

  it("TestGetWallet_splits_the_balance_and_counts_only_norama", async () => {
    const { gw, source } = await setup();
    holds(gw, addr(1), "100", "50", "20");
    gw.on(`index/accounts/${addr(1)}`, { address: addr(1), tx_count: 7, first_seen: "2026-10-01T00:00:00Z", last_active: "2026-10-08T11:00:00Z" });
    const w = await source.getWallet(addr(1).toUpperCase());
    expect(w?.balance).toEqual({ available: "100", staked: "50", unbonding: "21", total: "171" });
    expect(w?.facts).toEqual({ firstSeen: "2026-10-01T00:00:00.000Z", lastActive: "2026-10-08T11:00:00.000Z", txCount: 7 });
    expect(w?.roles).toEqual([]);
  });

  it("TestGetWallet_funds_with_no_transaction_still_get_a_page_without_dates", async () => {
    const { gw, source } = await setup();
    holds(gw, addr(4), "10", "0", "0");
    const w = await source.getWallet(addr(4));
    expect(w?.facts).toEqual({ firstSeen: null, lastActive: null, txCount: 0 });
  });

  it("TestGetWallet_a_validator_operator_is_labelled_with_its_roles", async () => {
    const { gw, source } = await setup();
    holds(gw, addr(7), "0", "5", "0");
    const w = await source.getWallet(addr(7));
    expect(w?.ref.label).toBe("Alpha");
    expect(w?.roles).toEqual(["Validator operator", "Founding committee"]);
  });

  it("TestGetWalletActivity_pages_with_a_cursor_and_filters_from_the_wallets_side", async () => {
    const { gw, source } = await setup();
    const a = addr(1);
    const mine = Array.from({ length: 3 }, (_, i) => indexTx(10 + i));
    const incoming = indexTx(20, { signer: addr(2), body: [{ "@type": "/cosmos.bank.v1beta1.MsgSend", from_address: addr(2), to_address: a, amount: [{ denom: "norama", amount: "7" }] }] });
    gw.on(`index/accounts/${a}/txs?page=1&limit=100`, { txs: [mine[0], incoming, mine[1], mine[2]] });
    const first = await source.getWalletActivity(a, { filter: "all", counterparty: null, cursor: null, limit: 2 });
    expect(first.items.map((i) => [i.direction, i.amount])).toEqual([["out", "-5000000000"], ["in", "7"]]);
    expect(first.nextCursor).toBe(":2");
    const rest = await source.getWalletActivity(a, { filter: "all", counterparty: null, cursor: first.nextCursor, limit: 5 });
    expect(rest.items).toHaveLength(2);
    expect(rest.nextCursor).toBeNull();
    const received = await source.getWalletActivity(a, { filter: "in", counterparty: null, cursor: null, limit: 5 });
    expect(received.items.map((i) => i.hash)).toEqual([hashOf(20).toUpperCase()]);
    const withBob = await source.getWalletActivity(a, { filter: "all", counterparty: addr(2), cursor: null, limit: 5 });
    expect(withBob.items).toHaveLength(4);
    expect(withBob.items.every((i) => i.counterparty?.address === addr(2))).toBe(true);
  });

  it("TestGetWalletActivity_staking_and_failed_are_the_indexers_own_lists", async () => {
    const { gw, source } = await setup();
    const a = addr(1);
    const stake = indexTx(5, {
      messages: ["/cosmos.staking.v1beta1.MsgDelegate"],
      body: [{ "@type": "/cosmos.staking.v1beta1.MsgDelegate", delegator_address: a, validator_address: valoper(7), amount: { denom: "norama", amount: "9" } }],
    });
    gw.on(`index/accounts/${a}/txs?page=1&limit=100&filter=staking`, { txs: [stake] });
    gw.on(`index/accounts/${a}/txs?page=1&limit=100&filter=failed`, { txs: [] });
    const staking = await source.getWalletActivity(a, { filter: "staking", counterparty: null, cursor: null, limit: 5 });
    expect(staking.items.map((i) => [i.hash, i.direction, i.amount])).toEqual([[hashOf(5).toUpperCase(), "out", "-9"]]);
    expect((await source.getWalletActivity(a, { filter: "failed", counterparty: null, cursor: null, limit: 5 })).items).toEqual([]);
    expect(gw.asked.filter((p) => p.includes("/txs"))).toEqual([`index/accounts/${a}/txs?page=1&limit=100&filter=staking`, `index/accounts/${a}/txs?page=1&limit=100&filter=failed`]);
  });

  it("TestGetWalletActivity_received_and_sent_are_decided_here_from_the_whole_list", async () => {
    const { gw, source } = await setup();
    const a = addr(1);
    gw.on(`index/accounts/${a}/txs?page=1&limit=100`, { txs: [indexTx(1)] });
    await source.getWalletActivity(a, { filter: "out", counterparty: null, cursor: null, limit: 5 });
    expect(gw.asked.filter((p) => p.includes("/txs"))).toEqual([`index/accounts/${a}/txs?page=1&limit=100`]);
  });

  it("TestGetWalletActivity_a_cursor_this_source_never_issued_is_refused", async () => {
    const { source } = await setup();
    await expect(source.getWalletActivity(addr(1), { filter: "all", counterparty: null, cursor: "bogus", limit: 5 })).rejects.toThrow(/not issued/);
    await expect(source.getWalletActivity(addr(1), { filter: "all", counterparty: null, cursor: "2:0", limit: 5 })).rejects.toThrow(/not issued/);
    await expect(source.getWalletActivity(addr(1), { filter: "all", counterparty: null, cursor: `${CURSOR_A}:x`, limit: 5 })).rejects.toThrow(/not issued/);
  });

  it("TestGetWalletActivity_a_full_page_of_a_hundred_has_a_next_page_named_by_the_indexers_cursor", async () => {
    const { gw, source } = await setup();
    const a = addr(1);
    gw.on(`index/accounts/${a}/txs?page=1&limit=100`, { txs: Array.from({ length: 100 }, (_, i) => indexTx(i + 1)), next_cursor: CURSOR_A });
    const page = await source.getWalletActivity(a, { filter: "all", counterparty: null, cursor: null, limit: 100 });
    expect(page.items).toHaveLength(100);
    expect(page.nextCursor).toBe(`${CURSOR_A}:0`);
  });

  it("TestGetWalletActivity_a_deep_page_follows_the_cursor_and_resumes_inside_it", async () => {
    const { gw, source } = await setup();
    const a = addr(1);
    gw.on(`index/accounts/${a}/txs?cursor=${CURSOR_A}&limit=100`, { txs: Array.from({ length: 100 }, (_, i) => indexTx(i + 1)), next_cursor: CURSOR_B });
    const page = await source.getWalletActivity(a, { filter: "all", counterparty: null, cursor: `${CURSOR_A}:97`, limit: 3 });
    expect(page.items).toHaveLength(3);
    expect(page.nextCursor).toBe(`${CURSOR_B}:0`);
    const middle = await source.getWalletActivity(a, { filter: "all", counterparty: null, cursor: `${CURSOR_A}:10`, limit: 2 });
    expect(middle.nextCursor).toBe(`${CURSOR_A}:12`);
    const asked = `index/accounts/${a}/txs?cursor=${CURSOR_A}&limit=100`;
    expect(gw.asked.filter((p) => p.includes("/txs"))).toEqual([asked, asked]);
  });

  it("TestGetWalletActivity_a_full_page_without_a_cursor_is_refused", async () => {
    const { gw, source } = await setup();
    const a = addr(1);
    gw.on(`index/accounts/${a}/txs?page=1&limit=100`, { txs: Array.from({ length: 100 }, (_, i) => indexTx(i + 1)) });
    await expect(source.getWalletActivity(a, { filter: "all", counterparty: null, cursor: null, limit: 100 })).rejects.toThrow(/needs its cursor/);
  });

  it("TestGetCounterparties_net_and_volume_from_the_newest_transactions", async () => {
    const { gw, source } = await setup();
    const a = addr(1);
    const incoming = indexTx(20, { signer: addr(2), body: [{ "@type": "/cosmos.bank.v1beta1.MsgSend", from_address: addr(2), to_address: a, amount: [{ denom: "norama", amount: "3" }] }] });
    const toBob = (n: number, amount: string) => indexTx(n, { body: [{ "@type": "/cosmos.bank.v1beta1.MsgSend", from_address: a, to_address: addr(2), amount: [{ denom: "norama", amount }] }] });
    gw.on(`index/accounts/${a}/txs?page=1&limit=100`, { txs: [toBob(1, "10"), incoming, toBob(2, "4"), indexTx(30, { code: 5, log: "x" })] });
    const rows = await source.getCounterparties(a, 5);
    expect(rows).toEqual([{ ref: { address: addr(2) }, txCount: 3, net: "-11", volume: "17" }]);
    expect(await source.getCounterparties(a, 0)).toEqual([]);
  });
});
