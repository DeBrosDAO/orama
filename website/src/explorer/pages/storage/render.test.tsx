import { renderToStaticMarkup } from "react-dom/server";
import type { ReactNode } from "react";
import { MemoryRouter } from "react-router";
import { describe, expect, it } from "vitest";
import { ExplorerProvider } from "../../data/provider";
import type { QueryState } from "../../data/use-query";
import { node, nodeSet } from "../../model/node-fixtures";
import type { ArchiveHealth, DealList, SettlementQueue, StorageDeal } from "../../model/storage";
import { ArchiveCard } from "./archive-card";
import { DealsCard } from "./deal-table";
import { dealTotals, filterDeals, repairDelegates } from "./logic";
import { ProvidersCard, providersOf } from "./providers-card";
import { RepairCard } from "./repair-card";
import { StoragePage } from "../storage";
import { createChainSource } from "../../data/chain/source";
import { FakeGateway } from "../../data/chain/fake-gateway";

const html = (ui: ReactNode) => renderToStaticMarkup(<MemoryRouter>{ui}</MemoryRouter>);
const ready = <T,>(data: T): QueryState<T> => ({ status: "ready", data, error: null });
const failed = <T,>(message: string): QueryState<T> => ({ status: "error", data: null, error: new Error(message) });
const loading = { status: "loading", data: null, error: null } as const;
const noop = () => undefined;
const GIB = 1024n ** 3n;

const deal = (id: number, over: Partial<StorageDeal> = {}): StorageDeal => ({
  id,
  class: "public_pin",
  protocol: false,
  client: "orama1sx0x6xpv8wtgshrajsqgednruq26aes0h2jh07",
  repairDelegate: null,
  replicas: 3,
  pricePerEpoch: "2000000000",
  durationEpochs: 30,
  startEpoch: 10,
  endEpoch: 40,
  escrow: "60000000000",
  status: "active",
  createdHeight: 50,
  ...over,
});

const list = (deals: StorageDeal[], over: Partial<DealList> = {}): DealList => ({
  params: {
    minDealBytes: "1024",
    dealFee: "1000000",
    missThreshold: 2,
    protocolEveryEpochs: 7,
    protocolPieceBytes: "1048576",
    protocolPricePerEpoch: "500000000",
    protocolDurationEpochs: 365,
  },
  deals,
  hasOlder: false,
  ...over,
});

const deals = [
  deal(9, { class: "archive", protocol: true }),
  deal(8, { repairDelegate: "operator-1" }),
  deal(7, { class: "private", status: "expired" }),
];

describe("DealsCard", () => {
  it("TestDealsCard_counts_the_deals_and_lists_each_with_its_kind_state_and_price", () => {
    const out = html(<DealsCard state={ready(list(deals))} onRetry={noop} />);
    expect(out).toContain("9 deals made so far");
    expect(out).toContain("Of the newest 3, 2 still run and 1 were made by the protocol itself");
    expect(out).toContain("#9");
    expect(out).toContain("Archive");
    expect(out).toContain("protocol");
    expect(out).toContain("Public pin");
    expect(out).toContain("expired");
  });

  it("TestDealsCard_offers_a_filter_for_every_class_and_the_protocols_own_deals", () => {
    const out = html(<DealsCard state={ready(list(deals))} onRetry={noop} />);
    for (const label of ["All", "Private", "Public pin", "Archive", "Made by the protocol"]) expect(out).toContain(label);
  });

  it("TestDealsCard_says_when_older_deals_exist", () => {
    expect(html(<DealsCard state={ready(list(deals, { hasOlder: true }))} onRetry={noop} />)).toContain("Older deals exist");
    expect(html(<DealsCard state={ready(list(deals))} onRetry={noop} />)).not.toContain("Older deals exist");
  });

  it("TestDealsCard_a_chain_with_no_deals_says_so", () => {
    expect(html(<DealsCard state={ready(list([]))} onRetry={noop} />)).toContain("No storage deal has been made on this chain yet.");
  });

  it("TestDealsCard_loading_and_error_have_their_own_states", () => {
    expect(html(<DealsCard state={loading} onRetry={noop} />)).toContain('aria-label="Loading"');
    const out = html(<DealsCard state={failed("The chain could not be reached.")} onRetry={noop} />);
    expect(out).toContain("Could not load this.");
    expect(out).toContain("The chain could not be reached.");
  });

  it("TestDealsCard_a_deal_links_the_client_inside_the_network", () => {
    expect(html(<DealsCard state={ready(list(deals))} onRetry={noop} />)).toContain('href="/explorer/wallet/orama1');
  });
});

describe("deal logic", () => {
  it("TestFilterDeals_by_class_protocol_or_all", () => {
    expect(filterDeals(deals, "archive").map((d) => d.id)).toEqual([9]);
    expect(filterDeals(deals, "protocol").map((d) => d.id)).toEqual([9]);
    expect(filterDeals(deals, "all")).toHaveLength(3);
    expect(filterDeals(deals, "private").map((d) => d.id)).toEqual([7]);
  });

  it("TestDealTotals_the_latest_id_is_the_highest_not_the_first", () => {
    expect(dealTotals([deal(3), deal(11), deal(5)])).toEqual({ read: 3, running: 3, protocol: 0, latestId: 11 });
    expect(dealTotals([])).toEqual({ read: 0, running: 0, protocol: 0, latestId: null });
  });

  it("TestRepairDelegates_counts_the_deals_that_name_one", () => {
    expect(repairDelegates(deals)).toBe(1);
    expect(repairDelegates([])).toBe(0);
  });
});

