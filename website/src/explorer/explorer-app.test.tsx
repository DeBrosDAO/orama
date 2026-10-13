import { describe, expect, it } from "vitest";
import { render } from "../entry-server";

const HASH = "A91F3C0000000000000000000000000000000000000000000000000000BC03BC";
const ADDRESS = "orama1q8w9k3v5r2m4x7h6d0n8c1p3t5y9j2u4l6e7x2";
const SITE_404 = "This page doesn&#x27;t exist.";

/**
 * The explorer is a client-side app: pages load their data in effects, so the
 * server render is the shell and each page's loading state. This proves every
 * route mounts without touching browser-only APIs during render and never
 * falls through to the site's 404.
 */
describe("explorer routes render on the server", () => {
  it.each([
    ["/explorer", "Follow any transaction"],
    ["/explorer/validators", "Who runs the chain"],
    ["/explorer/epochs", "Closed epochs"],
    ["/explorer/epochs?page=3", "Closed epochs"],
    ["/explorer/epoch/731", "Neighbouring epochs"],
    ["/explorer/epoch/not-a-number", "That is not an epoch number"],
    ["/explorer/emission", "Emission"],
    ["/explorer/economics", "Supply and economics"],
    ["/explorer/nodes", "The machines that make up"],
    ["/explorer/node/node-1", "Search address, transaction, block"],
    ["/explorer/storage", "Storage and archive"],
    ["/explorer/tor", "Tor and relays"],
    [`/explorer/tx/${HASH}`, "Search address, transaction, block"],
    [`/explorer/wallet/${ADDRESS}`, "Search address, transaction, block"],
    ["/explorer/block/42", "Search address, transaction, block"],
    ["/explorer/tokens", "Open a token"],
    [`/explorer/token/${ADDRESS}/cash`, "Search address, transaction, block"],
    ["/explorer/token/nobody/cash", "Token not found"],
    ["/explorer/nfts", "Find an NFT"],
    [`/explorer/nfts/${ADDRESS}`, "Not an address"],
    ["/explorer/nft/not-an-asset-id", "NFT not found"],
    ["/explorer/collection/3", "Search address, transaction, block"],
    ["/explorer/collection/0", "Collection not found"],
    ["/explorer/contracts", "Open a contract or a code"],
    ["/explorer/code/abc", "Code not found"],
    ["/explorer/contract/nope", "Contract not found"],
    ["/explorer/market", "Open a listing"],
    ["/explorer/listing/0", "Listing not found"],
    ["/explorer/governance", "Open a proposal"],
    ["/explorer/proposal/abc", "Proposal not found"],
    ["/explorer/shielded", "What is public and what is private"],
    ["/explorer/no-such-page", "not in the explorer"],
  ])("TestExplorerRender_%s", async (path, text) => {
    const html = await render(path);
    expect(html).toContain(text);
    expect(html).not.toContain(SITE_404);
    expect(html).not.toContain("<!--$?-->");
  }, 20_000);

  it("TestExplorerRender_says_it_shows_live_data_and_never_demo_data", async () => {
    for (const path of [
      "/explorer",
      "/explorer/validators",
      "/explorer/block/1",
      "/explorer/epochs",
      "/explorer/emission",
      "/explorer/economics",
      "/explorer/nodes",
      "/explorer/storage",
      "/explorer/tor",
      "/explorer/tokens",
      "/explorer/nfts",
      "/explorer/contracts",
      "/explorer/market",
      "/explorer/governance",
      "/explorer/shielded",
    ]) {
      const html = await render(path);
      expect(html).toContain("Live data from");
      expect(html).not.toContain("demo");
    }
  });
});
