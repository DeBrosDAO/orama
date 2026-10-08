import { describe, expect, it } from "vitest";
import { bech32Decode, bech32Encode, bech32Hrp, bech32Rehrp } from "./bech32";

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

describe("bech32Decode and bech32Rehrp", () => {
  it("TestBech32Decode_round_trips_the_bytes", () => {
    const bytes = Uint8Array.from({ length: 20 }, (_, i) => i * 13);
    const decoded = bech32Decode(bech32Encode("orama", bytes));
    expect(decoded?.hrp).toBe("orama");
    expect(Array.from(decoded?.bytes ?? [])).toEqual(Array.from(bytes));
  });

  it("TestBech32Decode_rejects_a_bad_checksum_and_junk", () => {
    const good = bech32Encode("orama", new Uint8Array(20).fill(5));
    expect(bech32Decode(good.slice(0, -1) + (good.endsWith("q") ? "p" : "q"))).toBeNull();
    expect(bech32Decode("notbech32")).toBeNull();
  });

  it("TestBech32Rehrp_moves_a_valoper_address_to_the_account_prefix", () => {
    const bytes = new Uint8Array(20).fill(9);
    const valoper = bech32Encode("oramavaloper", bytes);
    expect(bech32Rehrp(valoper, "orama")).toBe(bech32Encode("orama", bytes));
    expect(bech32Rehrp("junk", "orama")).toBeNull();
  });
});
