import { describe, expect, it } from "vitest";
import { ALICE, BOB, decode, fieldsOf, said } from "./fixtures";

const CNFT = "/orama.cnft.v1.";
const MARKET = "/orama.market.v1.";
/** 32 bytes of 0xab, base64. */
const ASSET = btoa(String.fromCharCode(...new Array(32).fill(0xab)));
const LEAF = { asset_id: ASSET, owner: ALICE };

describe("x/cnft decoders", () => {
  it("TestCnft_collection_and_tree", () => {
    const c = decode(`${CNFT}MsgCreateCollection`, { creator: ALICE, name: "Art", royalty_bps: 500 });
    expect(said(c)).toBe("Alice created the NFT collection “Art” with a 5% royalty");
    expect(said(decode(`${CNFT}MsgCreateTree`, { creator: ALICE, collection_id: "4", depth: 14, buffer: 64, canopy: 8 }))).toBe(
      "Alice created an NFT tree of depth 14 for collection #4",
    );
  });

  it("TestCnft_mint_counts_the_leaves", () => {
    expect(said(decode(`${CNFT}MsgMint`, { creator: ALICE, tree_id: "2", leaves: [{}, {}] }))).toBe("Alice minted 2 compressed NFTs into tree #2");
    expect(said(decode(`${CNFT}MsgMint`, { creator: ALICE, tree_id: "2", leaves: [{}] }))).toBe("Alice minted 1 compressed NFT into tree #2");
  });

  it("TestCnft_asset_ids_are_hex_and_short_in_the_sentence", () => {
    const m = decode(`${CNFT}MsgTransfer`, { signer: ALICE, tree_id: "2", current: LEAF, new_owner: BOB });
    expect(said(m)).toBe("Alice transferred asset abababababababab… in tree #2 to Bob");
    expect(fieldsOf(m)["Asset ID"]).toBe("ab".repeat(32));
    expect(fieldsOf(m)["New owner"]).toBe(BOB);
  });

  it("TestCnft_burn_update_decompress_compress_snapshot", () => {
    expect(said(decode(`${CNFT}MsgBurn`, { signer: ALICE, tree_id: "2", current: LEAF }))).toBe("Alice burned asset abababababababab… in tree #2");
    const upd = decode(`${CNFT}MsgUpdateMetadata`, { signer: ALICE, tree_id: "2", current: LEAF, new_metadata_cid: "bafynew" });
    expect(said(upd)).toBe("Alice updated the metadata of asset abababababababab… in tree #2");
    expect(fieldsOf(upd)["New metadata CID"]).toBe("bafynew");
    expect(said(decode(`${CNFT}MsgDecompress`, { owner: ALICE, tree_id: "2", current: LEAF }))).toBe("Alice decompressed asset abababababababab… from tree #2");
    expect(said(decode(`${CNFT}MsgCompress`, { owner: ALICE, asset_id: ASSET }))).toBe("Alice compressed asset abababababababab…");
    expect(said(decode(`${CNFT}MsgRecordSnapshot`, { creator: ALICE, tree_id: "2", cid: "bafysnap" }))).toBe("Alice recorded a snapshot of tree #2 (CID bafysnap)");
  });

  it("TestCnft_an_asset_that_is_not_base64_is_unknown_not_invented", () => {
    expect(said(decode(`${CNFT}MsgBurn`, { signer: ALICE, tree_id: "2", current: { asset_id: "!!not base64!!" } }))).toBe("Alice burned asset (unknown) in tree #2");
  });
});

describe("x/market decoders", () => {
  it("TestMarket_list_bid_settle", () => {
    const list = decode(`${MARKET}MsgList`, { seller: ALICE, tree_id: "2", leaf: LEAF, price: "3000000000" });
    expect(said(list)).toBe("Alice listed asset abababababababab… from tree #2 for 3 ORAMA");
    expect(list.amount).toBeNull();
    expect(said(decode(`${MARKET}MsgBid`, { bidder: BOB, listing_id: "8", amount: "2000000000" }))).toBe("Bob bid 2 ORAMA on listing #8");
    expect(said(decode(`${MARKET}MsgBid`, { bidder: BOB, listing_id: "8", amount: "2000000000" }), true)).toBe("Bob tried to bid 2 ORAMA on listing #8");
    expect(said(decode(`${MARKET}MsgSettle`, { signer: ALICE, listing_id: "8", bid_id: "3", leaf: LEAF }))).toBe("Alice settled bid #3 on listing #8");
  });

  it("TestMarket_cancellations", () => {
    expect(said(decode(`${MARKET}MsgCancelListing`, { seller: ALICE, listing_id: "8" }))).toBe("Alice cancelled listing #8");
    expect(said(decode(`${MARKET}MsgCancelBid`, { bidder: BOB, listing_id: "8", bid_id: "3" }))).toBe("Bob cancelled bid #3 on listing #8");
  });
});
