import { renderToStaticMarkup } from "react-dom/server";
import type { ReactNode } from "react";
import { MemoryRouter } from "react-router";
import { describe, expect, it } from "vitest";
import { FakeGateway } from "../../data/chain/fake-gateway";
import { createChainSource } from "../../data/chain/source";
import { ExplorerProvider } from "../../data/provider";
import type { QueryState } from "../../data/use-query";
import { node, nodeSet } from "../../model/node-fixtures";
import type { RelayEpoch, TorRelay, TorRelays } from "../../model/tor";
import { TorPage } from "../tor";
import { NodeRolesCard } from "./node-roles-card";
import { RelayTable, shortFingerprint } from "./relay-table";
import { RewardsCard } from "./rewards-card";

const html = (ui: ReactNode) => renderToStaticMarkup(<MemoryRouter>{ui}</MemoryRouter>);
const ready = <T,>(data: T): QueryState<T> => ({ status: "ready", data, error: null });
const failed = <T,>(message: string): QueryState<T> => ({ status: "error", data: null, error: new Error(message) });
const loading = { status: "loading", data: null, error: null } as const;
const noop = () => undefined;
const OPERATOR = "orama1sx0x6xpv8wtgshrajsqgednruq26aes0h2jh07";

const relay = (mark: number, over: Partial<TorRelay> = {}): TorRelay => ({
  fingerprint: mark.toString(16).padStart(2, "0").repeat(20).toUpperCase(),
  nodeId: `node-${mark}`,
  operator: OPERATOR,
  prefix16: `10.${mark}.0.0/16`,
  exit: false,
  jailed: false,
  ...over,
});

const tor = (relays: TorRelay[], over: Partial<TorRelays> = {}): TorRelays => ({
  relays,
  truncated: false,
  params: {
    reportersQuorum: 2,
    minUptime: 0.8,
    exitMultiplier: 2,
    perRelayCap: "1000000000",
    perOperatorCap: "3000000000",
    perPrefix16Cap: "5000000000",
  },
  reporters: [OPERATOR],
  lastSettlement: null,
  payouts: new Map(),
  settlementEpoch: 10,
  ...over,
});

const settled: RelayEpoch = { epoch: 10, quorumMet: true, activating: false, ceiling: "1000000000000", minted: "400000000000" };

describe("RelayTable", () => {
  const relays = [relay(1), relay(2, { exit: true }), relay(3, { jailed: true, nodeId: "gone" })];
  const nodes = nodeSet([node("node-1", { roles: ["relay"] }), node("node-2", { roles: ["relay", "exit"], status: "jailed" })]);

  it("TestRelayTable_lists_each_relay_with_its_node_network_and_kind", () => {
    const out = html(<RelayTable tor={tor(relays)} nodes={nodes} />);
    expect(out).toContain("3 relays registered, 2 on a node that still serves");
    expect(out).toContain('href="/explorer/node/node-1"');
    expect(out).toContain("10.2.0.0/16");
    expect(out).toContain("declares exit");
    expect(out).toContain("jailed");
    expect(out).toContain(`title="${"01".repeat(20)}"`);
    expect(out).toContain("010101010101…");
  });

  it("TestRelayTable_shows_what_the_newest_settled_epoch_paid_each_relay", () => {
    const paid = tor(relays, { lastSettlement: settled, payouts: new Map([[relays[1]!.fingerprint, "7770000000"]]) });
    const out = html(<RelayTable tor={paid} nodes={nodes} />);
    expect(out).toContain(">Paid</th>");
    expect(out).toContain("7.77");
    const unpaid = html(<RelayTable tor={tor(relays)} nodes={nodes} />);
    expect(unpaid).not.toContain("7.77");
  });

  it("TestRelayTable_without_the_node_list_it_still_shows_relays_and_makes_no_claim_about_nodes", () => {
    const out = html(<RelayTable tor={tor(relays)} nodes={null} />);
    expect(out).toContain("3 relays registered.");
    expect(out).not.toContain("still serves");
  });

  it("TestRelayTable_a_cut_node_list_makes_no_claim_about_nodes_that_still_serve", () => {
    expect(html(<RelayTable tor={tor(relays)} nodes={{ ...nodes, truncated: true }} />)).not.toContain("still serves");
  });

  it("TestRelayTable_no_relay_says_so", () => {
    expect(html(<RelayTable tor={tor([])} nodes={nodes} />)).toContain("No relay is registered on this chain yet.");
  });

  it("TestRelayTable_a_cut_list_says_every_figure_covers_the_relays_read", () => {
    expect(html(<RelayTable tor={tor(relays, { truncated: true })} nodes={null} />)).toContain("holds more relays than the 3");
  });

  it("TestShortFingerprint_keeps_the_start_and_a_short_one_whole", () => {
    expect(shortFingerprint("AB".repeat(20))).toBe("ABABABABABAB…");
    expect(shortFingerprint("ABCD")).toBe("ABCD");
  });
});

