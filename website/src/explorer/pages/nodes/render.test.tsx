import { renderToStaticMarkup } from "react-dom/server";
import type { ReactNode } from "react";
import { MemoryRouter } from "react-router";
import { describe, expect, it } from "vitest";
import type { QueryState } from "../../data/use-query";
import { node, nodeSet, OPERATOR } from "../../model/node-fixtures";
import type { NodeDetail } from "../../model/nodes";
import { Query } from "../../ui/query-states";
import { statusUrl } from "../../ui/status-link";
import { NodeBonds } from "../node/node-bonds";
import { NodeFacts } from "../node/node-facts";
import { NodeHeader } from "../node/node-header";
import { NodesBody } from "../nodes";
import { filterNodes, orderNodes } from "./node-table";

const html = (ui: ReactNode) => renderToStaticMarkup(<MemoryRouter>{ui}</MemoryRouter>);
const ORAMA = 1_000_000_000n;
const orama = (n: bigint) => (n * ORAMA).toString();

const fleet = [
  node("node-1", { roles: ["validator", "storage"], asn: 24940, regionHint: "eu-1", bonds: [{ role: "storage", amount: orama(150n) }, { role: "validator", amount: orama(500n) }] }),
  node("node-2", { roles: ["relay", "exit"], asn: 24940, status: "jailed" }),
  node("node-3", { roles: ["dirauth"], asn: 16276 }),
  node("node-4", { roles: ["storage"], status: "retired" }),
];

describe("NodesBody", () => {
  it("TestNodesBody_shows_counts_roles_asn_spread_and_every_node_with_a_link", () => {
    const out = html(<NodesBody set={nodeSet(fleet)} />);
    expect(out).toContain("3</div>");
    expect(out).toContain("1 jailed, 1 retired or tombstoned");
    expect(out).toContain("AS24940");
    expect(out).toContain("2 nodes · 66.7%");
    expect(out).toContain("AS16276");
    expect(out).toContain("Directory authority");
    expect(out).toContain('href="/explorer/node/node-1"');
    expect(out).toContain('href="/explorer/node/node-4"');
    expect(out).toContain("eu-1");
  });

  it("TestNodesBody_says_the_asn_is_the_operators_word", () => {
    const out = html(<NodesBody set={nodeSet(fleet)} />);
    expect(out).toContain("Operators declare their ASN and the chain cannot check it");
  });

  it("TestNodesBody_names_what_the_chain_does_not_keep", () => {
    const out = html(<NodesBody set={nodeSet(fleet)} />);
    expect(out).toContain("Not on the chain");
    expect(out).toContain("Software version");
    expect(out).toContain("Whether a node is up");
  });

  it("TestNodesBody_an_empty_chain_says_so_instead_of_showing_zero_bars", () => {
    const out = html(<NodesBody set={nodeSet([])} />);
    expect(out).toContain("No node is registered on this chain yet.");
    expect(out).toContain("No live node to count.");
    expect(out).not.toContain("AS0");
  });

  it("TestNodesBody_a_set_cut_at_the_pages_read_says_every_figure_covers_those_only", () => {
    const out = html(<NodesBody set={nodeSet(fleet, { truncated: true })} />);
    expect(out).toContain("The chain holds more nodes than the 4 the explorer reads");
    expect(html(<NodesBody set={nodeSet(fleet)} />)).not.toContain("holds more nodes");
  });

  it("TestNodesBody_nodes_with_no_declared_asn_are_counted_as_undeclared", () => {
    const out = html(<NodesBody set={nodeSet([node("a"), node("b")])} />);
    expect(out).toContain("No ASN declared");
    expect(out).toContain("no node declared one");
  });

  it("TestNodesBody_text_an_operator_wrote_is_escaped", () => {
    const out = html(<NodesBody set={nodeSet([node("a", { regionHint: "<img src=x onerror=alert(1)>" })])} />);
    expect(out).not.toContain("<img");
    expect(out).toContain("&lt;img src=x onerror=alert(1)&gt;");
  });
});

describe("node list order and filter", () => {
  it("TestOrderNodes_puts_live_nodes_first_then_sorts_by_id", () => {
    expect(orderNodes(fleet).map((n) => n.id)).toEqual(["node-1", "node-2", "node-3", "node-4"]);
    expect(orderNodes([node("b", { status: "retired" }), node("c"), node("a", { status: "tombstoned" })]).map((n) => n.id)).toEqual(["c", "a", "b"]);
  });

  it("TestFilterNodes_by_role_or_all", () => {
    expect(filterNodes(fleet, "storage").map((n) => n.id)).toEqual(["node-1", "node-4"]);
    expect(filterNodes(fleet, "all")).toHaveLength(4);
    expect(filterNodes(fleet, "archiver")).toEqual([]);
  });
});

