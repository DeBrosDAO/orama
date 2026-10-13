import { describe, expect, it } from "vitest";
import { addr } from "../data/chain/fake-gateway";
import type { Collection, NftRecord } from "../model/nfts";
import type { IdPage } from "../model/paging";
import type { TokenDetail, TokenHolder, TokenRow } from "../model/tokens";
import { failedPaged, keyPaged, loadingPaged, readyPaged, render } from "./asset-fixtures";
import { AssetTable } from "./nft/asset-table";
import { RecordCard } from "./nft/record-card";
import { TreeView } from "./nft/tree-card";
import { CollectionList } from "./nfts/collection-list";
import { OwnedList } from "./owned-nfts";
import { HoldersCard } from "./token/holders-card";
import { FrozenAnswer } from "./token/frozen-check";
import { PowersCard, TokenFacts, TokenHeader } from "./token/token-summary";
import { TokenList } from "./tokens/token-list";

const CREATOR = addr(3);
const row = (n: number): TokenRow => ({ denom: `factory/${CREATOR}/tok${n}`, creator: CREATOR, subdenom: `tok${n}`, supply: "1234567" });

const token = (over: Partial<TokenDetail> = {}): TokenDetail => ({
  denom: `factory/${CREATOR}/cash`,
  creator: CREATOR,
  subdenom: "cash",
  name: "Cash",
  symbol: "CASH",
  description: "",
  extensions: { mint: false, freeze: false, permanentDelegate: null, transferFeeBps: 0, nonTransferable: false, pause: false, transferHook: null },
  paused: false,
  shieldable: false,
  issued: "1000000",
  depositAmount: "70000000",
  ...over,
});

describe("TokenList", () => {
  it("TestTokenList_lists_each_token_with_its_page_link_creator_and_supply", () => {
    const html = render(<TokenList paged={readyPaged([row(1), row(2)])} />);
    expect(html).toContain(`href="/explorer/token/${CREATOR}/tok1"`);
    expect(html).toContain("1,234,567");
    expect(html).toContain(`title="${CREATOR}"`);
    expect(html).not.toContain("Show more");
  });

  it("TestTokenList_a_list_with_more_offers_show_more_and_a_failed_try_says_why", () => {
    expect(render(<TokenList paged={readyPaged([row(1)], "a2V5")} />)).toContain("Show more tokens");
    const html = render(<TokenList paged={keyPaged({ status: "ready", data: { items: [row(1)], nextKey: "a2V5" }, error: null }, { moreError: new Error("The chain could not be reached.") })} />);
    expect(html).toContain("Could not load more tokens: The chain could not be reached.");
    expect(html).toContain("Try again");
  });

  it("TestTokenList_no_tokens_loading_and_error_each_say_so", () => {
    expect(render(<TokenList paged={readyPaged<TokenRow>([])} />)).toContain("No token has been created on this chain yet.");
    expect(render(<TokenList paged={loadingPaged<TokenRow>()} />)).toContain('aria-label="Loading"');
    const html = render(<TokenList paged={failedPaged<TokenRow>("The chain could not be read (HTTP 502).")} />);
    expect(html).toContain("Could not load this.");
    expect(html).toContain("HTTP 502");
  });
});

describe("a token page", () => {
  it("TestTokenHeader_names_the_token_and_flags_a_paused_or_shieldable_one", () => {
    const html = render(<TokenHeader token={token({ paused: true, shieldable: true })} />);
    expect(html).toContain("Cash");
    expect(html).toContain("(CASH)");
    expect(html).toContain("Paused");
    expect(html).toContain("Can enter the shielded pool");
    expect(render(<TokenHeader token={token()} />)).not.toContain("Paused");
  });

  it("TestTokenFacts_shows_supply_in_whole_units_the_deposit_in_orama_and_no_description_as_absent", () => {
    const html = render(<TokenFacts token={token()} />);
    expect(html).toContain("1,000,000");
    expect(html).toContain("0.07 ORAMA");
    expect(html).toContain("None written");
  });

  it("TestPowersCard_a_token_with_nothing_held_says_the_creator_has_no_power_and_one_with_powers_lists_them", () => {
    expect(render(<PowersCard token={token()} />)).toContain("holds no special power");
    const held = render(
      <PowersCard token={token({ extensions: { mint: true, freeze: false, permanentDelegate: addr(5), transferFeeBps: 100, nonTransferable: false, pause: false, transferHook: addr(6) } })} />,
    );
    expect(held).toContain("The creator can mint more");
    expect(held).toContain("1% of every transfer is burned");
    expect(held).toContain(`href="/explorer/contract/${addr(6)}"`);
    expect(held).toContain(`href="/explorer/wallet/${addr(5)}"`);
  });

  it("TestHoldersCard_lists_balances_says_none_and_warns_that_shielded_balances_are_not_counted", () => {
    const holder: TokenHolder = { address: addr(1), balance: "2500" };
    const html = render(<HoldersCard paged={readyPaged([holder])} />);
    expect(html).toContain("2,500");
    expect(html).toContain("not by size");
    expect(render(<HoldersCard paged={readyPaged<TokenHolder>([])} />)).toContain("No account holds this token.");
    expect(render(<HoldersCard paged={failedPaged<TokenHolder>("boom")} />)).toContain("boom");
  });

  it("TestFrozenAnswer_says_frozen_or_not", () => {
    expect(render(<FrozenAnswer frozen />)).toContain("cannot send, receive, mint or burn");
    expect(render(<FrozenAnswer frozen={false} />)).toContain("Not frozen");
  });
});

