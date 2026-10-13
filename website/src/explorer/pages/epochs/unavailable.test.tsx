import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter } from "react-router";
import { describe, expect, it } from "vitest";
import { FakeGateway } from "../../data/chain/fake-gateway";
import { createChainSource } from "../../data/chain/source";
import { ExplorerProvider } from "../../data/provider";
import type { IndexPage, RecordedEpochRow, UnavailableEpochRow } from "../../model/epochs";
import { EpochBody } from "../epoch";
import { WindowBody } from "../emission/split-card";
import { RewardList } from "../rewards";
import { RewardsTable } from "../rewards/rewards-table";
import { EpochTable } from "./epoch-table";
import { ceilingWindow } from "./logic";
import { NOT_AVAILABLE, UnavailableNote } from "./unavailable";

const ORAMA = 1_000_000_000n;
const REASON = "the node no longer kept the chain state at the closing height (height 700)";
const html = (node: React.ReactNode) => renderToStaticMarkup(<MemoryRouter>{node}</MemoryRouter>);

function recorded(epoch: number, over: Partial<RecordedEpochRow> = {}): RecordedEpochRow {
  return {
    epoch,
    unavailable: false,
    startHeight: 1,
    endHeight: 14_400,
    startTime: "2026-10-08T00:00:00.000Z",
    endTime: "2026-10-09T00:00:00.000Z",
    blocks: 14_400,
    partial: false,
    maxMintable: (14_848n * ORAMA).toString(),
    mintedValidators: (8_908n * ORAMA + 800_000_000n).toString(),
    mintedService: "0",
    mintedDevelopment: "0",
    mintedFaucet: "0",
    mintedTotal: (9_908n * ORAMA + 800_000_000n).toString(),
    burned: "1500000000",
    feesCollected: "3000000000",
    feesBurned: "1500000000",
    feesDistributed: "1500000000",
    rewardsCredited: (8_900n * ORAMA).toString(),
    rewardsForceBonded: (8n * ORAMA).toString(),
    rewardsRecorded: true,
    rewardsCommission: (10n * ORAMA).toString(),
    rewardsOperatorShare: (70n * ORAMA).toString(),
    rewardsDelegators: (30n * ORAMA).toString(),
    ...over,
  };
}

function unavailable(epoch: number, over: Partial<UnavailableEpochRow> = {}): UnavailableEpochRow {
  return {
    epoch,
    unavailable: true,
    reason: REASON,
    startHeight: 1,
    endHeight: 14_400,
    startTime: "2026-10-08T00:00:00.000Z",
    endTime: "2026-10-09T00:00:00.000Z",
    blocks: 14_400,
    partial: false,
    rewardsRecorded: false,
    rewardsCommission: null,
    rewardsOperatorShare: null,
    rewardsDelegators: null,
    ...over,
  };
}

const page = (items: (RecordedEpochRow | UnavailableEpochRow)[]): IndexPage<RecordedEpochRow | UnavailableEpochRow> => ({
  items,
  page: 1,
  limit: 20,
  hasMore: false,
});

describe("EpochTable with an unavailable epoch", () => {
  it("TestEpochTable_an_unavailable_epoch_says_so_and_the_others_still_show", () => {
    const out = html(<EpochTable rows={[recorded(731), unavailable(730), recorded(729, { burned: "7000000000" })]} />);
    expect(out).toContain('href="/explorer/epoch/730"');
    expect(out).toContain(`>${NOT_AVAILABLE}<`);
    expect(out).toContain("9,908.8");
    expect(out).toContain(">7<");
    expect(out.match(new RegExp(`>${NOT_AVAILABLE}<`, "g"))).toHaveLength(1);
  });

  it("TestEpochTable_an_unavailable_epoch_gives_the_indexers_reason_and_no_zero", () => {
    const out = html(<EpochTable rows={[unavailable(730)]} />);
    expect(out).toContain(`Not available: ${REASON}.`);
    expect(out).not.toContain(">0<");
  });

  it("TestEpochTable_only_unavailable_epochs_render_a_table_of_markers", () => {
    const out = html(<EpochTable rows={[unavailable(3), unavailable(2)]} />);
    expect(out.match(new RegExp(`>${NOT_AVAILABLE}<`, "g"))).toHaveLength(2);
  });
});

describe("UnavailableNote", () => {
  it("TestUnavailableNote_says_it_is_never_zero", () => {
    expect(html(<UnavailableNote />)).toContain("never shown as zero");
  });
});

