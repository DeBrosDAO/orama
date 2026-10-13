import { describe, expect, it } from "vitest";
import { addr } from "../data/chain/fake-gateway";
import { assetPathsFor } from "./assets-routes";
import { durationText, untilText } from "./governance";
import { contractTarget, listingTarget, nftTarget, parseAccount, parseDenom, parseId, proposalTarget, tokenTarget } from "./lookup";
import { moduleTxs } from "./module-activity";
import { explorerPathsFor, isPageKind, parseExplorerPath } from "./routes";
import { heldPowers, splitFactoryDenom } from "./tokens";
import type { TxMessage, TxSummary } from "./types";
import { crumbLabel, isTrailPath } from "./trail";

const paths = assetPathsFor("/explorer", "");
const CREATOR = addr(3);

describe("asset page URLs", () => {
  it("TestAssetPaths_build_each_page_under_the_network_and_carry_the_query", () => {
    const p = explorerPathsFor("testnet", "?gateway=g").assets;
    expect([p.tokens, p.nfts, p.contracts, p.market, p.governance, p.shielded]).toEqual([
      "/explorer/testnet/tokens?gateway=g",
      "/explorer/testnet/nfts?gateway=g",
      "/explorer/testnet/contracts?gateway=g",
      "/explorer/testnet/market?gateway=g",
      "/explorer/testnet/governance?gateway=g",
      "/explorer/testnet/shielded?gateway=g",
    ]);
    expect([p.collection(3), p.code(4), p.listing(5), p.proposal(6)]).toEqual([
      "/explorer/testnet/collection/3?gateway=g",
      "/explorer/testnet/code/4?gateway=g",
      "/explorer/testnet/listing/5?gateway=g",
      "/explorer/testnet/proposal/6?gateway=g",
    ]);
  });

  it("TestAssetPaths_a_token_is_its_creator_and_name_and_a_stranger_denom_falls_back_to_the_list", () => {
    expect(paths.token(`factory/${CREATOR}/cash`)).toBe(`/explorer/token/${CREATOR}/cash`);
    expect(paths.token("norama")).toBe("/explorer/tokens");
  });

  it("TestAssetPaths_encode_identifiers", () => {
    expect(paths.contract("x/../y")).toBe("/explorer/contract/x%2F..%2Fy");
    expect(paths.nftsOf("a b")).toBe("/explorer/nfts/a%20b");
  });

  it("TestParseExplorerPath_knows_the_asset_pages_so_no_network_can_take_their_names", () => {
    expect(parseExplorerPath("/explorer/tokens")).toEqual({ network: null, kind: "tokens", id: "" });
    expect(parseExplorerPath("/explorer/testnet/proposal/4")).toEqual({ network: "testnet", kind: "proposal", id: "4" });
    expect(parseExplorerPath(`/explorer/token/${CREATOR}/cash`)).toMatchObject({ kind: "token", id: CREATOR });
    for (const kind of ["tokens", "nfts", "market", "governance", "shielded", "contracts"]) expect(isPageKind(kind)).toBe(true);
    expect(isPageKind("testnet")).toBe(false);
  });

  it("TestTrail_crumbs_and_shared_trails_cover_the_asset_pages", () => {
    expect(crumbLabel("/explorer/tokens")).toBe("Tokens");
    expect(crumbLabel("/explorer/proposal/4")).toBe("Proposal 4");
    expect(crumbLabel("/explorer/shielded")).toBe("Shielded pool");
    for (const path of ["/explorer/tokens", "/explorer/testnet/governance", "/explorer/proposal/4", `/explorer/token/${CREATOR}/cash`, `/explorer/contract/${CREATOR}`]) {
      expect(isTrailPath(path)).toBe(true);
    }
    expect(isTrailPath("/explorer/token/a/b/c")).toBe(false);
    expect(isTrailPath("/explorer/proposal/4/../x")).toBe(false);
  });
});

