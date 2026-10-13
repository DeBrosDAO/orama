import { describe, expect, it } from "vitest";
import { TX_HASH_LENGTH, normalizeTxHash } from "./tx-params";

const HASH = "a91f3c".padEnd(TX_HASH_LENGTH - 4, "0") + "03bc";

describe("normalizeTxHash", () => {
  it("TestNormalizeTxHash_upper_cases_a_valid_hash", () => {
    expect(normalizeTxHash(HASH)).toBe(HASH.toUpperCase());
  });

  it("TestNormalizeTxHash_keeps_an_upper_case_hash", () => {
    expect(normalizeTxHash(HASH.toUpperCase())).toBe(HASH.toUpperCase());
  });

  it("TestNormalizeTxHash_rejects_wrong_length", () => {
    expect(normalizeTxHash(HASH.slice(1))).toBeNull();
    expect(normalizeTxHash(`${HASH}0`)).toBeNull();
  });

  it("TestNormalizeTxHash_rejects_non_hex", () => {
    expect(normalizeTxHash(`${HASH.slice(1)}g`)).toBeNull();
    expect(normalizeTxHash(` ${HASH.slice(1)}`)).toBeNull();
  });

  it("TestNormalizeTxHash_rejects_missing_and_empty", () => {
    expect(normalizeTxHash(undefined)).toBeNull();
    expect(normalizeTxHash("")).toBeNull();
  });
});
