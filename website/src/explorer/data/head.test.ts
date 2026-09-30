import { describe, expect, it } from "vitest";
import { nextHead } from "./head";

const head = (height: number, time = "2026-09-29T14:00:00.000Z") => ({ height, time });

describe("nextHead", () => {
  it("TestNextHead_first_head_is_taken", () => {
    const first = head(10);
    expect(nextHead(null, first)).toBe(first);
  });

  it("TestNextHead_a_higher_head_replaces_the_current_one", () => {
    const next = head(11);
    expect(nextHead(head(10), next)).toBe(next);
  });

  it("TestNextHead_never_goes_backwards", () => {
    const prev = head(10);
    expect(nextHead(prev, head(9))).toBe(prev);
    expect(nextHead(prev, head(0))).toBe(prev);
  });

  it("TestNextHead_same_height_keeps_the_existing_object", () => {
    const prev = head(10);
    expect(nextHead(prev, head(10, "2026-09-29T14:00:02.000Z"))).toBe(prev);
  });
});
