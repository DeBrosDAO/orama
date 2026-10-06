import { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react";
import type { ReactNode } from "react";
import { Link, useLocation } from "react-router";
import { ChevronRight } from "lucide-react";
import { explorerPaths } from "../model/routes";
import { crumbLabel, shareUrl } from "../model/trail";
import { CopyChip } from "../ui/copy";
import { cn } from "../../lib/utils";
import { EMPTY_TRAIL, initialTrailState, visitPage } from "./trail-state";

interface TrailContextValue {
  trail: readonly string[];
  /** The trail arrived in a shared link and the reader has not left it yet. */
  shared: boolean;
  clear: () => void;
  /** A link that reopens the current page with the whole trail. */
  shareLink: () => string;
}

const TrailContext = createContext<TrailContextValue | null>(null);

export function TrailProvider({ children }: { children: ReactNode }) {
  const { pathname, search } = useLocation();
  const [{ trail, shared }, setState] = useState(() => initialTrailState(search, pathname));

  useEffect(() => {
    setState((s) => visitPage(s, pathname));
  }, [pathname]);

  const clear = useCallback(() => setState(EMPTY_TRAIL), []);
  const shareLink = useCallback(() => shareUrl(window.location.origin, pathname, trail), [pathname, trail]);
  const value = useMemo(() => ({ trail, shared, clear, shareLink }), [trail, shared, clear, shareLink]);
  return <TrailContext.Provider value={value}>{children}</TrailContext.Provider>;
}

function useTrail(): TrailContextValue {
  const ctx = useContext(TrailContext);
  if (!ctx) throw new Error("useTrail must be used inside <TrailProvider>");
  return ctx;
}

/** The investigation trail: every page followed since Home, one click apart. */
export function TrailBar() {
  const { trail, shared, clear, shareLink } = useTrail();
  const { pathname } = useLocation();
  if (trail.length === 0) return null;
  return (
    <nav aria-label="Investigation trail" className="border-b border-border bg-surface/80">
      <div className="mx-auto flex max-w-[1140px] items-center gap-1.5 overflow-x-auto px-4 py-2 text-[13px] [scrollbar-width:none] sm:px-6 [&::-webkit-scrollbar]:hidden">
        <span className="mr-1 shrink-0 font-semibold text-signal">{shared ? "Shared trail" : "Trail"}</span>
        <Crumb to={explorerPaths.home} label="Home" />
        {trail.map((p) => (
          <span key={p} className="flex shrink-0 items-center gap-1.5">
            <ChevronRight size={12} className="text-muted/60" aria-hidden="true" />
            <Crumb to={p} label={crumbLabel(p)} current={p === pathname} />
          </span>
        ))}
        <span className="ml-auto flex shrink-0 items-center gap-2 pl-4">
          <TrailShare getLink={shareLink} />
          <button type="button" onClick={clear} className="text-xs text-muted underline underline-offset-4 hover:text-fg cursor-pointer">
            Clear
          </button>
        </span>
      </div>
    </nav>
  );
}

function TrailShare({ getLink }: { getLink: () => string }) {
  return <CopyChip value={getLink()} label="Copy a link to this trail" withText className="text-[12px]" />;
}

function Crumb({ to, label, current = false }: { to: string; label: string; current?: boolean }) {
  return (
    <Link
      to={to}
      aria-current={current ? "page" : undefined}
      className={cn(
        "shrink-0 rounded-md border px-2 py-0.5 transition-colors",
        current ? "border-fg/40 bg-surface-2 text-fg" : "border-border bg-surface-2 text-muted hover:text-fg",
      )}
    >
      {label}
    </Link>
  );
}
