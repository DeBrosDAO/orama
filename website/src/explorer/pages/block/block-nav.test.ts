import { describe, expect, it } from "vitest";
import { blockKeyTarget, blockNeighbours, missingBlockHint, parseBlockHeight } from "./block-nav";
import type { KeyEventLike } from "./block-nav";

/** A stand-in for a DOM element: the tests run in node, where there is no document. */
const element = (tagName: string, isContentEditable = false) => ({ tagName, isContentEditable }) as unknown as EventTarget;

const key = (k: string, over: Partial<KeyEventLike> = {}): KeyEventLike => ({
  key: k,
  ctrlKey: false,
  metaKey: false,
  altKey: false,
  shiftKey: false,
  target: element("BODY"),
  ...over,
});

describe("parseBlockHeight", () => {
  it("TestParseBlockHeight_accepts_whole_numbers", () => {
    expect(parseBlockHeight("1284410")).toBe(1284410);
    expect(parseBlockHeight("1")).toBe(1);
  });

  it("TestParseBlockHeight_rejects_non_integers", () => {
    for (const bad of ["12.5", "1e3", "abc", "", " 5", "0x10", "1,284,410"]) {
      expect(parseBlockHeight(bad)).toBeNull();
    }
    expect(parseBlockHeight(undefined)).toBeNull();
  });

  it("TestParseBlockHeight_parses_zero_and_negative_for_the_caller_to_reject", () => {
    expect(parseBlockHeight("0")).toBe(0);
    expect(parseBlockHeight("-3")).toBe(-3);
  });

  it("TestParseBlockHeight_rejects_numbers_beyond_safe_integers", () => {
    expect(parseBlockHeight("99999999999999999999")).toBeNull();
  });
});

describe("blockNeighbours", () => {
  it("TestBlockNeighbours_middle_has_both", () => {
    expect(blockNeighbours(5, 10)).toEqual({ prev: 4, next: 6 });
  });

  it("TestBlockNeighbours_first_block_has_no_previous", () => {
    expect(blockNeighbours(1, 10).prev).toBeNull();
  });

  it("TestBlockNeighbours_head_has_no_next", () => {
    expect(blockNeighbours(10, 10).next).toBeNull();
    expect(blockNeighbours(9, 10).next).toBe(10);
  });

  it("TestBlockNeighbours_unknown_head_has_no_next", () => {
    expect(blockNeighbours(5, null)).toEqual({ prev: 4, next: null });
  });

  it("TestBlockNeighbours_chain_of_one_block_has_neither", () => {
    expect(blockNeighbours(1, 1)).toEqual({ prev: null, next: null });
  });
});

describe("blockKeyTarget", () => {
  const nav = { prev: 4, next: 6 };

  it("TestBlockKeyTarget_arrows_go_to_neighbours", () => {
    expect(blockKeyTarget(key("ArrowLeft"), nav)).toBe(4);
    expect(blockKeyTarget(key("ArrowRight"), nav)).toBe(6);
  });

  it("TestBlockKeyTarget_no_neighbour_means_no_move", () => {
    expect(blockKeyTarget(key("ArrowLeft"), { prev: null, next: 6 })).toBeNull();
    expect(blockKeyTarget(key("ArrowRight"), { prev: 4, next: null })).toBeNull();
  });

  it("TestBlockKeyTarget_ignores_typing_targets", () => {
    for (const tagName of ["INPUT", "TEXTAREA", "SELECT"]) {
      expect(blockKeyTarget(key("ArrowLeft", { target: element(tagName) }), nav)).toBeNull();
    }
    expect(blockKeyTarget(key("ArrowLeft", { target: element("DIV", true) }), nav)).toBeNull();
  });

  it("TestBlockKeyTarget_ignores_modifier_keys", () => {
    for (const mod of ["ctrlKey", "metaKey", "altKey", "shiftKey"] as const) {
      expect(blockKeyTarget(key("ArrowRight", { [mod]: true }), nav)).toBeNull();
    }
  });

  it("TestBlockKeyTarget_ignores_other_keys_and_missing_target", () => {
    expect(blockKeyTarget(key("a"), nav)).toBeNull();
    expect(blockKeyTarget(key("ArrowLeft", { target: null }), nav)).toBe(4);
  });
});

describe("missingBlockHint", () => {
  it("TestMissingBlockHint_not_a_number", () => {
    expect(missingBlockHint(null, 10)).toContain("whole number");
  });

  it("TestMissingBlockHint_below_the_first_block", () => {
    expect(missingBlockHint(0, 10)).toBe("Block heights start at 1.");
    expect(missingBlockHint(-4, 10)).toBe("Block heights start at 1.");
  });

  it("TestMissingBlockHint_above_the_head_says_chain_has_not_reached_it", () => {
    expect(missingBlockHint(2000000, 1284410)).toBe(
      "The chain has not reached block 2,000,000 yet. The latest block is 1,284,410.",
    );
    expect(missingBlockHint(11, 10)).toContain("has not reached block 11");
  });

  it("TestMissingBlockHint_the_head_itself_is_not_above_the_head", () => {
    expect(missingBlockHint(10, 10)).toBe("There is no block 10 on this chain.");
  });

  it("TestMissingBlockHint_unknown_head_or_within_range", () => {
    expect(missingBlockHint(50, null)).toBe("There is no block 50 on this chain.");
    expect(missingBlockHint(50, 100)).toBe("There is no block 50 on this chain.");
  });
});