describe("the nodes page states", () => {
  const loading: QueryState<string> = { status: "loading", data: null, error: null };
  const failed: QueryState<string> = { status: "error", data: null, error: new Error("The chain could not be reached.") };

  it("TestNodesPage_loading_and_error_use_the_shared_states", () => {
    expect(html(<Query state={loading}>{() => <p>ready</p>}</Query>)).toContain('aria-label="Loading"');
    const out = html(<Query state={failed} onRetry={() => undefined}>{() => <p>ready</p>}</Query>);
    expect(out).toContain("Could not load this.");
    expect(out).toContain("The chain could not be reached.");
    expect(out).toContain("Try again");
  });

  it("TestStatusUrl_is_the_gateways_status_page_and_absent_when_no_gateway_is_known", () => {
    expect(statusUrl("https://stagenet.orama.network")).toBe("https://stagenet.orama.network/status");
    expect(statusUrl(null)).toBeNull();
  });
});

describe("node detail", () => {
  const detail = (over: Partial<NodeDetail> = {}): NodeDetail => ({
    node: fleet[0]!,
    params: nodeSet([]).params,
    name: null,
    unbondings: [],
    ...over,
  });

  it("TestNodeHeader_names_the_node_its_state_roles_operator_and_block", () => {
    const out = html(<NodeHeader detail={detail({ name: "alpha" })} />);
    expect(out).toContain("node-1");
    expect(out).toContain("active");
    expect(out).toContain("Validator");
    expect(out).toContain("Storage provider");
    expect(out).toContain(`href="/explorer/wallet/${OPERATOR}"`);
    expect(out).toContain('href="/explorer/block/1"');
    expect(out).toContain("alpha");
  });

  it("TestNodeHeader_a_node_with_no_name_or_hot_key_says_nothing_about_them", () => {
    const out = html(<NodeHeader detail={detail()} />);
    expect(out).not.toContain("the operator claimed it");
    expect(out).toContain("none");
  });

  it("TestNodeBonds_marks_a_role_below_its_minimum", () => {
    const out = html(<NodeBonds detail={detail({ node: node("n", { roles: ["storage", "validator"], bonds: [{ role: "storage", amount: "100" }] }) })} />);
    expect(out).toContain("active");
    expect(out).toContain("below minimum");
    expect(out).toContain("Nothing is unbonding.");
  });

  it("TestNodeBonds_lists_the_bond_on_its_way_out_with_its_release_time", () => {
    const out = html(
      <NodeBonds detail={detail({ unbondings: [{ id: 4, role: "storage", amount: orama(5n), completesAt: "2026-10-03T04:00:00.000Z" }] })} />,
    );
    expect(out).toContain("from Storage provider");
    expect(out).toContain("2026-10-03 04:00:00 UTC");
    expect(out).toContain("Bond stays locked 1 day");
  });

  it("TestNodeBonds_a_node_with_no_role_has_nothing_to_bond", () => {
    expect(html(<NodeBonds detail={detail({ node: node("n", { roles: [] }) })} />)).toContain("holds no role");
  });

  it("TestNodeFacts_shows_declared_endpoints_as_text_and_says_the_chain_cannot_check_them", () => {
    const out = html(
      <NodeFacts
        detail={detail({
          node: node("n", { endpoints: ["https://1.2.3.4:443"], asn: 24940, regionHint: "eu-1", declaredCapacityBytes: String(2n * 1024n ** 3n), reservedCapacityBytes: String(1024n ** 3n) }),
        })}
      />,
    );
    expect(out).toContain("https://1.2.3.4:443");
    expect(out).not.toContain('href="https://1.2.3.4');
    expect(out).toContain("AS24940");
    expect(out).toContain("2 GiB");
    expect(out).toContain("50.0% of what it declared");
    expect(out).toContain("cannot check them");
  });

  it("TestNodeFacts_a_node_without_the_storage_role_shows_no_capacity", () => {
    const out = html(<NodeFacts detail={detail({ node: node("n", { roles: ["relay"] }) })} />);
    expect(out).not.toContain("Storage declared");
    expect(out).toContain("none declared");
  });
});