describe("lookups", () => {
  it("TestParseId_takes_whole_numbers_from_one_only", () => {
    expect(parseId("12")).toBe(12);
    for (const text of ["0", "-1", "01", "1.5", "abc", "", "99999999999999999999"]) expect(parseId(text)).toBeNull();
  });

  it("TestParseAccount_needs_a_valid_checksum_and_lowers_the_case", () => {
    expect(parseAccount(CREATOR.toUpperCase())).toBe(CREATOR);
    expect(parseAccount(`${CREATOR.slice(0, -1)}q`)).toBeNull();
    expect(parseAccount("oramavaloper1abc")).toBeNull();
  });

  it("TestParseDenom_takes_the_full_denom_or_creator_slash_name", () => {
    expect(parseDenom(`factory/${CREATOR}/cash`)).toBe(`factory/${CREATOR}/cash`);
    expect(parseDenom(`${CREATOR}/cash`)).toBe(`factory/${CREATOR}/cash`);
    for (const text of ["norama", `factory/${CREATOR}`, `factory/${CREATOR}/Cash`, `factory/${CREATOR}/a`, "factory/orama1nope/cash", `factory/${CREATOR}/cash/x`]) {
      expect(parseDenom(text)).toBeNull();
    }
  });

  it("TestTargets_open_the_page_the_text_names", () => {
    const asset = "ab".repeat(32);
    expect(tokenTarget(`${CREATOR}/cash`, paths)).toBe(`/explorer/token/${CREATOR}/cash`);
    expect(nftTarget(asset.toUpperCase(), paths)).toBe(`/explorer/nft/${asset}`);
    expect(nftTarget(CREATOR, paths)).toBe(`/explorer/nfts/${CREATOR}`);
    expect(nftTarget("3", paths)).toBe("/explorer/collection/3");
    expect(contractTarget(CREATOR, paths)).toBe(`/explorer/contract/${CREATOR}`);
    expect(contractTarget("4", paths)).toBe("/explorer/code/4");
    expect(listingTarget("5", paths)).toBe("/explorer/listing/5");
    expect(proposalTarget("6", paths)).toBe("/explorer/proposal/6");
    for (const target of [tokenTarget, nftTarget, contractTarget, listingTarget, proposalTarget]) expect(target("nonsense", paths)).toBeNull();
  });
});

describe("token helpers", () => {
  it("TestSplitFactoryDenom_requires_exactly_three_segments_and_a_lowercase_name", () => {
    expect(splitFactoryDenom("factory/orama1x/cash")).toEqual({ creator: "orama1x", subdenom: "cash" });
    expect(splitFactoryDenom("factory//cash")).toBeNull();
    expect(splitFactoryDenom("factory/orama1x/CASH")).toBeNull();
    expect(splitFactoryDenom("ibc/ABC")).toBeNull();
  });

  it("TestHeldPowers_lists_only_what_the_token_still_holds", () => {
    const none = { mint: false, freeze: false, permanentDelegate: null, transferFeeBps: 0, nonTransferable: false, pause: false, transferHook: null };
    expect(heldPowers(none)).toEqual([]);
    expect(heldPowers({ ...none, mint: true, transferFeeBps: 250 })).toEqual(["The creator can mint more", "2.5% of every transfer is burned"]);
  });
});

describe("durationText", () => {
  it.each([
    [1_209_600, "14 days"],
    [86_400, "1 day"],
    [90_000, "1 d 1 h"],
    [5_400, "1 h 30 m"],
    [3_600, "1 h"],
    [300, "5 m"],
    [75, "1 m 15 s"],
    [45, "45 s"],
    [0, "0 s"],
  ])("TestDurationText_%i_is_%s", (seconds, text) => {
    expect(durationText(seconds)).toBe(text);
  });
});

describe("untilText", () => {
  const now = Date.parse("2026-10-10T00:00:00Z");
  it.each([
    ["2026-10-14T03:00:30Z", "in 4 d 3 h"],
    ["2026-10-13T00:00:00Z", "in 3 d"],
    ["2026-10-10T02:05:00Z", "in 2 h 5 m"],
    ["2026-10-10T02:00:00Z", "in 2 h"],
    ["2026-10-10T00:12:30Z", "in 12 m"],
    ["2026-10-10T00:00:30Z", "in under a minute"],
    ["2026-10-10T00:00:00Z", "now"],
    ["2026-10-01T00:00:00Z", "now"],
    ["not a time", "now"],
  ])("TestUntilText_%s_is_%s", (iso, text) => {
    expect(untilText(iso, now)).toBe(text);
  });
});

describe("moduleTxs", () => {
  const action = (module: string): TxMessage => ({ type: "action", typeUrl: "/x", module, tag: "t", category: "other", actor: null, done: [], tried: [], amount: null, fields: [] });
  const tx = (hash: string, ...modules: string[]): TxSummary => ({
    hash,
    height: 1,
    time: "2026-10-08T00:00:00Z",
    status: { ok: true },
    signer: null,
    messages: modules.map(action),
    fee: { burned: "0", tip: "0", gasUsed: 0, gasWanted: 0 },
  });

  it("TestModuleTxs_keeps_the_transactions_with_a_message_of_the_module_in_order", () => {
    const txs = [tx("a", "Market"), tx("b", "Tokens"), tx("c", "Tokens", "Market"), tx("d")];
    expect(moduleTxs(txs, ["Market"]).map((t) => t.hash)).toEqual(["a", "c"]);
    expect(moduleTxs(txs, ["Tokens", "Market"]).map((t) => t.hash)).toEqual(["a", "b", "c"]);
    expect(moduleTxs(txs, ["Houses"])).toEqual([]);
    expect(moduleTxs([], ["Market"])).toEqual([]);
  });
});
