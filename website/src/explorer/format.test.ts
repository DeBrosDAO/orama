import { describe, expect, it } from "vitest";
import { formatNorama, sharePct, showAttr, txHashFromBase64 } from "./format";

describe("norama amounts", () => {
  it("TestFormatNorama_splits_at_1e9", () => {
    expect(formatNorama("0")).toBe("0");
    expect(formatNorama("1")).toBe("0.000000001");
    expect(formatNorama("1000000000")).toBe("1");
    expect(formatNorama("1500000000")).toBe("1.5");
    expect(formatNorama("1000000000000")).toBe("1,000");
    expect(formatNorama("21000640000000000")).toBe("21,000,640");
  });

  it("TestFormatNorama_rejects_non_integers", () => {
    expect(() => formatNorama("01")).toThrow();
    expect(() => formatNorama("1.2")).toThrow();
    expect(() => formatNorama("-1")).toThrow();
  });
});

describe("shares", () => {
  it("TestSharePct_truncates", () => {
    expect(sharePct(1n, 2n)).toBe("50.0%");
    expect(sharePct(1n, 3n)).toBe("33.3%");
    expect(sharePct(0n, 10n)).toBe("0.0%");
  });
});

describe("attribute text", () => {
  it("TestShowAttr_decodes_base64_text_only", () => {
    expect(showAttr("c2VuZGVy")).toBe("sender");
    expect(showAttr("transfer")).toBe("transfer");
    expect(showAttr("1000norama")).toBe("1000norama");
    expect(showAttr("ABCDEF0123456789ABCDEF0123456789ABCDEF01")).toBe(
      "ABCDEF0123456789ABCDEF0123456789ABCDEF01",
    );
  });
});

describe("transaction hash", () => {
  it("TestTxHash_sha256_of_raw_bytes", async () => {
    expect(await txHashFromBase64("aGVsbG8=")).toBe(
      "2CF24DBA5FB0A30E26E83B2AC5B9E29E1B161E5C1FA7425E73043362938B9824",
    );
  });
});
