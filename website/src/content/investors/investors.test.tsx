import { readFileSync } from "node:fs";
import { renderToString } from "react-dom/server";
import { MemoryRouter } from "react-router";
import { describe, expect, it } from "vitest";
import Investors from "../../pages/investors";
import { SOURCES, sourceNumber } from "./sources";
import { CLOUD_COMPETITION, WALLET_COMPETITION } from "./competition";
import { WALLET_SCENARIOS, walletRevenuePerUser } from "./model";
import { ORAMA_INPUTS, formatApproxEur, oramaArrEur, payingTeams, walletArrEur } from "./projections";
import { FAQ } from "./case";
import { INVESTOR_PDF } from "../site";
import { ROUTES } from "../routes";
import { SHADE_COUNT } from "../../components/visuals/funding-donut";
import { ALLOCATIONS, FUNDING_MONTHS, FUNDING_TOTAL_EUR, PAID_BETA_MONTH, PROOF_POINTS, TIMELINE } from "../funding";

const html = renderToString(
  <MemoryRouter initialEntries={["/investors"]}>
    <Investors />
  </MemoryRouter>,
);

describe("investor sources", () => {
  it("TestSources_unique_ids_and_https", () => {
    expect(new Set(SOURCES.map((s) => s.id)).size).toBe(SOURCES.length);
    for (const s of SOURCES) expect(s.url.startsWith("https://"), s.id).toBe(true);
  });

  it("TestSources_every_source_cited_on_the_rendered_page", () => {
    const cited = new Set([...html.matchAll(/href="#src-(\d+)"/g)].map((m) => Number(m[1])));
    const all = SOURCES.map((_, i) => i + 1);
    expect([...cited].sort((a, b) => a - b)).toEqual(all);
  });

  it("TestSources_every_citation_has_a_listed_target", () => {
    for (const m of html.matchAll(/href="#src-(\d+)"/g)) expect(html).toContain(`id="src-${m[1]}"`);
  });

  it("TestSourceNumber_unknown_id_throws", () => {
    expect(() => sourceNumber("nope")).toThrow(/unknown source/);
  });
});

describe("round", () => {
  it("TestRound_allocations_pinned", () => {
    expect(Object.fromEntries(ALLOCATIONS.map((a) => [a.id, a.amountEur]))).toEqual({
      engineering: 700_000,
      "orama-one": 200_000,
      audits: 175_000,
      oramaos: 150_000,
      ecosystem: 125_000,
      launch: 100_000,
      legal: 50_000,
    });
    expect(FUNDING_TOTAL_EUR).toBe(1_500_000);
    expect(FUNDING_MONTHS).toBe(18);
  });

  it("TestTimeline_strictly_increasing_and_contains_paid_beta", () => {
    const months = TIMELINE.map((t) => t.month);
    for (let i = 1; i < months.length; i++) expect(months[i]).toBeGreaterThan(months[i - 1]);
    expect(TIMELINE.find((t) => t.month === PAID_BETA_MONTH)?.title).toBe("Paid beta");
  });

  it("TestProofPoints_both_products", () => {
    expect(PROOF_POINTS.map((p) => p.product)).toEqual(["Orama", "RootWallet"]);
  });

  it("TestPage_states_runway_once_consistently", () => {
    expect(html).toContain(`${FUNDING_MONTHS} months, about €83k a month`);
    expect(html).not.toMatch(/24 months/);
  });
});

