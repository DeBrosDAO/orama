import { describe, expect, it } from "vitest";
import type { BalanceChange } from "../../model/types";
import { balanceText, changeKey, changeText, changeTone, deltasSumToZero } from "./balance-format";

const wallet = (delta: string, address = "orama1a"): BalanceChange => ({
  party: { kind: "wallet", ref: { address } },
  before: "0",
  after: "0",
  delta,
});
const system = (name: Extract<BalanceChange["party"], { kind: "system" }>["name"], delta: string): BalanceChange => ({
  party: { kind: "system", name },
  before: null,
  after: null,
  delta,
});

describe("balanceText", () => {
  it("TestBalanceText_fixed_four_decimals", () => {
    expect(balanceText("1217000071200")).toBe("1,217.0000");
    expect(balanceText("88000000000")).toBe("88.0000");
  });

  it("TestBalanceText_system_pool_is_an_em_dash", () => {
    expect(balanceText(null)).toBe("—");
  });
});

describe("changeText", () => {
  it("TestChangeText_signs_gains_and_losses", () => {
    expect(changeText(wallet("12500000000"))).toBe("+12.5000");
    expect(changeText(wallet("-12500071200"))).toBe("−12.5000");
  });

  it("TestChangeText_zero_has_no_sign", () => {
    expect(changeText(wallet("0"))).toBe("0.0000");
  });

  it("TestChangeText_a_nonzero_change_never_reads_as_zero", () => {
    expect(changeText(wallet("71200"))).toBe("+<0.0001");
    expect(changeText(wallet("-1"))).toBe("−<0.0001");
    expect(changeText(wallet("100000"))).toBe("+0.0001");
  });

  it("TestChangeText_burned_is_unsigned_with_a_flame", () => {
    expect(changeText(system("burned", "71200000"))).toBe("0.0712 🔥");
  });

  it("TestChangeText_a_pool_that_gave_value_shows_a_minus", () => {
    expect(changeText(system("minted", "-5000000000"))).toBe("−5.0000");
    expect(changeText(system("unbonding", "5000000000"))).toBe("+5.0000");
  });

  it("TestChangeText_rejects_a_malformed_delta", () => {
    expect(() => changeText(wallet("12.5"))).toThrow();
  });
});

describe("changeTone", () => {
  it("TestChangeTone_gain_plain_and_signal", () => {
    expect(changeTone(wallet("5"))).toBe("gain");
    expect(changeTone(wallet("-5"))).toBe("plain");
    expect(changeTone(wallet("0"))).toBe("plain");
    expect(changeTone(system("burned", "5"))).toBe("signal");
  });

  it("TestChangeTone_system_pools_are_never_gain_green", () => {
    for (const name of ["minted", "staked", "unbonding", "storage_escrow"] as const) {
      expect(changeTone(system(name, "5"))).toBe("plain");
      expect(changeTone(system(name, "-5"))).toBe("plain");
    }
  });
});

describe("changeKey", () => {
  it("TestChangeKey_distinguishes_wallets_and_pools", () => {
    expect(changeKey(wallet("1", "orama1zzz"))).toBe("orama1zzz");
    expect(changeKey(system("burned", "1"))).toBe("system:burned");
  });
});

describe("deltasSumToZero", () => {
  it("TestDeltasSumToZero_a_balanced_claim", () => {
    expect(deltasSumToZero([wallet("5000"), system("minted", "-5000")])).toBe(true);
  });

  it("TestDeltasSumToZero_a_transfer_with_a_burned_fee", () => {
    expect(deltasSumToZero([wallet("-1071"), wallet("1000", "orama1b"), system("burned", "71")])).toBe(true);
  });

  it("TestDeltasSumToZero_false_when_value_appears_or_vanishes", () => {
    expect(deltasSumToZero([wallet("5000")])).toBe(false);
    expect(deltasSumToZero([wallet("-1"), system("burned", "2")])).toBe(false);
  });

  it("TestDeltasSumToZero_no_rows_sum_to_zero", () => {
    expect(deltasSumToZero([])).toBe(true);
  });

  it("TestDeltasSumToZero_rejects_a_malformed_delta", () => {
    expect(() => deltasSumToZero([wallet("1.5")])).toThrow();
  });
});