const collection = (id: number): Collection => ({ id, creator: addr(2), royaltyBps: 250, name: id === 2 ? "" : `Apes ${id}` });
const page = (items: Collection[], next: number | null): IdPage<Collection> => ({ newest: 9, items, next });

describe("CollectionList", () => {
  it("TestCollectionList_links_each_collection_shows_its_royalty_and_offers_older_ones", () => {
    const html = render(<CollectionList page={page([collection(9), collection(8)], 7)} atNewest />);
    expect(html).toContain('href="/explorer/collection/9"');
    expect(html).toContain("Apes 9");
    expect(html).toContain("2.5%");
    expect(html).toContain("?from=7");
  });

  it("TestCollectionList_an_older_page_links_back_to_the_newest_and_an_unnamed_collection_gets_its_number", () => {
    const html = render(<CollectionList page={page([collection(2), collection(1)], null)} atNewest={false} />, "/?from=2");
    expect(html).toContain("Collection 2");
    expect(html).toContain("Newest");
    expect(html).not.toContain("Older");
  });

  it("TestCollectionList_an_empty_chain_and_an_empty_older_page_say_different_things", () => {
    expect(render(<CollectionList page={page([], null)} atNewest />)).toContain("No collection has been created on this chain yet.");
    expect(render(<CollectionList page={page([], null)} atNewest={false} />)).toContain("no collection at that number");
  });
});

const record = (over: Partial<NftRecord> = {}): NftRecord => ({
  id: "ab".repeat(32),
  state: "compressed",
  treeId: 7,
  leafIndex: 4,
  collectionId: 3,
  owner: addr(1),
  delegate: null,
  metadataCid: "bafybeigdyrzt",
  nonce: "2",
  updatedHeight: 90,
  updatedTx: "CD".repeat(32),
  ...over,
});

describe("NFT records", () => {
  it("TestRecordCard_shows_owner_collection_tree_metadata_id_and_the_last_change", () => {
    const html = render(<RecordCard record={record()} title="Asset" />);
    expect(html).toContain(`href="/explorer/wallet/${addr(1)}"`);
    expect(html).toContain('href="/explorer/collection/3"');
    expect(html).toContain("tree 7, leaf 4");
    expect(html).toContain("bafybeigdyrzt");
    expect(html).toContain(`href="/explorer/tx/${"CD".repeat(32)}"`);
    expect(html).toContain("Compressed");
  });

  it("TestRecordCard_a_burned_asset_names_its_last_owner_and_no_collection_is_none", () => {
    const html = render(<RecordCard record={record({ state: "burned", collectionId: 0, metadataCid: "" })} title="Asset" />);
    expect(html).toContain("Last owner");
    expect(html).toContain("Burned");
    expect(html.match(/>None</g)?.length).toBeGreaterThanOrEqual(3);
  });

  it("TestAssetTable_links_each_asset_by_its_id", () => {
    const html = render(<AssetTable rows={[record(), record({ id: "cd".repeat(32), state: "decompressed", collectionId: 0 })]} />);
    expect(html).toContain(`href="/explorer/nft/${"ab".repeat(32)}"`);
    expect(html).toContain("Decompressed");
  });

  it("TestOwnedList_follows_the_indexer_cursor_and_says_when_an_owner_holds_nothing", () => {
    const full = render(<OwnedList paged={readyPaged([record()], "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8AAAAAAAAAAwAAAAw")} />);
    expect(full).toContain("Show more assets");
    expect(full).not.toContain("?page=");
    expect(render(<OwnedList paged={readyPaged([record()])} />)).not.toContain("Show more assets");
    expect(render(<OwnedList paged={readyPaged<NftRecord>([])} />)).toContain("lists no NFT held by this address");
  });

  it("TestTreeView_shows_capacity_progress_and_snapshots_or_none", () => {
    const tree = { id: 7, creator: addr(2), collectionId: 3, depth: 14, bufferSize: 64, canopyDepth: 0, sequence: "12", nextLeaf: "6" };
    const html = render(<TreeView tree={tree} snapshots={[{ id: 1, cid: "bafysnap", sequence: "9" }]} />);
    expect(html).toContain("16,384 leaves");
    expect(html).toContain("bafysnap");
    expect(render(<TreeView tree={tree} snapshots={[]} />)).toContain("None recorded");
  });
});
