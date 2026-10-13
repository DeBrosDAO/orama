import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter } from "react-router";
import { describe, expect, it } from "vitest";
import { ChainIndexUnavailable, ChainReadError } from "../../data/chain/client";
import type { QueryState } from "../../data/use-query";
import type { EpochRow, IndexPage, RecordedEpochRow } from "../../model/epochs";
import type { AccountReward, AccountRewards, Earnings, EpochRewards, ValidatorReward, ValidatorRewardHistory } from "../../model/rewards";
import type { KeyPaged } from "../../ui/use-key-paged";
import { AccountRewardsView, EarningsView, NO_REWARDS } from "./account-rewards";
import { EpochRewardsView, NOT_RECORDED_EPOCH, NOTHING_PAID } from "./epoch-rewards";
import { RewardList } from "../rewards";
import { NOT_RECORDED, RewardsTable } from "./rewards-table";
import { NOT_SEEN_REWARDS, ValidatorRewardsView } from "./validator-rewards";

const ORAMA = 1_000_000_000n;
const html = (node: React.ReactNode) => renderToStaticMarkup(<MemoryRouter>{node}</MemoryRouter>);
const ready = <T,>(data: T): QueryState<T> => ({ status: "ready", data, error: null });
const failed = <T,>(error: Error): QueryState<T> => ({ status: "error", data: null, error });
const loading = <T,>(): QueryState<T> => ({ status: "loading", data: null, error: null });
const noop = () => undefined;

function epoch(n: number, over: Partial<RecordedEpochRow> = {}): RecordedEpochRow {
  return {
    epoch: n, unavailable: false, startHeight: 1, endHeight: 2, startTime: "2026-10-08T00:00:00.000Z", endTime: "2026-10-09T00:00:00.000Z", blocks: 2,
    partial: false, maxMintable: "0", mintedValidators: "0", mintedService: "0", mintedDevelopment: "0", mintedFaucet: "0", mintedTotal: "0",
    burned: "0", feesCollected: "0", feesBurned: "0", feesDistributed: "0", rewardsCredited: "0", rewardsForceBonded: (5n * ORAMA).toString(),
    rewardsRecorded: true, rewardsCommission: (10n * ORAMA).toString(), rewardsOperatorShare: (70n * ORAMA).toString(), rewardsDelegators: (30n * ORAMA).toString(),
    ...over,
  };
}
const unrecorded = { rewardsRecorded: false, rewardsCommission: null, rewardsOperatorShare: null, rewardsDelegators: null };

const alpha = { moniker: "Alpha", operator: "orama1alpha" };
const reward = (over: Partial<ValidatorReward> = {}): ValidatorReward => ({
  epoch: 9, ref: alpha, amount: (100n * ORAMA).toString(), commission: (10n * ORAMA).toString(), operatorShare: (70n * ORAMA).toString(),
  delegators: (30n * ORAMA).toString(), forceBonded: "0", ...over,
});

describe("RewardsTable", () => {
  it("TestRewardsTable_splits_a_recorded_epoch_and_links_it", () => {
    const out = html(<RewardsTable rows={[epoch(9)]} />);
    expect(out).toContain('href="/explorer/epoch/9"');
    expect(out).toContain("100");
    expect(out).toContain("10.0%");
    expect(out).toContain("60.0%");
    expect(out).toContain("30.0%");
    expect(out).not.toContain(NOT_RECORDED);
  });

  it("TestRewardsTable_an_epoch_before_the_chain_reported_them_says_not_recorded_in_every_figure_column", () => {
    const out = html(<RewardsTable rows={[epoch(2, unrecorded)]} />);
    expect(out.split(NOT_RECORDED)).toHaveLength(6);
    expect(out).not.toContain("0.0%");
  });

  it("TestRewardsTable_an_epoch_that_paid_nothing_shows_zeros_not_not_recorded", () => {
    const out = html(<RewardsTable rows={[epoch(3, { rewardsCommission: "0", rewardsOperatorShare: "0", rewardsDelegators: "0", rewardsForceBonded: "0" })]} />);
    expect(out).not.toContain(NOT_RECORDED);
    expect(out).toContain(">0<");
  });

  it("TestRewardsTable_no_epochs_is_an_empty_table_body", () => {
    expect(html(<RewardsTable rows={[]} />)).not.toContain("<tr class");
  });
});

