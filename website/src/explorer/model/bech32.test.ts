import { describe, expect, it } from "vitest";
import { bech32Encode, bech32Hrp } from "./bech32";

describe("bech32", () => {
  it("TestBech32_encode_then_check_round_trips", () => {
    const addr = bech32Encode("orama", Uint8Array.from({ length: 20 }, (_, i) => i * 11));
    expect(addr.startsWith("orama1")).toBe(true);
    expect(bech32Hrp(addr)).toBe("orama");
  });

  it("TestBech32_a_changed_character_fails_the_checksum", () => {
    const addr = bech32Encode("orama", new Uint8Array(20).fill(3));
    const bad = addr.slice(0, 12) + (addr[12] === "q" ? "p" : "q") + addr.slice(13);
    expect(bech32Hrp(bad)).toBeNull();
  });

  it("TestBech32_mixed_case_is_rejected", () => {
    const addr = bech32Encode("orama", new Uint8Array(20).fill(3));
    expect(bech32Hrp(addr.slice(0, 8) + addr.slice(8).toUpperCase())).toBeNull();
  });

  it("TestBech32_too_short_or_no_separator", () => {
    expect(bech32Hrp("orama")).toBeNull();
    expect(bech32Hrp("")).toBeNull();
    expect(bech32Hrp("oramaqqqqqqqqqq")).toBeNull();
  });
});
