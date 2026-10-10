import { Download, FileText } from "lucide-react";
import { Button } from "../ui/button";
import { DashedPanel } from "../ui/dashed-panel";
import { WHITEPAPER, WHITEPAPER_REFERENCE, WHITEPAPER_SHORT, WHITEPAPER_SOURCE_PATH, WHITEPAPER_SOURCE_URL } from "../../content/whitepaper";
import type { WhitepaperFile } from "../../content/whitepaper";

function meta(file: WhitepaperFile): string {
  return `PDF · ${file.pages} pages · ${file.size} · v${WHITEPAPER.version}`;
}

function Primary() {
  return (
    <DashedPanel withBackground withCorners>
      <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-5">
        <div className="flex items-start gap-4">
          <FileText className="w-6 h-6 text-accent shrink-0 mt-1" aria-hidden />
          <div>
            <p className="font-display text-xl font-semibold text-fg">{WHITEPAPER_SHORT.title}</p>
            <p className="text-sm text-muted mt-1 text-pretty">
              The whole system in {WHITEPAPER_SHORT.pages} pages: architecture, security, the chain, and the limits we know about. Start here.
            </p>
            <p className="font-mono text-[11px] tracking-wider uppercase text-muted mt-3">{meta(WHITEPAPER_SHORT)}</p>
          </div>
        </div>
        <Button asChild size="lg" className="shrink-0">
          <a href={WHITEPAPER_SHORT.href} download>
            <Download className="w-3.5 h-3.5 mr-2" aria-hidden />
            Download PDF
          </a>
        </Button>
      </div>
    </DashedPanel>
  );
}

function Reference() {
  const total = WHITEPAPER.referenceTotal;
  return (
    <div className="mt-6">
      <p className="font-display text-lg font-semibold text-fg">Technical Reference</p>
      <p className="text-sm text-muted mt-1 text-pretty">
        The exhaustive reference behind the edition above: {total.pages.toLocaleString("en-US")} pages in three volumes, {total.size} in all. It is meant to be looked things up in, not read front to back.
      </p>
      <ul className="mt-4 divide-y divide-dashed divide-border border-y border-dashed border-border">
        {WHITEPAPER_REFERENCE.map((file) => (
          <li key={file.key} className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 py-3">
            <div>
              <span className="text-sm text-fg">{file.title}</span>
              <span className="block font-mono text-[11px] tracking-wider uppercase text-muted mt-0.5">{meta(file)}</span>
            </div>
            <Button asChild variant="ghost" size="sm">
              <a href={file.href} download aria-label={`Download ${file.title} (PDF, ${file.size})`}>
                <Download className="w-3 h-3 mr-1.5" aria-hidden />
                Download
              </a>
            </Button>
          </li>
        ))}
      </ul>
    </div>
  );
}

/** The whitepaper PDFs, above the short overview the page renders. Not printed. */
export function WhitepaperDownloads() {
  return (
    <section aria-label="Download the whitepaper" className="no-print">
      <p className="font-mono text-[11px] tracking-[0.25em] uppercase text-muted mb-4">Download the whitepaper</p>
      <Primary />
      <Reference />
      <p className="text-sm text-muted mt-6 text-pretty">
        Every claim in both editions is checked against the code at version {WHITEPAPER.version}. The chapter-by-chapter source is in the repository under{" "}
        <a href={WHITEPAPER_SOURCE_URL} className="font-mono text-accent hover:underline underline-offset-4" rel="noopener">
          {WHITEPAPER_SOURCE_PATH}
        </a>
        . The overview below is the short version, in a page.
      </p>
    </section>
  );
}
