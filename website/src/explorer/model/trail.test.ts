import { describe, expect, it } from "vitest";
import { MAX_TRAIL, crumbLabel, isTrailPath, nextTrail, readSharedTrail, shareUrl } from "./trail";
import { explorerPaths } from "./routes";

describe("nextTrail", () => {
  it("TestNextTrail_appends_new_pages", () => {
    expect(nextTrail([], "/explorer/tx/AB")).toEqual(["/explorer/tx/AB"]);
    expect(nextTrail(["/explorer/tx/AB"], "/explorer/wallet/x")).toEqual(["/explorer/tx/AB", "/explorer/wallet/x"]);
  });

  it("TestNextTrail_revisiting_goes_back_and_drops_the_rest", () => {
    const t = ["/explorer/tx/AB", "/explorer/wallet/x", "/explorer/wallet/y"];
    expect(nextTrail(t, "/explorer/wallet/x")).toEqual(["/explorer/tx/AB", "/explorer/wallet/x"]);
  });

  it("TestNextTrail_home_clears", () => {
    expect(nextTrail(["/explorer/tx/AB"], explorerPaths.home)).toEqual([]);
  });

  it("TestNextTrail_is_capped_keeping_the_newest", () => {
    let t: string[] = [];
    for (let i = 0; i < MAX_TRAIL + 5; i++) t = nextTrail(t, `/explorer/block/${i + 1}`);
    expect(t).toHaveLength(MAX_TRAIL);
    expect(t.at(-1)).toBe(`/explorer/block/${MAX_TRAIL + 5}`);
  });

  it("TestNextTrail_does_not_mutate_its_input", () => {
    const t = ["/explorer/tx/AB"];
    nextTrail(t, "/explorer/wallet/x");
    expect(t).toEqual(["/explorer/tx/AB"]);
  });
});

describe("crumbLabel", () => {
  it("TestCrumbLabel_names_each_page_kind", () => {
    expect(crumbLabel("/explorer/tx/A91F3C0000000000000000000000000000000000000000000000000000BC03BC")).toBe("Tx A91F3C…03BC");
    expect(crumbLabel("/explorer/wallet/orama1q8w9k3v5r2m4x7h6d0n8c1p3t5y9j2u4l6e7x2")).toBe("Wallet orama1q8w9…e7x2");
    expect(crumbLabel("/explorer/block/1284410")).toBe("Block 1284410");
    expect(crumbLabel("/explorer/validators")).toBe("Validators");
  });
});

describe("shared trails", () => {
  it("TestShareUrl_round_trips_through_readSharedTrail", () => {
    const trail = ["/explorer/tx/AB12", "/explorer/wallet/orama1abc"];
    const url = new URL(shareUrl("https://orama.network", "/explorer/wallet/orama1abc", trail));
    expect(url.pathname).toBe("/explorer/wallet/orama1abc");
    expect(readSharedTrail(url.search)).toEqual(trail);
  });

  it("TestShareUrl_without_a_trail_is_just_the_page", () => {
    expect(shareUrl("https://orama.network", "/explorer", [])).toBe("https://orama.network/explorer");
  });

  it("TestReadSharedTrail_drops_anything_that_is_not_an_explorer_page", () => {
    const hostile = "?trail=" + encodeURIComponent(["/explorer/tx/AB", "https://evil.example/x", "/explorer/../admin", "javascript:alert(1)"].join("|"));
    expect(readSharedTrail(hostile)).toEqual(["/explorer/tx/AB"]);
  });

  it("TestReadSharedTrail_keeps_the_first_of_a_repeated_path", () => {
    const search = "?trail=" + encodeURIComponent(["/explorer/tx/AB", "/explorer/wallet/x", "/explorer/tx/AB", "/explorer/wallet/x"].join("|"));
    expect(readSharedTrail(search)).toEqual(["/explorer/tx/AB", "/explorer/wallet/x"]);
  });

  it("TestReadSharedTrail_is_capped_after_dropping_duplicates", () => {
    const paths = Array.from({ length: MAX_TRAIL + 4 }, (_, i) => `/explorer/block/${i + 1}`);
    const search = "?trail=" + encodeURIComponent([paths[0], ...paths].join("|"));
    expect(readSharedTrail(search)).toEqual(paths.slice(0, MAX_TRAIL));
  });

  it("TestReadSharedTrail_missing_or_empty", () => {
    expect(readSharedTrail("")).toEqual([]);
    expect(readSharedTrail("?trail=")).toEqual([]);
  });

  it("TestIsTrailPath_only_known_shapes", () => {
    expect(isTrailPath("/explorer/validators")).toBe(true);
    expect(isTrailPath("/explorer/tx/AB12")).toBe(true);
    expect(isTrailPath("/explorer")).toBe(false);
    expect(isTrailPath("/explorer/tx/AB/../x")).toBe(false);
  });
});

describe("route paths", () => {
  it("TestExplorerPaths_encode_identifiers_so_data_cannot_climb_out_of_its_segment", () => {
    expect(explorerPaths.wallet("x/../../foo")).toBe("/explorer/wallet/x%2F..%2F..%2Ffoo");
    expect(explorerPaths.tx("a?trail=b")).toBe("/explorer/tx/a%3Ftrail%3Db");
    expect(explorerPaths.wallet("orama1abc")).toBe("/explorer/wallet/orama1abc");
  });
});
