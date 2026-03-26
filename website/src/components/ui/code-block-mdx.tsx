import { useEffect, useState, useCallback, type ReactElement } from "react";
import { createHighlighter } from "shiki";
import type { Highlighter } from "shiki";
import { Mermaid } from "./mermaid";

let highlighterPromise: Promise<Highlighter> | null = null;

function getHighlighter() {
  if (!highlighterPromise) {
    highlighterPromise = createHighlighter({
      themes: ["vitesse-dark"],
      langs: ["typescript", "javascript", "bash", "json", "go", "yaml", "html", "css", "sql", "toml", "ini"],
    });
  }
  return highlighterPromise;
}

function CopyButton({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);

  const handleCopy = useCallback(() => {
    navigator.clipboard.writeText(text);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  }, [text]);

  return (
    <button
      type="button"
      onClick={handleCopy}
      className="absolute top-2 right-2 px-2 py-1 text-xs font-mono text-muted hover:text-fg bg-surface-2/80 hover:bg-surface-2 border border-dashed border-border rounded-sm transition-colors"
    >
      {copied ? "copied" : "copy"}
    </button>
  );
}

function extractTextContent(children: unknown): string {
  if (typeof children === "string") return children;
  if (Array.isArray(children)) return children.map(extractTextContent).join("");
  if (children && typeof children === "object" && "props" in (children as object)) {
    const el = children as ReactElement<{ children?: unknown }>;
    return extractTextContent(el.props.children);
  }
  return "";
}

export function MdxPre({ children, ...props }: React.ComponentPropsWithoutRef<"pre">) {
  const codeElement = children as ReactElement<{
    className?: string;
    children?: unknown;
  }>;

  const className = codeElement?.props?.className || "";
  const lang = className.replace("language-", "") || "";
  const rawCode = extractTextContent(codeElement?.props?.children).replace(/\n$/, "");

  if (lang === "mermaid") {
    return <Mermaid chart={rawCode} />;
  }

  return <HighlightedBlock code={rawCode} lang={lang} {...props} />;
}

function HighlightedBlock({ code, lang }: { code: string; lang: string }) {
  const [html, setHtml] = useState("");

  useEffect(() => {
    getHighlighter().then((h) => {
      const loadedLangs = h.getLoadedLanguages();
      const effectiveLang = loadedLangs.includes(lang) ? lang : "text";
      setHtml(
        h.codeToHtml(code, { lang: effectiveLang, theme: "vitesse-dark" }),
      );
    });
  }, [code, lang]);

  return (
    <div className="relative group border border-dashed border-border rounded-sm bg-surface overflow-hidden mb-4">
      {lang && (
        <div className="flex items-center justify-between px-4 py-1.5 border-b border-dashed border-border">
          <span className="font-mono text-[10px] tracking-wider uppercase text-muted">
            {lang}
          </span>
        </div>
      )}
      <CopyButton text={code} />
      {html ? (
        <div
          className="p-4 overflow-x-auto text-sm leading-relaxed [&_pre]:!bg-transparent [&_pre]:!m-0 [&_code]:font-mono"
          dangerouslySetInnerHTML={{ __html: html }}
        />
      ) : (
        <pre className="p-4 font-mono text-sm text-muted leading-relaxed overflow-x-auto">
          {code}
        </pre>
      )}
    </div>
  );
}
