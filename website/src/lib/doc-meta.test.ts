import { describe, expect, it } from "vitest";
import { MAX_DOC_DESCRIPTION, docMeta, truncateAtWord } from "./doc-meta";

describe("docMeta", () => {
  it("TestDocMeta_title_and_first_paragraph", () => {
    const raw = "import X from './x';\n\n# Cache\n\nDistributed caching powered by [Olric](https://x) -- fast, `replicated` storage for **apps**.\n\n## Overview\n\nNot this.";
    expect(docMeta("developer/cache", raw)).toEqual({
      title: "Cache",
      description: "Distributed caching powered by Olric — fast, replicated storage for apps.",
    });
  });

  it("TestDocMeta_skips_code_tables_and_lists", () => {
    const raw = "# T\n\n```bash\nnot prose here at all, ignore it please\n```\n\n| a | b |\n\n- item\n\nThe real opening paragraph of this document, spanning\ntwo lines of source.";
    expect(docMeta("x", raw).description).toBe("The real opening paragraph of this document, spanning two lines of source.");
  });

  it("TestDocMeta_long_paragraph_truncated", () => {
    const raw = `# T\n\n${"lorem ipsum ".repeat(40)}`;
    const { description } = docMeta("x", raw);
    expect(description.length).toBeLessThanOrEqual(MAX_DOC_DESCRIPTION);
    expect(description.endsWith("…")).toBe(true);
  });

  it("TestDocMeta_missing_title_or_paragraph_throws", () => {
    expect(() => docMeta("x", "No heading.")).toThrow("no \"# Title\"");
    expect(() => docMeta("x", "# T\n\n## Straight to sections")).toThrow("opening paragraph");
  });

  it("TestTruncateAtWord", () => {
    expect(truncateAtWord("short", 10)).toBe("short");
    expect(truncateAtWord("one two three four", 12)).toBe("one two…");
  });
});
