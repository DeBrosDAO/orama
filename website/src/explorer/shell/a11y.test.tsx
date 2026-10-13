import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter, Route, Routes } from "react-router";
import { describe, expect, it } from "vitest";
import { ChainIndexUnavailable, ChainReadError } from "../data/chain/client";
import { ExplorerApp } from "../explorer-app";
import { buildRegistry } from "../network/registry";
import { ErrorBox, Query } from "../ui/query-states";
import { NoIndexerBox } from "../ui/index-query";
import { IndexLagLine } from "../ui/index-lag";
import { Help } from "../ui/help";
import { CopyChip } from "../ui/copy";
import { LAG_ANNOUNCE_BLOCKS, lagAnnouncements } from "../model/index-lag";
import type { IndexStatus } from "../model/types";
import { navItems } from "./header";
import { explorerPathsFor } from "../model/routes";

const SHA = "84ee8d46628343b85c464185e1009974beb34f91c99477a75a5dfb66a5918b67";
const REGISTRY = buildRegistry({
  "../../../../networks/stagenet/manifest.json": {
    name: "stagenet",
    chain_id: "orama-stagenet-7",
    genesis_sha256: SHA,
    seeds: ["ns1.stagenet.orama.network", "ns2.stagenet.orama.network"],
  },
});
const ADDRESS = "orama1q8w9k3v5r2m4x7h6d0n8c1p3t5y9j2u4l6e7x2";

const app = (url: string) =>
  renderToStaticMarkup(
    <MemoryRouter initialEntries={[url]}>
      <Routes>
        <Route path="explorer/*" element={<ExplorerApp registry={REGISTRY} />} />
      </Routes>
    </MemoryRouter>,
  );

describe("the explorer shell's landmarks", () => {
  const html = app("/explorer/validators");

  it("TestShell_the_first_stop_is_a_skip_link_to_the_main_section", () => {
    const skip = html.indexOf('href="#explorer-main"');
    expect(skip).toBeGreaterThan(-1);
    expect(skip).toBeLessThan(html.indexOf("<header"));
    expect(html).toContain("Skip to the page");
    expect(html).toMatch(/<main id="explorer-main" tabindex="-1"/);
  });

  it("TestShell_the_sections_are_a_labelled_navigation_landmark", () => {
    expect(html).toContain('<nav aria-label="Explorer sections"');
  });

  it("TestShell_the_current_section_says_so", () => {
    expect(html).toMatch(/aria-current="page"[^>]*href="\/explorer\/validators"/);
  });

  it("TestShell_a_page_inside_a_section_keeps_the_section_current", () => {
    const inside = app(`/explorer/validator/${ADDRESS}`);
    expect(inside).toMatch(/aria-current="true"[^>]*href="\/explorer\/validators"/);
  });

  it("TestShell_the_network_strip_is_a_labelled_group_and_search_names_its_shortcut", () => {
    expect(html).toContain('role="group" aria-label="Network and chain"');
    expect(html).toContain('aria-keyshortcuts="Control+K Meta+K /"');
  });

  it("TestShell_the_gateway_prompt_page_has_the_skip_link_and_its_main_too", () => {
    const gate = app("/explorer/custom?gateway=https%3A%2F%2Fgw.example.org");
    expect(gate).toContain('href="#explorer-main"');
    expect(gate).toContain('<main id="explorer-main"');
    expect(gate).toContain("<h1");
  });
});

describe("navItems", () => {
  it("TestNavItems_a_validator_or_node_page_is_inside_its_section_and_nothing_else_is", () => {
    const paths = explorerPathsFor(null);
    expect(navItems(paths, "validator").filter((i) => i.inside).map((i) => i.label)).toEqual(["Validators"]);
    expect(navItems(paths, "node").filter((i) => i.inside).map((i) => i.label)).toEqual(["Nodes"]);
    expect(navItems(paths, "home").filter((i) => i.inside)).toEqual([]);
  });
});

describe("a page that is a query, in its loading and error states", () => {
  const loading = { status: "loading", data: null, error: null } as const;
  const failed = (error: Error) => ({ status: "error", data: null, error }) as const;

  it("TestQuery_loading_a_whole_page_has_an_h1_that_says_what_is_loading", () => {
    const out = renderToStaticMarkup(
      <Query state={loading} subject="block 100">
        {() => null}
      </Query>,
    );
    expect(out).toContain('<h1 class="sr-only">Loading block 100</h1>');
    expect(out).toContain('role="status" aria-label="Loading"');
  });

  it("TestQuery_failing_a_whole_page_makes_the_error_the_h1_and_names_what_could_not_load", () => {
    const out = renderToStaticMarkup(
      <Query state={failed(new ChainReadError("The chain could not be reached."))} subject="this wallet">
        {() => null}
      </Query>,
    );
    expect(out).toMatch(/<h1 class="[^"]*text-loss[^"]*">Could not load this wallet\.<\/h1>/);
    expect(out).toContain("The chain could not be reached.");
  });

  it("TestQuery_a_part_of_a_page_adds_no_heading", () => {
    const loadingPart = renderToStaticMarkup(
      <Query state={loading}>{() => null}</Query>,
    );
    const failedPart = renderToStaticMarkup(
      <Query state={failed(new Error("boom"))}>{() => null}</Query>,
    );
    expect(loadingPart).not.toContain("<h1");
    expect(failedPart).not.toContain("<h1");
    expect(failedPart).toContain("Could not load this.");
  });

  it("TestErrorBox_offers_a_retry_when_it_can_retry", () => {
    expect(renderToStaticMarkup(<ErrorBox message="x" onRetry={() => undefined} />)).toContain("Try again");
    expect(renderToStaticMarkup(<ErrorBox message="x" />)).not.toContain("Try again");
  });

  it("TestNoIndexerBox_is_the_page_h1_only_when_it_is_the_page", () => {
    expect(renderToStaticMarkup(<NoIndexerBox asPageTitle />)).toContain("<h1");
    expect(renderToStaticMarkup(<NoIndexerBox />)).not.toContain("<h1");
    expect(new ChainIndexUnavailable().message).toContain("no chain indexer");
  });
});


