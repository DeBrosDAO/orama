import { plainText } from "../blog/parse-post";

/**
 * The search-result title and description of a docs page, read from its
 * MDX source at build time: the H1, and the first paragraph after it. The
 * docs are written for people, so their opening paragraph already says what
 * the page covers; this keeps the snippet and the page from drifting apart.
 */

export interface DocMeta {
  title: string;
  description: string;
}

/** Search results cut descriptions off past ~160 characters. */
export const MAX_DOC_DESCRIPTION = 155;
const MIN_DOC_DESCRIPTION = 40;

/** Cut at a word boundary, so a snippet never ends mid-word. */
export function truncateAtWord(text: string, max: number): string {
  if (text.length <= max) return text;
  const cut = text.slice(0, max - 1);
  const space = cut.lastIndexOf(" ");
  return `${(space > max / 2 ? cut.slice(0, space) : cut).replace(/[\s,;:.-]+$/, "")}…`;
}

function firstParagraph(lines: string[], from: number): string {
  const para: string[] = [];
  let inFence = false;
  for (let i = from; i < lines.length; i++) {
    const line = lines[i].trim();
    if (/^(`{3,}|~{3,})/.test(line)) {
      inFence = !inFence;
      continue;
    }
    if (inFence) continue;
    const isProse = line && !/^(#|<|import |export |\||---|- |\* |\d+\. |>)/.test(line);
    if (isProse) para.push(line);
    else if (para.length > 0) break;
  }
  return plainText(para.join(" ")).replace(/\s+/g, " ").replace(/\s--\s/g, " — ");
}

export function docMeta(slug: string, raw: string): DocMeta {
  const lines = raw.replace(/\r\n/g, "\n").split("\n");
  const h1 = lines.findIndex((l) => /^#\s+\S/.test(l));
  if (h1 === -1) throw new Error(`docs: src/docs/${slug}.mdx has no "# Title" line`);
  const title = plainText(lines[h1].replace(/^#\s+/, ""));
  const description = truncateAtWord(firstParagraph(lines, h1 + 1), MAX_DOC_DESCRIPTION);
  if (description.length < MIN_DOC_DESCRIPTION) {
    throw new Error(
      `docs: src/docs/${slug}.mdx needs an opening paragraph of at least ${MIN_DOC_DESCRIPTION} characters under its title; ` +
        `search engines show it as the page's description (found "${description}")`,
    );
  }
  return { title, description };
}
