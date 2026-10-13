import { describe, expect, it } from "vitest";
import { renderDiagram } from "./mermaid";

describe("renderDiagram", () => {
  it("TestRenderDiagram_returns_the_svg", async () => {
    const got = await renderDiagram(async () => ({ svg: "<svg/>" }), "m-1", "graph TD; A-->B");
    expect(got).toEqual({ svg: "<svg/>", error: "" });
  });

  it("TestRenderDiagram_a_bad_chart_is_an_error_not_a_rejection", async () => {
    const got = await renderDiagram(async () => {
      throw new Error("Parse error on line 1");
    }, "m-2", "graph ???");
    expect(got).toEqual({ svg: "", error: "Parse error on line 1" });
  });

  it("TestRenderDiagram_a_non_error_rejection_is_reported_as_text", async () => {
    const got = await renderDiagram(() => Promise.reject("boom"), "m-3", "x");
    expect(got.error).toBe("boom");
  });
});
