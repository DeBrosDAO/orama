import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative, resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { APP_LINKS, CONTACT_EMAIL, GITHUB_URL } from "./site";

/**
 * The site's two content rules, enforced over every source file of the public
 * pages (the unlisted docs are exempt: they must show real package names):
 *  1. Orama is its own project: no mention of any former parent brand.
 *  2. Only what the code supports: no token, sale or reward language.
 *  3. One public contact address.
 */

const SRC = resolve(__dirname, "..");
const WHITEPAPER = resolve(__dirname, "../../../docs/whitepaper/WHITEPAPER.md");
const EXEMPT_DIRS = new Set(["docs"]);
const TEXT_FILES = /\.(tsx?|css|md|mdx|html)$/;

function sourceFiles(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const full = join(dir, name);
    if (statSync(full).isDirectory()) {
      return EXEMPT_DIRS.has(name) && dir === SRC ? [] : sourceFiles(full);
    }
    return TEXT_FILES.test(name) && !/\.test\.tsx?$/.test(name) ? [full] : [];
  });
}

const FILES = [...sourceFiles(SRC), resolve(SRC, "../index.html"), WHITEPAPER];

function offending(pattern: RegExp, allow: string[] = []): string[] {
  return FILES.flatMap((f) => {
    let text = readFileSync(f, "utf-8");
    for (const a of allow) text = text.split(a).join("");
    return pattern.test(text) ? [relative(SRC, f)] : [];
  });
}

describe("brand and claims", () => {
  it("TestSite_scans_the_pages", () => {
    expect(FILES.some((f) => f.endsWith("pages/home.tsx"))).toBe(true);
    expect(FILES.some((f) => f.includes("/docs/developer/"))).toBe(false);
  });

  it("TestSite_no_former_brand", () => {
    // Allowed only inside two URLs that can't change from here: the GitHub
    // organisation in the repository URL and AnChat's Play Store package id.
    expect(offending(/debros/i, [GITHUB_URL, APP_LINKS.anchatAndroid])).toEqual([]);
  });

  it("TestSite_no_token_or_sale_language", () => {
    // "staking" is allowed: RootWallet earns a commission when users stake.
    expect(offending(/\$ORAMA|presale|pre-sale|token sale|airdrop|node licen[cs]e|passive income|earn while/i)).toEqual([]);
  });

  it("TestSite_no_old_partners", () => {
    expect(offending(/icxcnika|nonos|dgrs/i)).toEqual([]);
  });

  it("TestSite_only_the_contact_email", () => {
    expect(CONTACT_EMAIL).toBe("info@orama.network");
    expect(offending(/[\w.+-]+@orama\.network/i, [CONTACT_EMAIL])).toEqual([]);
  });
});