describe("EpochRewardsView", () => {
  const data = (over: Partial<EpochRewards> = {}): EpochRewards => ({ epoch: 9, recorded: true, items: [reward()], page: 1, limit: 25, hasMore: false, ...over });

  it("TestEpochRewardsView_names_each_validator_and_splits_its_reward", () => {
    const out = html(<EpochRewardsView state={ready(data())} page={1} onRetry={noop} />);
    expect(out).toContain("Alpha");
    expect(out).toContain('href="/explorer/validator/orama1alpha"');
    expect(out).toContain("100");
    expect(out).toContain("60");
  });

  it("TestEpochRewardsView_an_epoch_not_recorded_says_so_and_shows_no_table", () => {
    const out = html(<EpochRewardsView state={ready(data({ recorded: false, items: [] }))} page={1} onRetry={noop} />);
    expect(out).toContain(NOT_RECORDED_EPOCH.slice(0, 40));
    expect(out).not.toContain("<table");
  });

  it("TestEpochRewardsView_a_recorded_epoch_that_paid_nobody_says_so_not_that_it_is_not_recorded", () => {
    const out = html(<EpochRewardsView state={ready(data({ items: [] }))} page={1} onRetry={noop} />);
    expect(out).toContain(NOTHING_PAID);
    expect(out).not.toContain("did not report");
  });

  it("TestEpochRewardsView_a_page_past_the_end_says_there_are_no_more", () => {
    expect(html(<EpochRewardsView state={ready(data({ items: [], page: 4 }))} page={4} onRetry={noop} />)).toContain("no more validators");
  });

  it("TestEpochRewardsView_a_full_page_offers_older_with_the_page_in_the_url", () => {
    expect(html(<EpochRewardsView state={ready(data({ hasMore: true }))} page={1} onRetry={noop} />)).toContain("rpage=2");
  });

  it("TestEpochRewardsView_an_epoch_the_index_does_not_hold_says_so", () => {
    expect(html(<EpochRewardsView state={ready(null)} page={1} onRetry={noop} />)).toContain("does not hold this epoch");
  });

  it("TestEpochRewardsView_loading_error_and_no_indexer", () => {
    expect(html(<EpochRewardsView state={loading()} page={1} onRetry={noop} />)).toContain('aria-label="Loading"');
    const err = html(<EpochRewardsView state={failed(new ChainReadError("The chain could not be read (HTTP 500)."))} page={1} onRetry={noop} />);
    expect(err).toContain("HTTP 500");
    expect(err).toContain("Try again");
    expect(html(<EpochRewardsView state={failed(new ChainIndexUnavailable())} page={1} onRetry={noop} />)).toContain("runs no chain indexer");
  });
});

describe("ValidatorRewardsView", () => {
  const history = (items: ValidatorRewardHistory["items"]): ValidatorRewardHistory => ({ items, page: 1, limit: 20, hasMore: false });

  it("TestValidatorRewardsView_shows_each_epoch_as_paid_nothing_paid_or_not_recorded", () => {
    const out = html(
      <ValidatorRewardsView
        onRetry={noop}
        state={ready(history([
          { epoch: 9, recorded: true, reward: reward() },
          { epoch: 8, recorded: true, reward: null },
          { epoch: 7, recorded: false, reward: null },
        ]))}
      />,
    );
    expect(out).toContain('href="/explorer/epoch/9"');
    expect(out).toContain("nothing paid");
    expect(out).toContain("not recorded");
    expect(out).toContain("Paid, 2 recorded epochs");
    expect(out).toContain("it adds nothing here, and is not a zero");
  });

  it("TestValidatorRewardsView_only_epochs_not_recorded_has_no_summary", () => {
    const out = html(<ValidatorRewardsView onRetry={noop} state={ready(history([{ epoch: 3, recorded: false, reward: null }]))} />);
    expect(out).not.toContain("Paid,");
    expect(out).toContain("not recorded");
  });

  it("TestValidatorRewardsView_a_validator_the_index_has_not_seen_and_one_with_no_epochs", () => {
    expect(html(<ValidatorRewardsView onRetry={noop} state={ready(null)} />)).toContain(NOT_SEEN_REWARDS);
    expect(html(<ValidatorRewardsView onRetry={noop} state={ready(history([]))} />)).toContain("No closed epoch yet.");
  });

  it("TestValidatorRewardsView_loading_and_error", () => {
    expect(html(<ValidatorRewardsView onRetry={noop} state={loading()} />)).toContain('aria-label="Loading"');
    expect(html(<ValidatorRewardsView onRetry={noop} state={failed(new ChainReadError("boom"))} />)).toContain("boom");
  });
});

