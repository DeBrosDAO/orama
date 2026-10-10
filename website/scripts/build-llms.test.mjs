import { describe, expect, it } from "vitest";
import { mdxToMarkdown } from "./build-llms.mjs";

describe("mdxToMarkdown", () => {
  it("TestMdxToMarkdown_drops_front_matter_and_esm_lines", () => {
    const out = mdxToMarkdown('---\ntitle: x\n---\nimport Foo from "./foo";\nexport default Layout;\n\n# Title\n\nBody.\n');
    expect(out).toBe("# Title\n\nBody.\n");
  });

  it("TestMdxToMarkdown_drops_jsx_including_multiline_tags", () => {
    const out = mdxToMarkdown('# T\n\n<div className="a">\n  <iframe\n    src="x"\n    allowFullScreen\n  />\n</div>\n\nText.\n');
    expect(out).toBe("# T\n\nText.\n");
  });

  it("TestMdxToMarkdown_keeps_code_fences_untouched", () => {
    const code = '```ts\nimport { createClient } from "@debros/orama";\nexport default App;\n<div>\n```';
    expect(mdxToMarkdown(`# T\n\n${code}\n`)).toBe(`# T\n\n${code}\n`);
  });

  it("TestMdxToMarkdown_longer_fence_closes_only_on_same_length", () => {
    const body = '````md\n```\nimport x from "y";\n```\n````';
    expect(mdxToMarkdown(`${body}\nimport a from "b";\nafter\n`)).toBe(`${body}\nafter\n`);
  });

  it("TestMdxToMarkdown_empty_input", () => {
    expect(mdxToMarkdown("")).toBe("\n");
  });
});
