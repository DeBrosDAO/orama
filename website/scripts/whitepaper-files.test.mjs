import { describe, expect, it } from "vitest";
import { Buffer } from "node:buffer";
import { buildManifest, formatBytes, isPdf, parsePageCount, whitepaperPlan } from "./whitepaper-files.mjs";

describe("whitepaperPlan", () => {
  it("TestWhitepaperPlan_names_carry_the_version", () => {
    const plan = whitepaperPlan("0.3.0");
    expect(plan.map((e) => e.key)).toEqual(["short", "vol1", "vol2", "appendices"]);
    expect(plan[0].source).toBe("docs/whitepaper/orama-whitepaper/dist/orama-whitepaper-v0.3.0.pdf");
    expect(plan[0].stable).toBe("orama-whitepaper.pdf");
    expect(plan[1].source).toBe("docs/whitepaper/technical-reference/dist/orama-whitepaper-technical-reference-v0.3.0-vol1.pdf");
    expect(plan[1].versioned).toBe("orama-whitepaper-technical-reference-v0.3.0-vol1.pdf");
    expect(plan[1].stable).toBe("orama-whitepaper-technical-reference-vol1.pdf");
    expect(new Set(plan.flatMap((e) => [e.stable, e.versioned])).size).toBe(8);
  });

  it.each(["", "1.2", "v1.2.3", "1.2.3-rc1", "../1.2.3"])("TestWhitepaperPlan_rejects_version_%j", (version) => {
    expect(() => whitepaperPlan(version)).toThrow(/VERSION/);
  });
});

describe("formatBytes", () => {
  it("TestFormatBytes_units", () => {
    expect(formatBytes(1_678_807)).toBe("1.7 MB");
    expect(formatBytes(17_849_047)).toBe("17.8 MB");
    expect(formatBytes(812_400)).toBe("812 KB");
    expect(formatBytes(0)).toBe("1 KB");
  });

  it("TestFormatBytes_rejects_nonsense", () => {
    expect(() => formatBytes(-1)).toThrow();
    expect(() => formatBytes(1.5)).toThrow();
    expect(() => formatBytes(Number.NaN)).toThrow();
  });
});

describe("parsePageCount", () => {
  it("TestParsePageCount_reads_pdfinfo", () => {
    expect(parsePageCount("Title: x\nPages:           98\nEncrypted: no\n")).toBe(98);
  });

  it("TestParsePageCount_missing_or_zero", () => {
    expect(() => parsePageCount("")).toThrow(/page count/);
    expect(() => parsePageCount("Pages: 0\n")).toThrow(/page count/);
  });
});

describe("isPdf", () => {
  it("TestIsPdf_magic_bytes", () => {
    expect(isPdf(Buffer.from("%PDF-1.7\n"))).toBe(true);
    expect(isPdf(Buffer.from("<html>"))).toBe(false);
    expect(isPdf(Buffer.alloc(0))).toBe(false);
  });
});

describe("buildManifest", () => {
  const plan = whitepaperPlan("0.3.0");
  const measured = {
    short: { bytes: 1_678_807, pages: 98 },
    vol1: { bytes: 17_849_047, pages: 826 },
    vol2: { bytes: 4_191_702, pages: 198 },
    appendices: { bytes: 6_379_734, pages: 212 },
  };

  it("TestBuildManifest_links_sizes_and_totals", () => {
    const m = buildManifest("0.3.0", plan, measured);
    expect(m.version).toBe("0.3.0");
    expect(m.files[0]).toMatchObject({ href: "/whitepaper/orama-whitepaper-v0.3.0.pdf", stableHref: "/whitepaper/orama-whitepaper.pdf", size: "1.7 MB", pages: 98 });
    expect(m.referenceTotal).toEqual({ bytes: 28_420_483, size: "28.4 MB", pages: 1236 });
  });

  it("TestBuildManifest_missing_measurement", () => {
    expect(() => buildManifest("0.3.0", plan, { short: measured.short })).toThrow(/vol1/);
  });
});
