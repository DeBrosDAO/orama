import { describe, expect, it } from "vitest";
import { bech32Hrp, classify } from "./search";

const ACCOUNT = "orama1qypqxpq9qcrsszg2pvxq6rs0zqg3yyc5hfazke";
const OPERATOR = "oramavaloper1qypqxpq9qcrsszg2pvxq6rs0zqg3yyc5ug8lxe";

describe("bech32", () => {
  it("TestBech32_bip173_vector", () => {
    expect(bech32Hrp("bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4")).toBe("bc");
    expect(bech32Hrp("bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t5")).toBeNull();
  });

  it("TestBech32_orama_prefixes", () => {
    expect(bech32Hrp(ACCOUNT)).toBe("orama");
    expect(bech32Hrp(OPERATOR)).toBe("oramavaloper");
    expect(bech32Hrp(ACCOUNT.slice(0, -1) + "q")).toBeNull();
  });
});

describe("classify", () => {
  it("TestClassify_live_shapes_only", () => {
    expect(classify("18420")).toEqual({ kind: "block", height: 18420 });
    expect(classify("ab".repeat(32))?.kind).toBe("tx");
    expect(classify(ACCOUNT)).toEqual({ kind: "account", address: ACCOUNT });
    expect(classify(OPERATOR)?.kind).toBe("validator");
    expect(classify("ab".repeat(20))?.kind).toBe("validator");
    expect(classify("u1qqqqqq")?.kind).toBe("shielded-address");
  });

  it("TestClassify_ignores_demo_names", () => {
    expect(classify("alice")).toBeNull();
    expect(classify("athena")).toBeNull();
    expect(classify("bob")).toBeNull();
    expect(classify("orama1alice7k3m9qx2p4n8rwd6vht0csylua")).toBeNull();
    expect(classify("oramavaloper1athena0k3m9qx2p4n8rwd6vh")).toBeNull();
    expect(classify("u1demo7k3m9qxshieldedaddress0001")).toBeNull();
    expect(classify("")).toBeNull();
  });
});
