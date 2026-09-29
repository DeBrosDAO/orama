import { describe, expect, it } from "vitest";
import { bech32Encode } from "../../model/bech32";
import { walletParam } from "./wallet-param";

const ADDRESS = bech32Encode("orama", Uint8Array.from({ length: 20 }, (_, i) => i * 11));
const OPERATOR = bech32Encode("oramavaloper", new Uint8Array(20).fill(7));

describe("walletParam", () => {
  it("TestWalletParam_accepts_a_valid_address", () => {
    expect(walletParam(ADDRESS)).toBe(ADDRESS);
  });

  it("TestWalletParam_lower_cases_an_upper_case_address", () => {
    expect(walletParam(ADDRESS.toUpperCase())).toBe(ADDRESS);
  });

  it("TestWalletParam_rejects_empty_and_missing", () => {
    expect(walletParam("")).toBeNull();
    expect(walletParam(undefined)).toBeNull();
  });

  it("TestWalletParam_rejects_path_and_query_tricks", () => {
    for (const bad of ["..", "../tx", "?x=1", `${ADDRESS}?x=1`, `${ADDRESS}/..`, `%2e%2e`]) {
      expect(walletParam(bad)).toBeNull();
    }
  });

  it("TestWalletParam_rejects_surrounding_whitespace", () => {
    expect(walletParam(` ${ADDRESS} `)).toBeNull();
  });

  it("TestWalletParam_rejects_too_long", () => {
    expect(walletParam(`${ADDRESS}${"q".repeat(200)}`)).toBeNull();
  });

  it("TestWalletParam_rejects_a_valid_looking_address_with_a_bad_checksum", () => {
    const bad = ADDRESS.slice(0, 12) + (ADDRESS[12] === "q" ? "p" : "q") + ADDRESS.slice(13);
    expect(walletParam(bad)).toBeNull();
  });

  it("TestWalletParam_rejects_other_identifiers", () => {
    expect(walletParam(OPERATOR)).toBeNull();
    expect(walletParam("1284410")).toBeNull();
    expect(walletParam("AB".repeat(32))).toBeNull();
  });
});
