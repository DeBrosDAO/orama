import { describe, expect, it } from "vitest";
import { addressFor } from "../data/demo/ids";
import { bech32Encode } from "./bech32";
import { classify } from "./search";

const HASH = "a91f3c0000000000000000000000000000000000000000000000000000bc03bc";

describe("classify", () => {
  it("TestClassify_block_height", () => {
    expect(classify("1284410")).toEqual({ kind: "block", height: 1284410 });
    expect(classify("  42 ")).toEqual({ kind: "block", height: 42 });
  });

  it("TestClassify_rejects_zero_and_leading_zero_heights", () => {
    expect(classify("0")).toBeNull();
    expect(classify("007")).toBeNull();
  });

  it("TestClassify_tx_hash_with_or_without_0x", () => {
    const upper = HASH.toUpperCase();
    expect(classify(HASH)).toEqual({ kind: "tx", hash: upper });
    expect(classify(`0x${HASH}`)).toEqual({ kind: "tx", hash: upper });
  });

  it("TestClassify_wallet_needs_a_valid_checksum", () => {
    const good = addressFor("someone");
    expect(classify(good)).toEqual({ kind: "wallet", address: good });
    const typo = good.slice(0, -1) + (good.endsWith("q") ? "p" : "q");
    expect(classify(typo)).toBeNull();
  });

  it("TestClassify_wallet_accepts_upper_case_and_lowers_it", () => {
    const good = addressFor("someone");
    expect(classify(good.toUpperCase())).toEqual({ kind: "wallet", address: good });
  });

  it("TestClassify_foreign_bech32_is_not_a_wallet", () => {
    const other = bech32Encode("cosmos", new Uint8Array(20).fill(7));
    expect(classify(other)).toBeNull();
  });

  it("TestClassify_validator_operator_address_is_not_a_wallet", () => {
    const valoper = bech32Encode("oramavaloper", new Uint8Array(20).fill(7));
    expect(classify(valoper)).toBeNull();
  });

  it("TestClassify_shielded_address_is_recognised_not_looked_up", () => {
    expect(classify("u1qpzry9x8gf2tvdw0s3jn54khce6mua7l")).toEqual({ kind: "shielded-address" });
  });

  it("TestClassify_empty_and_words_are_null", () => {
    expect(classify("")).toBeNull();
    expect(classify("   ")).toBeNull();
    expect(classify("val-3")).toBeNull();
  });
});