describe("ProvidersCard", () => {
  const providers = [
    node("small", { declaredCapacityBytes: String(GIB), reservedCapacityBytes: String(GIB / 4n) }),
    node("big", { declaredCapacityBytes: String(4n * GIB), reservedCapacityBytes: String(GIB), bonds: [{ role: "storage", amount: "150000000000" }] }),
    node("relay-only", { roles: ["relay"], declaredCapacityBytes: String(100n * GIB) }),
    node("gone", { status: "retired", declaredCapacityBytes: String(100n * GIB) }),
  ];

  it("TestProvidersOf_keeps_live_storage_nodes_largest_declared_first", () => {
    expect(providersOf(providers).map((n) => n.id)).toEqual(["big", "small"]);
  });

  it("TestProvidersCard_sums_what_providers_declare_and_what_deals_reserved", () => {
    const out = html(<ProvidersCard state={ready(nodeSet(providers))} onRetry={noop} />);
    expect(out).toContain("2 providers declare 5 GiB between them");
    expect(out).toContain("reserved 1.2 GiB");
    expect(out).toContain('href="/explorer/node/big"');
    expect(out).toContain("25.0%");
    expect(out).not.toContain("relay-only");
    expect(out).toContain("not measured by the chain");
  });

  it("TestProvidersCard_no_provider_and_a_failed_read_have_their_own_states", () => {
    expect(html(<ProvidersCard state={ready(nodeSet([node("r", { roles: ["relay"] })]))} onRetry={noop} />)).toContain("No node runs the storage role yet.");
    expect(html(<ProvidersCard state={failed("down")} onRetry={noop} />)).toContain("Could not load this.");
    expect(html(<ProvidersCard state={loading} onRetry={noop} />)).toContain('aria-label="Loading"');
  });

  it("TestProvidersCard_a_provider_that_declares_nothing_has_no_utilisation", () => {
    const out = html(<ProvidersCard state={ready(nodeSet([node("zero")]))} onRetry={noop} />);
    expect(out).toContain("0 B");
    expect(out).not.toContain("NaN");
  });
});

describe("ArchiveCard", () => {
  const health = (over: Partial<ArchiveHealth> = {}): ArchiveHealth => ({
    tip: 1450,
    lastArchivedHeight: 400,
    retainHeight: 400,
    retentionWindowBlocks: 1000,
    rangeBlocks: 100,
    retainLag: 50,
    unarchivedRanges: 10,
    oldestUnarchived: [
      { start: 401, end: 500, state: "decided", attesters: 3, replicaDeals: 1 },
      { start: 501, end: 600, state: "none", attesters: 0, replicaDeals: 0 },
    ],
    ...over,
  });

  it("TestArchiveCard_shows_the_prefix_the_retain_height_the_lag_and_the_oldest_gaps", () => {
    const out = html(<ArchiveCard state={ready(health())} onRetry={noop} />);
    expect(out).toContain('href="/explorer/block/400"');
    expect(out).toContain("tip 1,450");
    expect(out).toContain("1,000 blocks are always kept");
    expect(out).toContain("50 blocks");
    expect(out).toContain("the archive holds pruning back by this much");
    expect(out).toContain("10 ranges");
    expect(out).toContain("401 to 500");
    expect(out).toContain("3 attesters (3 needed) · 1 deals (3 needed)");
    expect(out).toContain("decided, waiting for replica deals");
    expect(out).toContain("no attestation yet");
  });

  it("TestArchiveCard_a_young_chain_has_nothing_archived_and_nothing_prunable", () => {
    const out = html(<ArchiveCard state={ready(health({ lastArchivedHeight: 0, retainHeight: 0, retainLag: 0, tip: 50, unarchivedRanges: 0, oldestUnarchived: [] }))} onRetry={noop} />);
    expect(out).toContain("none yet");
    expect(out).toContain("nothing prunable");
    expect(out).toContain("the archive keeps up with the window");
    expect(out).toContain("Every complete range up to the tip is archived.");
  });

  it("TestArchiveCard_loading_and_error_have_their_own_states", () => {
    expect(html(<ArchiveCard state={loading} onRetry={noop} />)).toContain('aria-label="Loading"');
    expect(html(<ArchiveCard state={failed("The chain could not be read (HTTP 500).")} onRetry={noop} />)).toContain("HTTP 500");
  });
});

describe("RepairCard", () => {
  const queue = ready<SettlementQueue>({ pending: 12 });

  it("TestRepairCard_shows_the_queue_and_the_delegates_and_says_repair_work_is_off_chain", () => {
    const out = html(<RepairCard queue={queue} deals={ready(list(deals))} onRetry={noop} />);
    expect(out).toContain(">12</span> settlements waiting");
    expect(out).toContain(">1</span> of the newest 3 deals name an operator");
    expect(out).toContain("the chain keeps no record of it");
  });

  it("TestRepairCard_one_settlement_is_singular_and_a_failed_deal_read_leaves_the_delegate_line_out", () => {
    const out = html(<RepairCard queue={ready({ pending: 1 })} deals={failed("down")} onRetry={noop} />);
    expect(out).toContain("settlement waiting");
    expect(out).not.toContain("name an operator");
  });

  it("TestRepairCard_a_failed_queue_read_has_its_own_error", () => {
    expect(html(<RepairCard queue={failed("queue down")} deals={ready(list([]))} onRetry={noop} />)).toContain("queue down");
  });
});

describe("StoragePage", () => {
  it("TestStoragePage_renders_every_section_in_its_loading_state", () => {
    const source = createChainSource({ fetcher: new FakeGateway().fetch });
    const out = html(
      <ExplorerProvider source={source}>
        <StoragePage />
      </ExplorerProvider>,
    );
    for (const text of ["Storage and archive", "Deals", "Providers", "Block archive", "Settlement and repair"]) expect(out).toContain(text);
    expect(out).toContain('aria-label="Loading"');
  });
});
