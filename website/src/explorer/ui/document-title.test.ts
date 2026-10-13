import { describe, expect, it } from "vitest";
import { documentTitle } from "./document-title";

describe("documentTitle", () => {
  it("TestDocumentTitle_appends_the_suffix", () => {
    expect(documentTitle("Validators")).toBe("Validators · Orama Explorer");
  });

  it("TestDocumentTitle_still_produces_a_title_for_an_empty_page_name", () => {
    expect(documentTitle("")).toBe(" · Orama Explorer");
  });
});
