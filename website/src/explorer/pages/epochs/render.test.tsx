import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter } from "react-router";
import { describe, expect, it } from "vitest";
import { ChainIndexUnavailable, ChainReadError } from "../../data/chain/client";
import type { QueryState } from "../../data/use-query";
import type { EpochClock, RecordedEpochRow } from "../../model/epochs";
import { IndexQuery } from "../../ui/index-query";
import { SeriesChart } from "../../ui/series-chart";
import { HalvingCard } from "../emission/halving-card";
import { ScheduleCard } from "../emission/schedule-card";
import { SplitCard } from "../emission/split-card";
import { EpochTable } from "./epoch-table";
import { EpochFacts, FeesCard, MintedCard } from "./epoch-summary";
import { Pager } from "./pager";

const ORAMA = 1_000_000_000n;

function row(epoch: number, over: Partial<RecordedEpochRow> = {}): RecordedEpochRow {
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
    mintedService: (1_000n * ORAMA).toString(),
    mintedDevelopment: "0",
    mintedFaucet: "0",
    mintedTotal: (9_908n * ORAMA + 800_000_000n).toString(),
    burned: "1500000000",
    feesCollected: "3000000000",
    feesBurned: "1500000000",
    feesDistributed: "1500000000",
    rewardsCredited: (8_900n * ORAMA).toString(),
    rewardsForceBonded: (8n * ORAMA).toString(),
    rewardsRecorded: false,
    rewardsCommission: null,
    rewardsOperatorShare: null,
    rewardsDelegators: null,
    ...over,
  };
}

const html = (node: React.ReactNode) => renderToStaticMarkup(<MemoryRouter>{node}</MemoryRouter>);

describe("IndexQuery", () => {
  const children = () => <p>ready</p>;

  it("TestIndexQuery_a_node_with_no_indexer_gets_a_plain_notice_not_an_error_box", () => {
    const state: QueryState<string> = { status: "error", data: null, error: new ChainIndexUnavailable() };
    const out = html(<IndexQuery state={state} onRetry={() => undefined}>{children}</IndexQuery>);
    expect(out).toContain("This node runs no chain indexer.");
    expect(out).toContain("Try again");
    expect(out).not.toContain("Could not load this.");
    expect(out).not.toContain("orama global install");
  });

  it("TestIndexQuery_any_other_failure_keeps_the_error_box", () => {
    const state: QueryState<string> = { status: "error", data: null, error: new ChainReadError("The chain could not be read (HTTP 500).") };
    const out = html(<IndexQuery state={state}>{children}</IndexQuery>);
    expect(out).toContain("Could not load this.");
    expect(out).toContain("HTTP 500");
    expect(out).not.toContain("runs no chain indexer");
  });

  it("TestIndexQuery_loading_and_ready_pass_through", () => {
    expect(html(<IndexQuery state={{ status: "loading", data: null, error: null }}>{children}</IndexQuery>)).toContain('aria-label="Loading"');
    expect(html(<IndexQuery state={{ status: "ready", data: "x", error: null }}>{children}</IndexQuery>)).toContain("ready");
  });
});

describe("EpochTable", () => {
  it("TestEpochTable_marks_a_partial_epoch_and_links_every_epoch", () => {
    const out = html(<EpochTable rows={[row(731), row(730, { partial: true })]} />);
    expect(out).toContain('href="/explorer/epoch/731"');
    expect(out).toContain('href="/explorer/epoch/730"');
    expect(out.match(/partial/g)).toHaveLength(1);
    expect(out).toContain("9,908.8");
  });
});

describe("Pager", () => {
  it("TestPager_a_list_that_fits_one_page_has_no_pager", () => {
    expect(html(<Pager param="page" page={1} hasMore={false} />)).toBe("");
  });

  it("TestPager_a_full_first_page_offers_older_and_a_later_page_offers_both_with_the_page_in_the_url", () => {
    const first = html(<Pager param="page" page={1} hasMore />);
    expect(first).toContain('href="/?page=2"');
    expect(first).toContain('aria-disabled="true"');
    const middle = html(<Pager param="page" page={3} hasMore />);
    expect(middle).toContain("page=2");
    expect(middle).toContain("page=4");
  });

  it("TestPager_the_last_page_has_no_older_link", () => {
    const out = html(<Pager param="vpage" page={2} hasMore={false} />);
    expect(out).toContain('rel="prev"');
    expect(out).not.toContain('rel="next"');
  });
});

describe("EpochFacts", () => {
  it("TestEpochFacts_shows_the_time_of_day_because_a_test_networks_epochs_are_minutes_long", () => {
    const out = html(<EpochFacts row={row(5, { startTime: "2026-10-08T12:05:00.000Z", endTime: "2026-10-08T12:10:00.000Z" })} />);
    expect(out).toContain("12:05:00 UTC");
    expect(out).toContain("12:10:00 UTC");
    expect(out).toContain("5 m 0 s");
  });
});

describe("MintedCard", () => {
  it("TestMintedCard_shows_storage_and_relay_as_one_figure_and_says_so", () => {
    const out = html(<MintedCard row={row(5)} />);
    expect(out).toContain("Storage and relay, combined");
    expect(out).not.toMatch(/>Storage</);
    expect(out).not.toMatch(/>Relay</);
    expect(out).toContain("8,908.8");
    expect(out).toContain("5,196.8");
    expect(out).toContain("14,848");
  });

  it("TestMintedCard_a_partial_epoch_says_its_figures_are_partial", () => {
    expect(html(<MintedCard row={row(5, { partial: true })} />)).toContain("This epoch is partial");
    expect(html(<MintedCard row={row(5)} />)).not.toContain("This epoch is partial");
  });

  it("TestMintedCard_the_faucet_row_appears_only_when_it_minted", () => {
    expect(html(<MintedCard row={row(5)} />)).not.toContain("faucet");
    expect(html(<MintedCard row={row(5, { mintedFaucet: "1000000000" })} />)).toContain("Test-network faucet");
  });

  it("TestMintedCard_a_zero_ceiling_row_does_not_divide_by_zero", () => {
    const out = html(<MintedCard row={row(5, { maxMintable: "0", mintedValidators: "0", mintedService: "0", mintedTotal: "0" })} />);
    expect(out).toContain("Total");
  });
});

