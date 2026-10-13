import { describe, expect, it } from "vitest";
import { nextTabIndex } from "./tab-nav";

describe("nextTabIndex", () => {
  it("TestNextTabIndex_arrows_move_one_step", () => {
    expect(nextTabIndex(0, 2, "ArrowRight")).toBe(1);
    expect(nextTabIndex(1, 3, "ArrowLeft")).toBe(0);
  });

  it("TestNextTabIndex_arrows_wrap_around", () => {
    expect(nextTabIndex(1, 2, "ArrowRight")).toBe(0);
    expect(nextTabIndex(0, 2, "ArrowLeft")).toBe(1);
  });

  it("TestNextTabIndex_home_and_end_jump_to_the_ends", () => {
    expect(nextTabIndex(1, 4, "Home")).toBe(0);
    expect(nextTabIndex(1, 4, "End")).toBe(3);
  });

  it("TestNextTabIndex_other_keys_do_nothing", () => {
    expect(nextTabIndex(0, 2, "Tab")).toBeNull();
    expect(nextTabIndex(0, 2, "ArrowDown")).toBeNull();
  });

  it("TestNextTabIndex_single_tab_stays_put", () => {
    expect(nextTabIndex(0, 1, "ArrowRight")).toBe(0);
  });

  it("TestNextTabIndex_no_tabs_or_bad_index_does_nothing", () => {
    expect(nextTabIndex(0, 0, "ArrowRight")).toBeNull();
    expect(nextTabIndex(5, 2, "ArrowRight")).toBeNull();
    expect(nextTabIndex(-1, 2, "ArrowRight")).toBeNull();
  });
});
