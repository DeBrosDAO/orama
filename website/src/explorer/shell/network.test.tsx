import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter, Route, Routes } from "react-router";
import type { InitialEntry } from "react-router";
import { afterEach, describe, expect, it } from "vitest";
import { ExplorerApp } from "../explorer-app";
import { NetworkProvider } from "../network/active";
import { confirmGateway, forgetConfirmedGateways } from "../network/gate";
import { buildRegistry } from "../network/registry";
import { selectNetwork } from "../network/selection";
import { LinkResetPage, ResetBanner } from "./reset-notice";

const SHA = "84ee8d46628343b85c464185e1009974beb34f91c99477a75a5dfb66a5918b67";
const manifest = (name: string, id: number) => ({
  name,
  chain_id: `orama-${name}-${id}`,
  genesis_sha256: SHA,
  seeds: [`ns1.${name}.orama.network`, `ns2.${name}.orama.network`],
});
const REGISTRY = buildRegistry({
  "../../../../networks/stagenet/manifest.json": manifest("stagenet", 7),
  "../../../../networks/testnet/manifest.json": manifest("testnet", 2),
});

function app(entry: InitialEntry): string {
  return renderToStaticMarkup(
    <MemoryRouter initialEntries={[entry]}>
      <Routes>
        <Route path="explorer/*" element={<ExplorerApp registry={REGISTRY} />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("the network switcher", () => {
  it("TestNetworkBar_lists_every_created_network_and_a_custom_gateway_and_selects_the_default", () => {
    const html = app("/explorer");
    expect(html).toContain('<label for=');
    expect(html).toMatch(/<option value="stagenet" selected="">stagenet<\/option>/);
    expect(html).toContain('<option value="testnet">testnet</option>');
    expect(html).toContain("Custom gateway…");
  });

  it("TestNetworkBar_always_shows_the_chain_id_the_registry_lists_until_the_gateway_answers", () => {
    expect(app("/explorer")).toContain("orama-stagenet-7");
    expect(app("/explorer/validators")).toContain("orama-stagenet-7");
    expect(app("/explorer/testnet")).toContain("orama-testnet-2");
  });

  it("TestNetworkBar_a_network_segment_selects_that_network_and_names_where_the_data_comes_from", () => {
    const html = app("/explorer/testnet/block/5");
    expect(html).toMatch(/<option value="testnet" selected="">/);
    expect(html).toContain("Live data from testnet at testnet.orama.network");
  });

  it("TestNetworkBar_the_nodes_storage_and_tor_pages_are_per_network", () => {
    const html = app("/explorer/testnet");
    for (const page of ["nodes", "storage", "tor"]) expect(html).toContain(`href="/explorer/testnet/${page}"`);
    const own = app("/explorer");
    for (const page of ["nodes", "storage", "tor"]) expect(own).toContain(`href="/explorer/${page}"`);
  });

  it("TestNodesPage_links_the_status_page_of_the_network_being_read", () => {
    expect(app("/explorer/testnet/nodes")).toContain('href="https://testnet.orama.network/status"');
    expect(app("/explorer/nodes")).toContain('href="https://stagenet.orama.network/status"');
  });

  it("TestNodesPage_a_custom_network_without_a_gateway_has_no_status_link", () => {
    expect(app("/explorer/custom/nodes")).not.toContain("/status");
  });

  it("TestNetworkBar_links_inside_a_network_stay_in_it", () => {
    const html = app("/explorer/testnet");
    expect(html).toContain('href="/explorer/testnet/validators"');
    expect(html).not.toContain('href="/explorer/validators"');
    expect(app("/explorer")).toContain('href="/explorer/validators"');
  });

  it("TestExplorerApp_an_unknown_page_under_a_network_is_still_not_found", () => {
    expect(app("/explorer/testnet/no-such-page")).toContain("not in the explorer");
  });
});

describe("a custom gateway", () => {
  afterEach(forgetConfirmedGateways);
  const GATEWAY = "https://gateway.example.org";
  const search = `?gateway=${encodeURIComponent(GATEWAY)}`;

  it("TestCustom_without_a_gateway_asks_for_one_and_reads_nothing", () => {
    const html = app("/explorer/custom");
    expect(html).toContain("Read a gateway you name");
    expect(html).toContain('aria-label="Read from a gateway you name"');
    expect(html).not.toContain("Live data from");
  });

  it("TestCustom_a_link_naming_a_gateway_asks_before_any_request_is_made", () => {
    const html = app({ pathname: "/explorer/custom", search });
    expect(html).toContain("This link reads from a gateway Orama does not list");
    expect(html).toContain(GATEWAY);
    expect(html).not.toContain("Live data from");
  });

  it("TestCustom_a_gateway_the_reader_typed_is_read_and_marked_as_not_in_the_registry", () => {
    confirmGateway(GATEWAY);
    const html = app({ pathname: "/explorer/custom", search });
    expect(html).toContain("Live data from Custom gateway at gateway.example.org");
    expect(html).toContain("Not in the Orama registry");
    expect(html).toContain('href="/explorer/custom/validators?gateway=https%3A%2F%2Fgateway.example.org"');
  });

  it("TestCustom_agreeing_to_one_gateway_does_not_cover_another", () => {
    confirmGateway("https://other.example.org");
    const html = app({ pathname: "/explorer/custom", search });
    expect(html).toContain("This link reads from a gateway Orama does not list");
  });

  it("TestCustom_a_refused_gateway_says_why_and_is_never_read", () => {
    const html = app({ pathname: "/explorer/custom", search: `?gateway=${encodeURIComponent("http://gateway.example.org")}` });
    expect(html).toContain("is not an https gateway origin");
    expect(html).not.toContain("Live data from");
  });

  it("TestCustom_a_hostile_gateway_value_is_text_never_markup", () => {
    const html = app({ pathname: "/explorer/custom", search: `?gateway=${encodeURIComponent("<img src=x onerror=alert(1)>")}` });
    expect(html).not.toContain("<img src=x");
  });
});

function withNetwork(path: string, ui: React.ReactNode): string {
  const selection = selectNetwork(path, "", REGISTRY);
  return renderToStaticMarkup(
    <MemoryRouter initialEntries={[path]}>
      <NetworkProvider value={{ ...selection, registry: REGISTRY }}>{ui}</NetworkProvider>
    </MemoryRouter>,
  );
}

describe("the reset states", () => {
  it("TestResetBanner_names_both_chain_ids_and_says_which_one_is_shown", () => {
    const html = withNetwork("/explorer", <ResetBanner state={{ kind: "registry", expected: "orama-stagenet-8", live: "orama-stagenet-7" }} />);
    expect(html).toContain("This network has been reset.");
    expect(html).toContain("orama-stagenet-8");
    expect(html).toContain("orama-stagenet-7");
    expect(html).toContain('role="status"');
  });

  it("TestLinkResetPage_replaces_the_page_and_leads_to_the_current_chain", () => {
    const html = withNetwork("/explorer/testnet", <LinkResetPage state={{ kind: "link", pinned: "orama-testnet-1", live: "orama-testnet-2" }} />);
    expect(html).toContain("This network has been reset");
    expect(html).toContain("orama-testnet-1");
    expect(html).toContain("orama-testnet-2");
    expect(html).toContain("The testnet network");
    expect(html).toContain('href="/explorer/testnet"');
    expect(html).toContain('role="alert"');
  });

  it("TestResetNotices_chain_text_from_a_hostile_gateway_is_cleaned", () => {
    const html = withNetwork("/explorer", <ResetBanner state={{ kind: "registry", expected: "a-1", live: "x‮evil" }} />);
    expect(html).not.toContain("‮");
  });
});
