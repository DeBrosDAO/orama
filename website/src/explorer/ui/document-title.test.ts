import { describe, expect, it } from "vitest";
import { documentTitle } from "./document-title";

describe("documentTitle", () => {
  it("appends the suffix for live data", () => {
    expect(documentTitle("Validators", false)).toBe("Validators · Orama Explorer");
  });

  it("marks demo data in the tab title", () => {
    expect(documentTitle("Validators", true)).toBe("Validators · Orama Explorer (demo)");
  });

  it("still produces a title for an empty page name", () => {
    expect(documentTitle("", true)).toBe(" · Orama Explorer (demo)");
  });
});
