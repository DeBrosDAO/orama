import { describe, expect, it } from "vitest";
import { addr } from "../data/chain/fake-gateway";
import type { ShieldedPool, ShieldedTx } from "../model/shielded";
import { SHIELDED_POOL_ADDRESS } from "../model/shielded";
import type { TxSummary } from "../model/types";
import { ActivityRows } from "../ui/module-activity";
import type { KeyPaged } from "../ui/use-key-paged";
import { IdPager } from "../ui/id-pager";
import { LookupForm } from "../ui/lookup-form";
import { render } from "./asset-fixtures";
import { ActivityCard, PageTotals } from "./shielded/activity-card";
import { PoolStats, RulesCard } from "./shielded/pool-cards";
import { summarizeWindow } from "./shielded/window-summary";
import { VisibilityCard } from "./shielded/visibility-card";
import { PublicNote } from "./wallet/public-note";

const tx = (n: number, over: Partial<ShieldedTx> = {}): ShieldedTx => ({
  hash: n.toString(16).padStart(64, "0").toUpperCase(),
  height: 100 + n,
  time: "2026-10-08T12:00:00.000Z",
  ok: true,
  bundle: { kind: "shield", actions: 1, amount: "5000000000" },
  signer: { address: addr(1) },
  ...over,
});
const transfer = (n: number, fee = "400000") => tx(n, { bundle: { kind: "transfer", actions: 2, amount: fee }, signer: null });
const unshield = (n: number, amount: string) => tx(n, { bundle: { kind: "unshield", actions: 1, amount } });

describe("summarizeWindow", () => {
  it("TestSummarizeWindow_adds_shielded_unshielded_and_transfer_fees_apart", () => {
    const t = summarizeWindow([tx(1), tx(2, { bundle: { kind: "shield_earnings", actions: 1, amount: "1000000000" } }), unshield(3, "2000000000"), transfer(4), transfer(5, "100000")]);
    expect(t).toEqual({
      accepted: 5,
      refused: 0,
      shieldedAmount: 6_000_000_000n,
      shieldedCount: 2,
      unshieldedAmount: 2_000_000_000n,
      unshieldedCount: 1,
      transferCount: 2,
      transferFees: 500_000n,
    });
  });

  it("TestSummarizeWindow_a_transaction_without_a_bundle_moves_nothing_and_an_empty_list_is_all_zero", () => {
    expect(summarizeWindow([tx(1, { ok: false, bundle: null })])).toMatchObject({ accepted: 0, refused: 1, shieldedAmount: 0n });
    expect(summarizeWindow([])).toMatchObject({ accepted: 0, refused: 0, transferFees: 0n });
  });
});

const pool: ShieldedPool = {
  held: "3500000000",
  noteCommitments: "1200",
  nullifiers: "700",
  params: { nullifierFee: "1000000", maxActionsPerBundle: 16, unshieldFloor: "1000000000", maxFeeTopup: "10000000" },
};

describe("pool figures", () => {
  it("TestPoolStats_shows_what_the_account_holds_and_the_counters_without_calling_them_notes", () => {
    const html = render(<PoolStats pool={pool} />);
    expect(html).toContain("3.5 ORAMA");
    expect(html).toContain("1,200");
    expect(html).toContain("700");
    expect(html).toContain(`href="/explorer/wallet/${SHIELDED_POOL_ADDRESS}"`);
    expect(html).not.toMatch(/notes in the pool/i);
  });

  it("TestRulesCard_states_the_turnstile_as_a_rule_not_a_measurement_and_the_cap_with_the_floor", () => {
    const html = render(<RulesCard pool={pool} />);
    expect(html).toContain("not a figure this page measures");
    expect(html).toContain("1 ORAMA");
    expect(html).toContain("at most 16 actions");
  });

  it("TestVisibilityCard_lists_what_is_and_is_not_on_chain", () => {
    const html = render(<VisibilityCard />);
    expect(html).toContain("On chain");
    expect(html).toContain("Not on chain");
    expect(html).toContain("Who sent a private transfer");
  });
});

