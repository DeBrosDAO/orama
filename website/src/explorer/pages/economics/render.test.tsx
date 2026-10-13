import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter } from "react-router";
import { describe, expect, it } from "vitest";
import type { RecordedEpochRow, SupplyPoint, UnavailableEpochRow } from "../../model/epochs";
import { SeriesChart } from "../../ui/series-chart";
import { CompositionCard, SupplyTiles } from "./supply-cards";
import { BurnTrend, SupplyTrends } from "./trend-cards";

const ORAMA = 1_000_000_000n;
const o = (n: bigint) => (n * ORAMA).toString();

function point(epoch: number, over: Partial<SupplyPoint> = {}): SupplyPoint {
  return {
    epoch,
    height: epoch * 100,
    time: "2026-10-09T00:00:00.000Z",
    totalSupply: o(41_000_000n),
    mintedToDate: o(1_000_000n),
    burnedToDate: "1234500000",
    genesisSupply: o(40_000_000n),
    bonded: o(7_000_000n),
    unbonding: o(1_000n),
    earningsPools: o(2_000n),
    storageEscrow: o(3_000n),
    otherProtocol: o(4_000n),
    circulating: o(41_000_000n - 7_000_000n - 1_000n - 2_000n - 3_000n - 4_000n),
    ...over,
  };
}

function epochRow(epoch: number, burned: string, partial = false): RecordedEpochRow {
  return {
    epoch,
    unavailable: false,
    startHeight: 1,
    endHeight: 2,
    startTime: "2026-10-08T00:00:00.000Z",
    endTime: "2026-10-09T00:00:00.000Z",
    blocks: 2,
    partial,
    maxMintable: o(14_848n),
    mintedValidators: "0",
    mintedService: "0",
    mintedDevelopment: "0",
    mintedFaucet: "0",
    mintedTotal: "0",
    burned,
    feesCollected: "0",
    feesBurned: burned,
    feesDistributed: "0",
    rewardsCredited: "0",
    rewardsForceBonded: "0",
    rewardsRecorded: false,
    rewardsCommission: null,
    rewardsOperatorShare: null,
    rewardsDelegators: null,
  };
}

const html = (node: React.ReactNode) => renderToStaticMarkup(<MemoryRouter>{node}</MemoryRouter>);

describe("SupplyTiles", () => {
  it("TestSupplyTiles_shows_total_circulating_minted_and_burned_in_ORAMA", () => {
    const out = html(<SupplyTiles point={point(12)} />);
    expect(out).toContain("Total supply");
    expect(out).toContain("41,000,000");
    expect(out).toContain("Circulating");
    expect(out).toContain("33,990,000");
    expect(out).toContain("Minted to date");
    expect(out).toContain("1,000,000");
    expect(out).toContain("40,000,000");
    expect(out).toContain("Burned to date");
    expect(out).toContain("1.2345");
  });

  it("TestSupplyTiles_keeps_every_digit_of_a_supply_beyond_a_double", () => {
    const out = html(<SupplyTiles point={point(1, { totalSupply: "123456789012345678901234567890" })} />);
    expect(out).toContain("123,456,789,012,345,678,901.23456789");
    expect(out).toContain("123456789012345678901234567890 norama");
  });
});

describe("CompositionCard", () => {
  it("TestCompositionCard_names_every_holder_with_its_amount_and_share", () => {
    const out = html(<CompositionCard point={point(12)} />);
    for (const name of ["Circulating", "Staked (bonded to validators)", "Unbonding", "Earnings pools", "Storage escrow", "Other protocol accounts"]) {
      expect(out).toContain(name);
    }
    expect(out).toContain("17.0%");
    expect(out).toContain("82.9%");
    expect(out).toContain('role="img"');
  });

  it("TestCompositionCard_zero_slices_stay_in_the_table_but_not_in_the_bar", () => {
    const out = html(<CompositionCard point={point(1, { bonded: "0", circulating: o(41_000_000n - 10_000n) })} />);
    expect(out).toContain("Staked (bonded to validators)");
    expect(out).toContain("0.0%");
  });
});

describe("SupplyTrends", () => {
  it("TestSupplyTrends_draws_four_series_oldest_to_newest_each_with_a_table", () => {
    const points = [point(3), point(2), point(1)];
    const out = html(<SupplyTrends points={points} />);
    for (const title of ["Total supply", "Circulating", "Staked", "Burned to date"]) expect(out).toContain(title);
    expect(out.match(/<svg/g)).toHaveLength(4);
    expect(out.match(/<details/g)).toHaveLength(4);
    expect(out).toContain("from epoch 1 to epoch 3");
  });

  it("TestSupplyTrends_a_single_epoch_has_no_trend_line_only_its_figure", () => {
    const out = html(<SupplyTrends points={[point(1)]} />);
    expect(out).not.toContain("<svg");
    expect(out).toContain("a trend needs two");
    expect(out).toContain("View as a table");
  });

  it("TestSupplyTrends_no_points_draws_nothing", () => {
    expect(html(<SupplyTrends points={[]} />)).not.toContain("<svg");
  });
});