describe("FeesCard", () => {
  it("TestFeesCard_shows_each_figure_in_ORAMA", () => {
    const out = html(<FeesCard row={row(5)} />);
    expect(out).toContain("3 ORAMA");
    expect(out).toContain("1.5 ORAMA");
  });
});

describe("SeriesChart", () => {
  const props = {
    label: "Supply over epochs",
    values: [10n ** 24n, 10n ** 24n + 5n, 10n ** 24n + 9n],
    format: (v: bigint) => v.toString(),
    from: "epoch 1",
    to: "epoch 3",
    table: { head: ["Epoch", "Supply"], rows: [["1", "a"], ["2", "b"], ["3", "c"]] },
  };

  it("TestSeriesChart_has_an_accessible_name_and_a_table_of_the_same_data", () => {
    const out = html(<SeriesChart kind="line" {...props} />);
    expect(out).toContain('role="img"');
    expect(out).toContain('aria-label="Supply over epochs. 3 points, from epoch 1 to epoch 3;');
    expect(out).toContain("<details");
    expect(out).toContain("View as a table");
    expect(out).toContain("<caption");
    expect(out.match(/<tr/g)).toHaveLength(4);
  });

  it("TestSeriesChart_bars_draw_one_rect_per_value_and_a_dashed_ceiling", () => {
    const out = html(<SeriesChart kind="bars" {...props} reference={[10n ** 24n + 9n, 10n ** 24n + 9n, 10n ** 24n + 9n]} />);
    expect(out.match(/<rect/g)).toHaveLength(3);
    expect(out).toContain("stroke-dasharray");
  });

  it("TestSeriesChart_one_point_is_no_line_only_a_note_and_the_table", () => {
    const out = html(<SeriesChart kind="line" {...props} values={[5n]} table={{ head: ["Epoch"], rows: [["1"]] }} />);
    expect(out).not.toContain("<svg");
    expect(out).toContain("a trend needs two");
    expect(out).toContain("View as a table");
  });

  it("TestSeriesChart_no_values_draws_nothing", () => {
    expect(html(<SeriesChart kind="line" {...props} values={[]} />)).toBe("");
  });
});

describe("ScheduleCard", () => {
  it("TestScheduleCard_lists_the_chains_six_rows_and_marks_the_current_one", () => {
    const out = html(<ScheduleCard currentEpoch={731} />);
    expect(out).toContain("1 to 730");
    expect(out).toContain("731 to 1,460");
    expect(out).toContain("3,651 onward");
    expect(out).toContain("14,848");
    expect(out).toContain("7,424");
    expect(out).toContain("274");
    expect(out).toContain("21,000,640");
    expect(out.match(/aria-current="true"/g)).toHaveLength(1);
    expect(out.indexOf("aria-current")).toBeGreaterThan(out.indexOf("1 to 730"));
    expect(out.indexOf("aria-current")).toBeLessThan(out.indexOf("731 to 1,460"));
  });

  it("TestScheduleCard_marks_nothing_until_the_epoch_is_known", () => {
    expect(html(<ScheduleCard currentEpoch={null} />)).not.toContain("aria-current");
  });
});

describe("SplitCard", () => {
  it("TestSplitCard_splits_the_current_ceiling_and_keeps_storage_and_relay_together", () => {
    const out = html(<SplitCard epoch={731} />);
    expect(out).toContain("epoch 731");
    expect(out).toContain("4,454.4");
    expect(out).toContain("2,598.4");
    expect(out).toContain("371.2");
    expect(out).toContain("25% + 10%");
  });
});

describe("HalvingCard", () => {
  const clock = (epoch: number): EpochClock => ({
    epoch,
    startedAt: "2026-10-08T00:00:00.000Z",
    durationSeconds: 300,
    minBlocks: 10,
    blocksInEpoch: 0,
    readAtHeight: 1,
  });

  it("TestHalvingCard_the_last_epoch_of_a_bracket_is_one_epoch_from_the_halving", () => {
    const out = html(<HalvingCard clock={clock(730)} />);
    expect(out).toContain("1 epoch<");
    expect(out).toContain("until epoch 731");
    expect(out).toContain("halves");
    expect(out).toContain("14,848");
    expect(out).toContain("7,424");
  });

  it("TestHalvingCard_epoch_731_is_a_full_bracket_away_from_the_next", () => {
    const out = html(<HalvingCard clock={clock(731)} />);
    expect(out).toContain("730 epochs");
    expect(out).toContain("until epoch 1,461");
  });

  it("TestHalvingCard_the_step_to_the_tail_is_not_called_a_halving", () => {
    const out = html(<HalvingCard clock={clock(3_650)} />);
    expect(out).toContain("Final step down");
    expect(out).toContain("drops");
    expect(out).not.toContain("halves");
  });

  it("TestHalvingCard_in_the_tail_there_is_nothing_left_to_count_down", () => {
    const out = html(<HalvingCard clock={clock(3_651)} />);
    expect(out).toContain("No more halvings");
    expect(out).toContain("274 ORAMA");
  });
});