const activity = (items: ShieldedTx[], over: { hasMore?: boolean } = {}): KeyPaged<ShieldedTx> => ({
  first: { status: "ready", data: { items, nextKey: over.hasMore ? "AAAAAAAAAAAAA-Dk" : null }, error: null },
  items,
  hasMore: over.hasMore ?? false,
  loadingMore: false,
  moreError: null,
  loadMore: () => {},
  retry: () => {},
});

describe("pool activity", () => {
  it("TestActivityCard_a_private_transfer_row_has_no_signer_and_only_its_fee", () => {
    const html = render(<ActivityCard paged={activity([transfer(1), unshield(2, "2000000000")])} total={42} />);
    expect(html).toContain("Private transfer");
    expect(html).toContain("none: private");
    expect(html).toContain("0.0004 ORAMA");
    expect(html).toContain("fee");
    expect(html).toContain("2 ORAMA");
    expect(html).toContain("out of the pool");
    expect(html).toContain(`href="/explorer/wallet/${addr(1)}"`);
    expect(html).toContain("42 transactions in all");
  });

  it("TestActivityCard_a_failed_row_says_nothing_moved_and_shows_no_amount", () => {
    const html = render(<ActivityCard paged={activity([tx(1, { ok: false, bundle: null })])} total={42} />);
    expect(html).toContain("Failed");
    expect(html).toContain("nothing moved");
  });

  it("TestActivityCard_no_activity_says_so_and_claims_no_total", () => {
    const html = render(<ActivityCard paged={activity([])} total={null} />);
    expect(html).toContain("No transaction has touched the shielded pool in this index.");
    expect(html).not.toContain("transactions in all");
  });

  it("TestPageTotals_say_the_amounts_of_private_transfers_are_not_on_chain_and_stay_quiet_for_none", () => {
    expect(render(<PageTotals activity={activity([tx(1), transfer(2)])} />)).toContain("amounts of the private transfers are not on chain");
    expect(render(<PageTotals activity={activity([])} />)).toBe("");
  });

  it("TestActivityCard_a_full_page_offers_an_older_one", () => {
    const html = render(<ActivityCard paged={activity([tx(1)], { hasMore: true })} total={42} />);
    expect(html).toContain("Show more transactions");
    expect(html).not.toContain("?page=");
    expect(render(<ActivityCard paged={activity([tx(1)])} total={42} />)).not.toContain("Show more transactions");
  });
});

describe("chrome", () => {
  it("TestPublicNote_says_the_wallet_page_covers_public_activity_only", () => {
    const html = render(<PublicNote />);
    expect(html).toContain("This page shows public activity only.");
    expect(html).toContain("a private transfer is not listed here");
    expect(html).toContain('href="/explorer/shielded"');
  });

  it("TestIdPager_offers_older_and_newest_in_the_url_and_hides_when_the_list_fits", () => {
    expect(render(<IdPager param="from" atNewest next={7} />)).toContain("?from=7");
    expect(render(<IdPager param="from" atNewest={false} next={null} />, "/?from=3&x=1")).toContain('rel="first"');
    expect(render(<IdPager param="from" atNewest next={null} />)).toBe("");
  });

  it("TestLookupForm_starts_quiet_and_names_the_box_for_a_screen_reader", () => {
    const html = render(<LookupForm label="Proposal number" placeholder="Proposal number" resolve={() => null} hint="A proposal is numbered from 1." />);
    expect(html).toContain("Proposal number");
    expect(html).not.toContain("is numbered from 1");
    expect(html).toContain("disabled");
  });

  it("TestActivityRows_list_the_transactions_or_say_there_are_none", () => {
    expect(render(<ActivityRows txs={[]} empty="None of the newest transactions touched the market." />)).toContain("None of the newest transactions touched the market.");
    const summary: TxSummary = {
      hash: "AB".repeat(32),
      height: 1,
      time: "2026-10-08T12:00:00.000Z",
      status: { ok: true },
      signer: { address: addr(1) },
      messages: [{ type: "send", from: { address: addr(1) }, to: { address: addr(2) }, amount: "5000000000" }],
      fee: { burned: "0", tip: "0", gasUsed: 0, gasWanted: 0 },
    };
    expect(render(<ActivityRows txs={[summary]} empty="x" />)).toContain(`/explorer/tx/${"AB".repeat(32)}`);
  });
});