describe("RewardsCard", () => {
  it("TestRewardsCard_shows_the_rules_the_reporters_and_the_newest_settlement", () => {
    const out = html(<RewardsCard tor={tor([relay(1)], { lastSettlement: settled })} />);
    expect(out).toContain("2 distinct reporters must submit");
    expect(out).toContain("under 80.0% median uptime");
    expect(out).toContain("2x");
    expect(out).toContain('href="/explorer/wallet/orama1');
    expect(out).toContain("Epoch 10: enough reporters reported");
    expect(out).toContain("400 ORAMA");
    expect(out).toContain("1,000 ORAMA");
  });

  it("TestRewardsCard_an_epoch_without_quorum_says_nothing_was_paid", () => {
    const out = html(<RewardsCard tor={tor([], { lastSettlement: { ...settled, quorumMet: false, minted: "0" } })} />);
    expect(out).toContain("too few reporters reported, so nothing was paid");
  });

  it("TestRewardsCard_the_activating_epoch_mints_nothing_and_says_so", () => {
    expect(html(<RewardsCard tor={tor([], { lastSettlement: { ...settled, activating: true } })} />)).toContain("switched relay rewards on");
  });

  it("TestRewardsCard_no_settlement_names_the_epoch_it_looked_for", () => {
    expect(html(<RewardsCard tor={tor([])} />)).toContain("holds no relay settlement for epoch 10");
    expect(html(<RewardsCard tor={tor([], { settlementEpoch: 0 })} />)).toContain("No epoch is past its report window yet.");
  });

  it("TestRewardsCard_no_reporters_says_none", () => {
    expect(html(<RewardsCard tor={tor([], { reporters: [] })} />)).toContain("none");
  });
});

describe("NodeRolesCard", () => {
  const nodes = nodeSet([
    node("auth-1", { roles: ["dirauth"], endpoints: ["https://9.9.9.9:443"] }),
    node("relay-1", { roles: ["relay"] }),
    node("relay-2", { roles: ["relay", "exit"] }),
    node("storage-1", { roles: ["storage"] }),
  ]);
  const registered = ready({ relays: [relay(1, { nodeId: "relay-1" })], truncated: false });

  it("TestNodeRolesCard_lists_the_directory_authorities_and_the_relay_and_exit_nodes", () => {
    const out = html(<NodeRolesCard nodes={ready(nodes)} relays={registered} onRetry={noop} />);
    expect(out).toContain("Directory authorities (1)");
    expect(out).toContain("https://9.9.9.9:443");
    expect(out).toContain("relay role (2) and the exit role (1)");
    expect(out).toContain('href="/explorer/node/relay-2"');
    expect(out).not.toContain("storage-1");
  });

  it("TestNodeRolesCard_names_relay_nodes_that_registered_no_relay_with_x_relay", () => {
    const out = html(<NodeRolesCard nodes={ready(nodes)} relays={registered} onRetry={noop} />);
    expect(out).toContain("1 of those node has no relay registered with x/relay");
    expect(out).toContain("relay-2");
  });

  it("TestNodeRolesCard_makes_no_claim_about_registration_while_the_relays_are_not_read", () => {
    expect(html(<NodeRolesCard nodes={ready(nodes)} relays={loading} onRetry={noop} />)).not.toContain("no relay registered");
    expect(html(<NodeRolesCard nodes={ready(nodes)} relays={failed("down")} onRetry={noop} />)).not.toContain("no relay registered");
  });

  it("TestNodeRolesCard_a_cut_relay_list_proves_nothing_about_a_node_having_no_relay", () => {
    const cut = ready({ relays: [relay(1, { nodeId: "relay-1" })], truncated: true });
    expect(html(<NodeRolesCard nodes={ready(nodes)} relays={cut} onRetry={noop} />)).not.toContain("no relay registered");
  });

  it("TestNodeRolesCard_a_chain_with_no_tor_roles_says_so", () => {
    const out = html(<NodeRolesCard nodes={ready(nodeSet([node("s")]))} relays={registered} onRetry={noop} />);
    expect(out).toContain("No node holds the directory authority role.");
    expect(out).toContain("No node holds a relay or exit role.");
  });

  it("TestNodeRolesCard_loading_and_error_have_their_own_states", () => {
    expect(html(<NodeRolesCard nodes={loading} relays={registered} onRetry={noop} />)).toContain('aria-label="Loading"');
    expect(html(<NodeRolesCard nodes={failed("The chain could not be reached.")} relays={registered} onRetry={noop} />)).toContain("Could not load this.");
  });
});

describe("TorPage", () => {
  it("TestTorPage_loading_shows_the_header_and_what_the_chain_does_not_keep", () => {
    const source = createChainSource({ fetcher: new FakeGateway().fetch });
    const out = html(
      <ExplorerProvider source={source}>
        <TorPage />
      </ExplorerProvider>,
    );
    expect(out).toContain("Tor and relays");
    expect(out).toContain('aria-label="Loading"');
    expect(out).toContain("Validators&#x27; onion services");
    expect(out).toContain("Whether a relay is in the consensus");
    expect(out).toContain("Bandwidth and uptime of a relay");
  });
});
