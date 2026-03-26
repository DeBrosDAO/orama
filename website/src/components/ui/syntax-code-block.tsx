import { useEffect, useState } from "react";
import { createHighlighter } from "shiki";
import type { Highlighter } from "shiki";
import { cn } from "../../lib/utils";

let highlighterPromise: Promise<Highlighter> | null = null;

function getHighlighter() {
  if (!highlighterPromise) {
    highlighterPromise = createHighlighter({
      themes: ["vitesse-dark"],
      langs: ["typescript"],
    });
  }
  return highlighterPromise;
}

export interface SyntaxCodeBlockProps {
  code: string;
  language?: string;
  label?: string;
  className?: string;
}

export function SyntaxCodeBlock({
  code,
  language = "typescript",
  label,
  className,
}: SyntaxCodeBlockProps) {
  const [html, setHtml] = useState("");

  useEffect(() => {
    getHighlighter().then((h) => {
      setHtml(h.codeToHtml(code, { lang: language, theme: "vitesse-dark" }));
    });
  }, [code, language]);

  return (
    <div className={cn("flex flex-col gap-3", className)}>
      <div className="relative border border-dashed border-border overflow-hidden rounded-sm">
        {/* Corner markers */}
        <div className="absolute -top-1 -left-1 w-2.5 h-2.5">
          <div className="absolute top-1/2 left-0 w-full h-px bg-accent/30" />
          <div className="absolute left-1/2 top-0 h-full w-px bg-accent/30" />
        </div>
        <div className="absolute -top-1 -right-1 w-2.5 h-2.5">
          <div className="absolute top-1/2 left-0 w-full h-px bg-accent/30" />
          <div className="absolute left-1/2 top-0 h-full w-px bg-accent/30" />
        </div>
        <div className="absolute -bottom-1 -left-1 w-2.5 h-2.5">
          <div className="absolute top-1/2 left-0 w-full h-px bg-accent/30" />
          <div className="absolute left-1/2 top-0 h-full w-px bg-accent/30" />
        </div>
        <div className="absolute -bottom-1 -right-1 w-2.5 h-2.5">
          <div className="absolute top-1/2 left-0 w-full h-px bg-accent/30" />
          <div className="absolute left-1/2 top-0 h-full w-px bg-accent/30" />
        </div>

        {html ? (
          <div
            className="p-6 overflow-x-auto text-sm leading-relaxed [&_pre]:!bg-transparent [&_code]:font-mono"
            dangerouslySetInnerHTML={{ __html: html }}
          />
        ) : (
          <pre className="p-6 font-mono text-sm text-muted leading-relaxed overflow-x-auto">
            {code}
          </pre>
        )}
      </div>

      {label && (
        <p className="text-xs font-mono text-muted text-center tracking-wider uppercase">
          {label}
        </p>
      )}
    </div>
  );
}
