import { describe, expect, it } from "vitest";
import { explorerPaths } from "../model/routes";
import { EMPTY_TRAIL, initialTrailState, visitPage } from "./trail-state";

const A = explorerPaths.validators;
const B = explorerPaths.block(12);
const C = explorerPaths.block(13);

function shared(paths: string[], landing: string = paths[paths.length - 1] as string) {
  return initialTrailState(`?trail=${encodeURIComponent(paths.join("|"))}`, landing);
}

describe("initialTrailState", () => {
  it("is shared when the link carried a trail", () => {
    expect(shared([A, B])).toEqual({ trail: [A, B], shared: true });
  });

  it("is empty and not shared without one", () => {
    expect(initialTrailState("", A)).toEqual(EMPTY_TRAIL);
  });

  it("drops a hand-made trail that omits the page it landed on", () => {
    expect(shared([A, B], C)).toEqual(EMPTY_TRAIL);
  });

  it("ignores a trail made only of foreign paths", () => {
    expect(initialTrailState("?trail=https%3A%2F%2Fevil.example", A)).toEqual(EMPTY_TRAIL);
  });
});

describe("visitPage", () => {
  it("stays shared while landing on a page already in the trail", () => {
    const state = visitPage(shared([A, B]), B);
    expect(state.shared).toBe(true);
    expect(state.trail).toEqual([A, B]);
  });

  it("becomes the reader's own trail on a new page", () => {
    const state = visitPage(shared([A, B]), C);
    expect(state.shared).toBe(false);
    expect(state.trail).toEqual([A, B, C]);
  });

  it("stays own once it has stopped being shared", () => {
    const own = visitPage(shared([A, B]), C);
    expect(visitPage(own, B).shared).toBe(false);
  });

  it("is not shared after going home", () => {
    expect(visitPage(shared([A, B]), explorerPaths.home)).toEqual(EMPTY_TRAIL);
  });

  it("a trail the reader built by clicking is never shared", () => {
    expect(visitPage(EMPTY_TRAIL, A).shared).toBe(false);
  });
});