describe("BurnTrend", () => {
  it("TestBurnTrend_one_bar_per_epoch_with_the_partial_epoch_marked_in_the_table", () => {
    const out = html(<BurnTrend rows={[epochRow(3, "3000000000"), epochRow(2, "0"), epochRow(1, "500000000", true)]} />);
    expect(out.match(/<rect/g)).toHaveLength(3);
    expect(out).toContain("<td");
    expect(out).toContain(">yes<");
    expect(out).toContain("A partial");
  });

  it("TestBurnTrend_an_index_with_no_closed_epoch_says_so_instead_of_drawing_an_empty_chart", () => {
    const out = html(<BurnTrend rows={[]} />);
    expect(out).toContain("No epoch has closed in this index yet");
    expect(out).not.toContain("<svg");
  });

  it("TestBurnTrend_an_all_zero_burn_still_draws_bars_of_no_height", () => {
    const out = html(<BurnTrend rows={[epochRow(2, "0"), epochRow(1, "0")]} />);
    expect(out.match(/<rect/g)).toHaveLength(2);
    expect(out).toContain('height="0"');
  });
});

const unavailableRow = (epoch: number): UnavailableEpochRow => ({
  epoch,
  unavailable: true,
  reason: "the node no longer kept the chain state at the closing height",
  startHeight: 1,
  endHeight: 2,
  startTime: "2026-10-08T00:00:00.000Z",
  endTime: "2026-10-09T00:00:00.000Z",
  blocks: 2,
  partial: false,
  rewardsRecorded: false,
  rewardsCommission: null,
  rewardsOperatorShare: null,
  rewardsDelegators: null,
});

describe("BurnTrend with an unavailable epoch", () => {
  it("TestBurnTrend_an_unavailable_epoch_is_a_gap_with_no_bar_and_no_zero_in_the_table", () => {
    const out = html(<BurnTrend rows={[epochRow(3, "3000000000"), unavailableRow(2), epochRow(1, "500000000")]} />);
    expect(out.match(/<rect/g)).toHaveLength(2);
    expect(out).toContain("1 slot has no figure");
    expect(out).toContain("not available");
    expect(out).toContain("never shown as zero");
    expect(out).toContain("2 points");
  });

  it("TestBurnTrend_the_chart_scale_ignores_an_unavailable_epoch", () => {
    const out = html(<BurnTrend rows={[epochRow(2, "3000000000"), unavailableRow(1)]} />);
    expect(out).toContain("highest 3");
    expect(out).toContain("lowest 0");
    expect(out.match(/<rect/g)).toHaveLength(1);
  });

  it("TestBurnTrend_only_unavailable_epochs_draw_no_chart", () => {
    const out = html(<BurnTrend rows={[unavailableRow(2), unavailableRow(1)]} />);
    expect(out).not.toContain("<svg");
    expect(out).toContain("no figure for any of these epochs");
  });
});

describe("SupplyTrends with an epoch the index has no supply point for", () => {
  it("TestSupplyTrends_a_missing_epoch_breaks_the_line_and_is_listed_as_not_available", () => {
    const out = html(<SupplyTrends points={[point(5), point(4), point(1)]} />);
    expect(out.match(/<polyline/g)).toHaveLength(8);
    expect(out).toContain("epochs 2 to 3");
    expect(out).toContain("not available");
    expect(out).toContain("A blank stretch");
    expect(out).toContain("1 slot has no figure");
  });

  it("TestSupplyTrends_contiguous_epochs_have_no_gap_note", () => {
    const out = html(<SupplyTrends points={[point(3), point(2), point(1)]} />);
    expect(out).not.toContain("A blank stretch");
    expect(out.match(/<polyline/g)).toHaveLength(4);
  });
});

describe("SeriesChart", () => {
  it("TestSeriesChart_a_flat_line_is_drawn_not_dropped", () => {
    const out = html(
      <SeriesChart
        kind="line"
        label="Flat"
        values={[5n, 5n]}
        format={(v) => v.toString()}
        from="a"
        to="b"
        table={{ head: ["x"], rows: [["1"], ["2"]] }}
      />,
    );
    expect(out).toContain("<polyline");
    expect(out).toContain("0.0,80.0 640.0,80.0");
  });
});
