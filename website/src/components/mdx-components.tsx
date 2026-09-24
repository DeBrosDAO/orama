import type { MDXComponents } from "mdx/types";
import { mdxBaseComponents } from "./mdx-base";
import { MdxPre } from "./ui/code-block-mdx";

/** Markdown styling for the docs: the base set plus highlighted code blocks. */
export const mdxComponents: MDXComponents = {
  ...mdxBaseComponents,
  pre: MdxPre,
};
