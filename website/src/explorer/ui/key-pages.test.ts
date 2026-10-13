import { describe, expect, it } from "vitest";
import type { KeyPage } from "../model/paging";
import { allItems, nextKeyOf, ownedBy, withPage } from "./key-pages";

const page = (items: string[], nextKey: string | null): KeyPage<string> => ({ items, nextKey });

describe("key pages", () => {
  const first = page(["a", "b"], "k1");

  it("TestKeyPages_the_first_page_alone_is_its_items_and_its_key", () => {
    expect(allItems(first, null)).toEqual(["a", "b"]);
    expect(nextKeyOf(first, null)).toBe("k1");
    expect(allItems(null, null)).toEqual([]);
    expect(nextKeyOf(null, null)).toBeNull();
  });

  it("TestKeyPages_each_appended_page_adds_its_items_and_moves_the_key", () => {
    const one = withPage(first, null, page(["c"], "k2"));
    const two = withPage(first, one, page(["d", "e"], null));
    expect(allItems(first, two)).toEqual(["a", "b", "c", "d", "e"]);
    expect(nextKeyOf(first, two)).toBeNull();
  });

  it("TestKeyPages_pages_appended_to_another_first_page_are_dropped_when_the_list_starts_over", () => {
    const appended = withPage(first, null, page(["c"], "k2"));
    const again = page(["a", "b"], "k1");
    expect(ownedBy(again, appended)).toBeNull();
    expect(allItems(again, appended)).toEqual(["a", "b"]);
    expect(nextKeyOf(again, appended)).toBe("k1");
    expect(withPage(again, appended, page(["z"], null)).items).toEqual(["z"]);
  });
});
