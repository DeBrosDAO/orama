import { describe, expect, it } from "vitest";
import { SHIELDED_POOL_ADDRESS } from "../../model/shielded";
import { bech32Decode } from "../../model/bech32";
import { chainPath, createClient, withQuery } from "./client";
import { CURSOR_A, FakeGateway, addr, indexTx } from "./fake-gateway";
import { queryPath } from "./module-query";
import { loadShieldedActivity, loadShieldedActivityTotal, loadShieldedPool } from "./shielded";

const client = (gw: FakeGateway) => createClient(gw.fetch, "https://gw.example/v1/chain");
const bundleEvent = (kind: string, actions: number, amount: string) => ({
  type: "shielded_bundle",
  attributes: [{ key: "kind", value: kind }, { key: "actions", value: String(actions) }, { key: "amount", value: amount }],
});

describe("the pool's account", () => {
  it("TestShieldedPoolAddress_is_the_sha256_of_the_module_name_as_the_chain_derives_it", async () => {
    const digest = new Uint8Array(await crypto.subtle.digest("SHA-256", new TextEncoder().encode("shielded")));
    const decoded = bech32Decode(SHIELDED_POOL_ADDRESS);
    expect(decoded?.hrp).toBe("orama");
    expect(Array.from(decoded?.bytes ?? [])).toEqual(Array.from(digest.slice(0, 20)));
  });
});

describe("loadShieldedPool", () => {
  const params = { anchor_window_blocks: "14400", nullifier_fee: "1000000", action_gas: "250000", max_actions_per_bundle: 16, unshield_floor: "1000000000", max_fee_topup: "10000000" };

  it("TestLoadShieldedPool_reads_what_the_pool_account_holds_the_tree_counters_and_the_rules", async () => {
    const gw = new FakeGateway()
      .on(queryPath("orama.shielded.v1.Query/Params"), { params })
      .on(queryPath("orama.shielded.v1.Query/TreeState"), { tree_size: "12", current_root: "AA==", anchors: "5", nullifier_accumulator: "AA==", nullifier_count: "7" })
      .on(queryPath("cosmos.bank.v1beta1.Query/Balance", { address: SHIELDED_POOL_ADDRESS, denom: "norama" }), { balance: { denom: "norama", amount: "3500000000" } });
    const pool = await loadShieldedPool(client(gw));
    expect(pool).toMatchObject({ held: "3500000000", noteCommitments: "12", nullifiers: "7" });
    expect(pool.params).toEqual({ nullifierFee: "1000000", maxActionsPerBundle: 16, unshieldFloor: "1000000000", maxFeeTopup: "10000000" });
  });

  it("TestLoadShieldedPool_a_query_that_fails_rejects_the_whole_read", async () => {
    const gw = new FakeGateway().on(queryPath("orama.shielded.v1.Query/Params"), { params });
    await expect(loadShieldedPool(client(gw))).rejects.toThrow();
  });
});

describe("loadShieldedActivity", () => {
  const txsPath = withQuery(chainPath("index", "accounts", SHIELDED_POOL_ADDRESS, "txs"), { page: 1, limit: 3 });
  const summaryPath = chainPath("index", "accounts", SHIELDED_POOL_ADDRESS);

  it("TestLoadShieldedActivity_reads_the_bundle_of_each_transaction_and_has_no_signer_for_a_private_transfer", async () => {
    const gw = new FakeGateway()
      .on(txsPath, {
        page: 1,
        limit: 3,
        next_cursor: CURSOR_A,
        txs: [
          indexTx(1, { signer: "", messages: ["/orama.shielded.v1.MsgShieldedTransfer"], body: [{ "@type": "/orama.shielded.v1.MsgShieldedTransfer" }], events: [bundleEvent("transfer", 2, "700000")] }),
          indexTx(2, { signer: addr(1), messages: ["/orama.shielded.v1.MsgUnshield"], body: [{ "@type": "/orama.shielded.v1.MsgUnshield" }], events: [bundleEvent("unshield", 1, "2000000000")] }),
          indexTx(3, { code: 11, log: "refused", events: [] }),
        ],
      })
      .on(summaryPath, { address: SHIELDED_POOL_ADDRESS, tx_count: 41, first_seen: "2026-10-01T00:00:00Z", last_active: "2026-10-08T00:00:00Z" });
    const activity = await loadShieldedActivity(client(gw), { key: null, limit: 3 });
    expect(await loadShieldedActivityTotal(client(gw))).toBe(41);
    expect(activity.nextKey).toBe(CURSOR_A);
    expect(activity.items.map((t) => [t.bundle?.kind ?? null, t.bundle?.amount ?? null, t.signer?.address ?? null, t.ok])).toEqual([
      ["transfer", "700000", null, true],
      ["unshield", "2000000000", addr(1), true],
      [null, null, addr(1), false],
    ]);
  });

  it("TestLoadShieldedActivity_a_pool_nobody_has_touched_is_an_empty_list_with_no_total", async () => {
    const gw = new FakeGateway().on(txsPath, { page: 1, limit: 3, txs: [] });
    expect(await loadShieldedActivity(client(gw), { key: null, limit: 3 })).toMatchObject({ items: [], nextKey: null });
    expect(await loadShieldedActivityTotal(client(gw))).toBeNull();
  });

  it("TestLoadShieldedActivity_a_later_page_is_asked_by_the_cursor_the_page_before_it_named", async () => {
    const gw = new FakeGateway().on(withQuery(chainPath("index", "accounts", SHIELDED_POOL_ADDRESS, "txs"), { cursor: CURSOR_A, limit: 3 }), {
      limit: 3,
      txs: [indexTx(1)],
    });
    const page = await loadShieldedActivity(client(gw), { key: CURSOR_A, limit: 3 });
    expect(page.items).toHaveLength(1);
    expect(page.nextKey).toBeNull();
    expect(gw.asked).toEqual([`index/accounts/${SHIELDED_POOL_ADDRESS}/txs?cursor=${CURSOR_A}&limit=3`]);
  });

  it("TestLoadShieldedActivity_a_full_page_with_no_cursor_or_a_malformed_one_is_refused", async () => {
    const full = [indexTx(1), indexTx(2), indexTx(3)];
    for (const next_cursor of [undefined, "", "short", "has+plus/slash!!!", `${CURSOR_A}A`]) {
      const gw = new FakeGateway().on(txsPath, { page: 1, limit: 3, txs: full, next_cursor });
      await expect(loadShieldedActivity(client(gw), { key: null, limit: 3 })).rejects.toThrow(/needs its cursor/);
    }
  });
});