describe("RewardsTable and RewardList with an unavailable epoch", () => {
  it("TestRewardsTable_an_unavailable_epoch_is_marked_and_keeps_the_payout_split_it_has", () => {
    const out = html(
      <RewardsTable
        rows={[recorded(9), unavailable(8, { rewardsRecorded: true, rewardsCommission: (10n * ORAMA).toString(), rewardsOperatorShare: (70n * ORAMA).toString(), rewardsDelegators: (30n * ORAMA).toString() })]}
      />,
    );
    expect(out.match(new RegExp(`>${NOT_AVAILABLE}<`, "g"))).toHaveLength(2);
    expect(out).toContain('href="/explorer/epoch/8"');
    expect(out).toContain("100");
    expect(out).toContain("60.0%");
  });

  it("TestRewardsTable_an_unavailable_epoch_whose_payout_was_not_recorded_is_not_a_zero_split", () => {
    const out = html(<RewardsTable rows={[unavailable(8)]} />);
    expect(out).toContain("not recorded");
    expect(out).not.toContain("0.0%");
    expect(out).not.toContain(">0<");
  });

  it("TestRewardList_a_list_with_an_unavailable_epoch_renders_every_row_and_explains_it", () => {
    const out = html(<RewardList data={page([recorded(9), unavailable(8), recorded(7)])} page={1} />);
    expect(out).toContain('href="/explorer/epoch/9"');
    expect(out).toContain('href="/explorer/epoch/8"');
    expect(out).toContain('href="/explorer/epoch/7"');
    expect(out).toContain("never shown as zero");
  });

  it("TestRewardList_a_list_without_one_has_no_such_note", () => {
    expect(html(<RewardList data={page([recorded(9)])} page={1} />)).not.toContain("never shown as zero");
  });
});

describe("EpochBody", () => {
  const source = createChainSource({ fetcher: new FakeGateway().fetch });
  const body = (row: RecordedEpochRow | UnavailableEpochRow) =>
    html(
      <ExplorerProvider source={source}>
        <EpochBody row={row} />
      </ExplorerProvider>,
    );

  it("TestEpochBody_an_unavailable_epoch_shows_when_it_ran_why_it_has_no_figures_and_no_figure_cards", () => {
    const out = body(unavailable(8));
    expect(out).toContain("Figures not available");
    expect(out).toContain(`Not available: ${REASON}.`);
    expect(out).toContain("Started");
    expect(out).toContain("Validators in this epoch");
    expect(out).not.toContain("Minted in this epoch");
    expect(out).not.toContain("Fees and burn");
    expect(out).not.toContain("Credited to earnings accounts");
  });

  it("TestEpochBody_an_unavailable_epoch_shows_the_payout_split_it_has", () => {
    const out = body(unavailable(8, { rewardsRecorded: true, rewardsCommission: (10n * ORAMA).toString(), rewardsOperatorShare: (70n * ORAMA).toString(), rewardsDelegators: (30n * ORAMA).toString() }));
    expect(out).toContain("Who was paid");
    expect(out).toContain("Delegators");
    expect(out).toContain("30.0%");
  });

  it("TestEpochBody_a_recorded_epoch_still_shows_every_figure_card", () => {
    const out = body(recorded(8));
    expect(out).toContain("Minted in this epoch");
    expect(out).toContain("Fees and burn");
    expect(out).not.toContain("Figures not available");
  });
});

describe("ceilingWindow and SplitCard with an unavailable epoch", () => {
  it("TestCeilingWindow_an_unavailable_epoch_is_counted_apart_and_adds_nothing", () => {
    const win = ceilingWindow([recorded(3), unavailable(2), recorded(1)]);
    expect(win.epochs).toBe(2);
    expect(win.unavailableSkipped).toBe(1);
    expect(win.total.ceiling).toBe(2n * 14_848n * ORAMA);
    expect(win.total.minted).toBe(2n * (9_908n * ORAMA + 800_000_000n));
  });

  it("TestCeilingWindow_only_unavailable_epochs_is_all_zeros_with_the_count", () => {
    const win = ceilingWindow([unavailable(2), unavailable(1)]);
    expect(win.epochs).toBe(0);
    expect(win.unavailableSkipped).toBe(2);
    expect(win.total).toEqual({ ceiling: 0n, minted: 0n });
  });

  it("TestWindowBody_a_window_with_an_unavailable_epoch_sums_the_others_says_so_and_charts_a_gap", () => {
    const out = html(<WindowBody rows={[recorded(3), unavailable(2), recorded(1)]} latest={undefined} />);
    expect(out).toContain("leaving out 1 that is not available");
    expect(out).toContain("2 points");
    expect(out).toContain("1 slot has no figure");
    expect(out.match(/<rect/g)).toHaveLength(2);
    expect(out).toContain(`>${NOT_AVAILABLE}<`);
  });

  it("TestWindowBody_a_window_of_only_unavailable_epochs_has_no_sum_no_bars_and_a_note", () => {
    const out = html(<WindowBody rows={[unavailable(2), unavailable(1)]} latest={undefined} />);
    expect(out).toContain("leaving out 2 that are not available");
    expect(out).not.toContain("<rect");
    expect(out).toContain("no figure for any of these epochs");
  });
});