describe("AccountRewardsView", () => {
  const data = (over: Partial<AccountRewards> = {}): AccountRewards => ({ address: "orama1x", recordedFromEpoch: 5, items: [], nextKey: null, ...over });
  const paged = (first: QueryState<AccountRewards>, over: Partial<KeyPaged<AccountReward, AccountRewards>> = {}): KeyPaged<AccountReward, AccountRewards> => ({
    first,
    items: first.status === "ready" ? first.data.items : [],
    hasMore: first.status === "ready" && first.data.nextKey !== null,
    loadingMore: false,
    moreError: null,
    loadMore: noop,
    retry: noop,
    ...over,
  });

  it("TestAccountRewardsView_lists_credits_by_epoch_and_validator_and_marks_an_operator", () => {
    const out = html(
      <AccountRewardsView
        paged={paged(ready(data({
          items: [
            { epoch: 9, validator: alpha, role: "delegator", amount: (3n * ORAMA).toString(), forceBonded: "0" },
            { epoch: 8, validator: alpha, role: "operator", amount: (7n * ORAMA).toString(), forceBonded: (2n * ORAMA).toString() },
          ],
        })))}
      />,
    );
    expect(out).toContain("Alpha");
    expect(out).toContain("its operator");
    expect(out).toContain("(2");
    expect(out).toContain("Payouts are recorded from epoch 5");
    expect(out).not.toContain("Show more rewards");
  });

  it("TestAccountRewardsView_a_full_page_offers_more_by_the_indexer_cursor_and_loaded_pages_stay_on_screen", () => {
    const credit = (epoch: number): AccountReward => ({ epoch, validator: alpha, role: "delegator", amount: ORAMA.toString(), forceBonded: "0" });
    const first = data({ items: [credit(9)], nextKey: "AAAAAAAAAAdvcmFtYXZhbG9wZXIxcXFxcXFxcXFxcXFxcXFxcXFxcXFxcXFxcXFxcXFxcXFxcXFx" });
    const out = html(<AccountRewardsView paged={paged(ready(first), { items: [credit(9), credit(8)] })} />);
    expect(out).toContain("Show more rewards");
    expect(out).not.toContain("?page=");
    expect(out).toContain("/epoch/8");
    expect(out).toContain("/epoch/9");
  });

  it("TestAccountRewardsView_an_account_with_none_says_so_and_from_when_that_is_known", () => {
    const out = html(<AccountRewardsView paged={paged(ready(data()))} />);
    expect(out).toContain(NO_REWARDS);
    expect(out).toContain("from epoch 5");
  });

  it("TestAccountRewardsView_before_any_epoch_is_recorded_it_says_none_can_be_shown", () => {
    expect(html(<AccountRewardsView paged={paged(ready(data({ recordedFromEpoch: 0 })))} />)).toContain("No epoch&#x27;s payout has been recorded yet");
  });

  it("TestAccountRewardsView_error_and_loading", () => {
    expect(html(<AccountRewardsView paged={paged(failed(new ChainReadError("nope")))} />)).toContain("nope");
    expect(html(<AccountRewardsView paged={paged(loading())} />)).toContain('aria-label="Loading"');
  });
});

describe("EarningsView", () => {
  const earnings = (balance: string): Earnings => ({ address: "orama1x", balance });

  it("TestEarningsView_shows_the_balance_in_ORAMA_and_says_it_is_not_in_the_public_balance", () => {
    const out = html(<EarningsView state={ready(earnings((12n * ORAMA + 340_000_000n).toString()))} onRetry={noop} />);
    expect(out).toContain("12.34");
    expect(out).toContain("waiting to be withdrawn");
    expect(out).toContain("Earnings");
  });

  it("TestEarningsView_an_account_with_none_shows_zero", () => {
    expect(html(<EarningsView state={ready(earnings("0"))} onRetry={noop} />)).toContain(">0<");
  });

  it("TestEarningsView_loading_and_error", () => {
    expect(html(<EarningsView state={loading()} onRetry={noop} />)).toContain('aria-label="Loading"');
    expect(html(<EarningsView state={failed(new ChainReadError("down"))} onRetry={noop} />)).toContain("down");
  });
});

describe("RewardList", () => {
  const page = (items: EpochRow[], over: Partial<IndexPage<EpochRow>> = {}): IndexPage<EpochRow> => ({ items, page: 1, limit: 20, hasMore: false, ...over });

  it("TestRewardList_an_empty_first_page_says_no_epoch_has_closed", () => {
    expect(html(<RewardList data={page([])} page={1} />)).toContain("No epoch has closed in this index yet.");
  });

  it("TestRewardList_an_empty_later_page_says_there_are_no_older_epochs", () => {
    expect(html(<RewardList data={page([], { page: 3 })} page={3} />)).toContain("There are no older epochs in this index.");
  });

  it("TestRewardList_a_page_with_an_unrecorded_epoch_explains_the_label", () => {
    const out = html(<RewardList data={page([epoch(9), epoch(2, unrecorded)])} page={1} />);
    expect(out).toContain("An epoch marked not recorded closed before the chain reported who was paid");
  });

  it("TestRewardList_a_page_of_recorded_epochs_has_no_such_note_and_a_full_page_has_a_pager", () => {
    const out = html(<RewardList data={page([epoch(9)], { hasMore: true })} page={1} />);
    expect(out).not.toContain("marked not recorded");
    expect(out).toContain("page=2");
  });
});