describe("the indexer's lag line is not a chatty live region", () => {
  const status = (over: Partial<IndexStatus> = {}): IndexStatus => ({ startHeight: 1, cursor: 1000, tip: 1000, lag: 0, aggregatesFrom: 1, stuck: null, ...over });
  const behind = (lag: number) => status({ cursor: 1000 - lag, tip: 1000, lag });
  const silent = { status: "", alert: "" };

  it("TestIndexLagLine_the_visible_line_counting_blocks_is_not_live", () => {
    const out = renderToStaticMarkup(<IndexLagLine index={status()} />);
    expect(out).toMatch(/<p data-index-lag="current"[^>]*>History is up to date at block 1,000\.<\/p>/);
  });

  it("TestLagAnnouncements_a_healthy_index_moving_between_0_1_and_2_blocks_behind_says_nothing", () => {
    for (const lag of [0, 1, 2, 1, 0, 2, 1]) expect(lagAnnouncements(behind(lag))).toEqual(silent);
    expect(LAG_ANNOUNCE_BLOCKS).toBeGreaterThan(2);
  });

  it("TestLagAnnouncements_an_index_that_falls_behind_speaks_once_and_stays_the_same_as_it_grows", () => {
    expect(lagAnnouncements(behind(LAG_ANNOUNCE_BLOCKS - 1))).toEqual(silent);
    const first = lagAnnouncements(behind(LAG_ANNOUNCE_BLOCKS));
    expect(first).toEqual({ status: "The indexed history is behind the chain.", alert: "" });
    expect(lagAnnouncements(behind(LAG_ANNOUNCE_BLOCKS + 25))).toEqual(first);
  });

  it("TestLagAnnouncements_a_stuck_index_is_an_alert_with_its_fixed_position_and_an_empty_index_a_status", () => {
    const stuck = { height: 1001, reason: "boom", statePruned: false, failures: 2 };
    const a = lagAnnouncements(status({ lag: 5, stuck }));
    expect(a.status).toBe("");
    expect(a.alert).toContain("stuck at block 1,001");
    expect(lagAnnouncements(status({ lag: 6, stuck }))).toEqual(a);
    expect(lagAnnouncements(status({ cursor: 0, tip: 50, lag: 50 }))).toEqual({ status: "The indexer has not indexed a block yet.", alert: "" });
  });

  it("TestIndexLagLine_has_two_regions_with_one_role_each_whatever_the_state", () => {
    const stuck = { height: 1001, reason: "boom", statePruned: false, failures: 2 };
    for (const index of [status(), behind(1), behind(40), status({ lag: 5, stuck })]) {
      const out = renderToStaticMarkup(<IndexLagLine index={index} />);
      expect(out.match(/role="status"/g)).toHaveLength(1);
      expect(out.match(/role="alert"/g)).toHaveLength(1);
    }
    // The alert region holds the stuck sentence only when stuck, the status region never then.
    const out = renderToStaticMarkup(<IndexLagLine index={status({ lag: 5, stuck })} />);
    expect(out).toContain('<span role="status" class="sr-only"></span>');
    expect(out).toMatch(/<span role="alert" class="sr-only">The indexer is stuck/);
  });
});

describe("small controls", () => {
  it("TestHelp_the_button_is_named_and_described_and_its_touch_area_reaches_24_pixels", () => {
    const out = renderToStaticMarkup(<Help tip="A block is a batch." />);
    expect(out).toContain('aria-label="What does this mean?"');
    expect(out).toMatch(/aria-describedby="[^"]+"/);
    expect(out).toContain('role="tooltip"');
    expect(out).toContain("before:-inset-1");
  });

  it("TestCopyChip_is_at_least_24_pixels_tall_and_named_for_what_it_copies", () => {
    const out = renderToStaticMarkup(<CopyChip value="abc" label="Copy chain id abc" />);
    expect(out).toContain("min-h-6");
    expect(out).toContain('aria-label="Copy chain id abc"');
    expect(out).toContain('aria-live="polite"');
  });
});
