import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter } from "react-router";
import { describe, expect, it } from "vitest";
import { assetPathsFor } from "../model/assets-routes";
import { AssetsMenu, menuItems } from "./assets-menu";

const html = (node: React.ReactNode, url: string) => renderToStaticMarkup(<MemoryRouter initialEntries={[url]}>{node}</MemoryRouter>);

describe("menuItems", () => {
  it("TestMenuItems_name_every_asset_page_once_under_the_network", () => {
    const items = menuItems(assetPathsFor("/explorer/testnet", "?gateway=g"));
    expect(items.map((i) => i.label)).toEqual(["Tokens", "NFTs", "Contracts", "Market", "Governance", "Shielded pool"]);
    expect(items.map((i) => i.to)).toEqual([
      "/explorer/testnet/tokens?gateway=g",
      "/explorer/testnet/nfts?gateway=g",
      "/explorer/testnet/contracts?gateway=g",
      "/explorer/testnet/market?gateway=g",
      "/explorer/testnet/governance?gateway=g",
      "/explorer/testnet/shielded?gateway=g",
    ]);
  });

  it("TestMenuItems_a_detail_page_keeps_its_section_lit", () => {
    const items = menuItems(assetPathsFor("/explorer", ""));
    const owner = (kind: string) => items.find((i) => i.kinds.includes(kind))?.label;
    expect([owner("token"), owner("collection"), owner("code"), owner("listing"), owner("proposal"), owner("shielded")]).toEqual([
      "Tokens",
      "NFTs",
      "Contracts",
      "Market",
      "Governance",
      "Shielded pool",
    ]);
  });
});

describe("AssetsMenu", () => {
  it("TestAssetsMenu_a_wide_header_gets_one_closed_menu_button", () => {
    const out = html(<AssetsMenu />, "/explorer");
    expect(out).toContain("Browse");
    expect(out).toContain('aria-expanded="false"');
    expect(out).not.toContain("/explorer/tokens");
  });

  it("TestAssetsMenu_a_narrow_header_gets_plain_links_and_lights_the_current_section", () => {
    const out = html(<AssetsMenu inline />, "/explorer/proposal/4");
    for (const href of ["/explorer/tokens", "/explorer/nfts", "/explorer/contracts", "/explorer/market", "/explorer/governance", "/explorer/shielded"]) {
      expect(out).toContain(`href="${href}"`);
    }
    expect(out).toMatch(/class="[^"]*bg-white\/\[0\.07\][^"]*" href="\/explorer\/governance"/);
    expect(out).not.toMatch(/class="[^"]*bg-white\/\[0\.07\][^"]*" href="\/explorer\/tokens"/);
  });
});