describe("projections", () => {
  it("TestWalletScenarios_match_published_per_user_values", () => {
    expect(walletRevenuePerUser(WALLET_SCENARIOS.conservative)).toBeCloseTo(1.04, 2);
    expect(walletRevenuePerUser(WALLET_SCENARIOS.base)).toBeCloseTo(5.2, 2);
    expect(walletRevenuePerUser(WALLET_SCENARIOS.upside)).toBeCloseTo(14.16, 2);
  });

  it("TestPayingTeams_zero_until_paid_beta_then_full_at_horizon", () => {
    expect(payingTeams(ORAMA_INPUTS.base, PAID_BETA_MONTH)).toBe(0);
    expect(payingTeams(ORAMA_INPUTS.base, 36)).toBe(1_500);
    expect(payingTeams(ORAMA_INPUTS.base, 18)).toBe(289);
  });

  it("TestOramaArr_base_by_hand", () => {
    // 1,500 teams × €45 × 12 × 30% + 1,500 × €9 × 12 + 6 × €20k
    expect(oramaArrEur(ORAMA_INPUTS.base, 36)).toBe(243_000 + 162_000 + 120_000);
    // 289 teams at month 18, one support contract
    expect(oramaArrEur(ORAMA_INPUTS.base, 18)).toBeCloseTo(289 * 45 * 12 * 0.3 + 289 * 9 * 12 + 20_000, 6);
  });

  it("TestWalletArr_converted_to_euros", () => {
    expect(walletArrEur("base")).toBeCloseTo((walletRevenuePerUser(WALLET_SCENARIOS.base) * 25_000) / 1.137, 6);
  });

  it("TestFormatApproxEur", () => {
    expect(formatApproxEur(525_000)).toBe("~€525k");
    expect(formatApproxEur(1_920_000)).toBe("~€1.9M");
  });
});

describe("content", () => {
  it("TestCompetitionGrids_are_rectangular", () => {
    for (const g of [CLOUD_COMPETITION, WALLET_COMPETITION]) {
      for (const row of g.rows) expect(row.cells.length, row.label).toBe(g.columns.length);
    }
  });

  it("TestPage_has_no_overclaims", () => {
    expect(html).not.toMatch(/operators earn 70%|every orama (command-line )?login|passive income|earn while/i);
    expect(html).not.toMatch(/Premium[^.]{0,40}€\d/);
    expect(FAQ.map((f) => f.a).join(" ")).toMatch(/no token/i);
  });

  it("TestPage_links_to_every_product", () => {
    for (const href of [
      "https://rootwallet.io",
      "https://anchat.io",
      "https://testflight.apple.com/join/GzQ2gvx4",
      "https://play.google.com/store/apps/details?id=debros.anchat_lite",
      "https://g.anchat.io/orama",
      "https://github.com/DeBrosDAO/orama",
      'href="/whitepaper"',
    ]) {
      expect(html, href).toContain(href);
    }
    for (const m of html.matchAll(/<a [^>]*href="https:[^"]*"[^>]*>/g)) {
      expect(m[0], m[0]).toContain('rel="noopener noreferrer"');
    }
  });

  it("TestPage_has_disclaimer_twice", () => {
    expect(html.match(/not an offer or solicitation/g)?.length).toBe(2);
  });
});

describe("pdf", () => {
  const css = readFileSync(new URL("../../index.css", import.meta.url), "utf8");

  it("TestInvestorPdf_printed_from_the_investor_page_to_a_root_pdf", () => {
    expect(INVESTOR_PDF.page).toBe(ROUTES.investors.path);
    expect(INVESTOR_PDF.path).toMatch(/^\/[a-z0-9-]+\.pdf$/);
  });

  it("TestPage_offers_the_pdf_as_a_download_twice", () => {
    const links = [...html.matchAll(/<a [^>]*href="([^"]*\.pdf)"[^>]*>/g)];
    expect(links.map((m) => m[1])).toEqual([INVESTOR_PDF.path, INVESTOR_PDF.path]);
    for (const m of links) {
      expect(m[0]).toContain("download");
      expect(m[0], "the printout hides its own download button").toContain("no-print");
    }
  });

  it("TestFundingShades_one_per_allocation_for_screen_and_print", () => {
    expect(ALLOCATIONS.length).toBeLessThanOrEqual(SHADE_COUNT);
    for (let i = 0; i < SHADE_COUNT; i++) {
      const defs = css.match(new RegExp(`--funding-shade-${i}:`, "g")) ?? [];
      expect(defs.length, `--funding-shade-${i}`).toBe(2);
    }
  });

  it("TestPrintStylesheet_reveals_scroll_in_content", () => {
    const print = css.slice(css.indexOf("@media print"));
    expect(print).toMatch(/\.animate-in\[data-animate="out"\]\s*\{\s*opacity: 1;/);
  });
});
