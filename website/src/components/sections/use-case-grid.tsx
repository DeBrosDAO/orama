import { Lightbulb } from "lucide-react";
import { SERVICES } from "../../content/services";
import { EMAILS } from "../../content/site";
import { USE_CASES, USE_CASE_TAG_LABEL } from "../../content/use-cases";
import type { UseCase, UseCaseTag } from "../../content/use-cases";
import { cn } from "../../lib/utils";
import { StatusBadge } from "../ui/status-badge";
import type { BadgeTone } from "../ui/status-badge";

const TAG_TONE: Record<UseCaseTag, BadgeTone> = {
  live: "now",
  ready: "soon",
  "orama-one": "future",
};

const SERVICE_NAME = new Map(SERVICES.map((s) => [s.id, s.name]));

export interface UseCaseGridProps {
  /** Limit to these use cases, in this order. Default: all. */
  ids?: string[];
  /** Show which services each one uses. */
  showServices?: boolean;
  /** End the grid with an invitation to bring an idea. */
  invite?: boolean;
}

function pick(ids?: string[]): UseCase[] {
  if (!ids) return USE_CASES;
  return ids.map((id) => {
    const found = USE_CASES.find((u) => u.id === id);
    if (!found) throw new Error(`UseCaseGrid: unknown use case "${id}"`);
    return found;
  });
}

function InviteTile() {
  return (
    <li className="flex flex-col gap-4 p-6 border border-fg/20 bg-white/[0.02]">
      <Lightbulb size={28} strokeWidth={1.25} className="text-fg" />
      <h3 className="font-display font-semibold text-lg text-fg leading-tight">What would you build?</h3>
      <p className="text-sm text-muted flex-1">Tell us the idea. We read everything.</p>
      <a href={`mailto:${EMAILS.dev}`} className="font-mono text-xs text-accent hover:text-fg transition-colors">
        {EMAILS.dev}
      </a>
    </li>
  );
}

export function UseCaseGrid({ ids, showServices = false, invite = false }: UseCaseGridProps) {
  return (
    <ul className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
      {pick(ids).map(({ id, icon: Icon, title, line, tag, uses, beta }) => (
        <li
          key={id}
          className={cn(
            "flex flex-col gap-4 p-6 border bg-surface/60 transition-colors",
            tag === "orama-one" ? "border-dashed border-signal/20 hover:border-signal/40" : "border-dashed border-border hover:border-fg/25",
          )}
        >
          <div className="flex items-start justify-between gap-3">
            <Icon size={28} strokeWidth={1.25} className={tag === "orama-one" ? "text-signal/90" : "text-fg"} />
            <StatusBadge tone={TAG_TONE[tag]}>{USE_CASE_TAG_LABEL[tag]}</StatusBadge>
          </div>
          <h3 className="font-display font-semibold text-lg text-fg leading-tight">{title}</h3>
          <p className="text-sm text-muted flex-1">{line}</p>
          {showServices && (
            <div className="flex flex-wrap gap-1.5 pt-3 border-t border-dashed border-border">
              {uses.map((s) => (
                <span key={s} className="px-2 py-0.5 text-[10px] font-mono uppercase tracking-wider text-accent border border-border/70">
                  {SERVICE_NAME.get(s)}
                </span>
              ))}
              {beta?.map((b) => (
                <span key={b} className="px-2 py-0.5 text-[10px] font-mono uppercase tracking-wider text-accent border border-dashed border-border/70">
                  {b} (beta)
                </span>
              ))}
            </div>
          )}
        </li>
      ))}
      {invite && <InviteTile />}
    </ul>
  );
}

export function UseCaseLegend() {
  return (
    <div className="flex flex-wrap items-center justify-center gap-x-6 gap-y-3 text-xs text-muted">
      <span className="flex items-center gap-2"><StatusBadge tone="now">Live</StatusBadge>an app does this on Orama today</span>
      <span className="flex items-center gap-2"><StatusBadge tone="soon">Ready now</StatusBadge>every piece it needs exists</span>
      <span className="flex items-center gap-2"><StatusBadge tone="future">With Orama One</StatusBadge>needs our node hardware</span>
    </div>
  );
}
