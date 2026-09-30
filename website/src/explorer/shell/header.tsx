import { Link, NavLink } from "react-router";
import { Search } from "lucide-react";
import { useHead, useHeadError, useSource } from "../data/provider";
import { explorerPaths } from "../model/routes";
import { formatInt } from "../model/units";
import { cn } from "../../lib/utils";
import oramaIcon from "../../assets/orama-icon.png";
import { usePalette } from "./palette";

const NAV = [
  { label: "Explore", to: explorerPaths.home, end: true },
  { label: "Validators", to: explorerPaths.validators, end: false },
] as const;

function NavLinks({ className }: { className?: string }) {
  return (
    <div className={cn("flex items-center gap-1", className)}>
      {NAV.map((n) => (
        <NavLink
          key={n.to}
          to={n.to}
          end={n.end}
          className={({ isActive }) =>
            cn("rounded-full px-3 py-1.5 text-[13px] transition-colors", isActive ? "bg-white/[0.07] text-fg" : "text-muted hover:text-fg")
          }
        >
          {n.label}
        </NavLink>
      ))}
    </div>
  );
}

export function ExplorerHeader() {
  const { open } = usePalette();
  const head = useHead();
  const headError = useHeadError();
  const origin = useSource().origin;
  return (
    <header className="sticky top-0 z-40 border-b border-border bg-surface/90 backdrop-blur-xl">
      <div className="mx-auto flex h-14 max-w-[1140px] items-center gap-3 px-4 sm:gap-5 sm:px-6">
        <Link to="/" className="flex shrink-0 items-center gap-2" aria-label="Orama home">
          <img src={oramaIcon} alt="" className="h-6 w-6" />
          <span className="font-display text-sm font-bold tracking-[0.14em]">ORAMA</span>
        </Link>
        <NavLinks className="hidden md:flex" />
        <button
          type="button"
          onClick={open}
          className="mx-auto flex min-w-0 max-w-md flex-1 items-center gap-2.5 rounded-lg border border-border bg-bg px-3 py-2 text-left text-[13px] text-muted hover:border-fg/30 cursor-pointer"
        >
          <Search size={15} aria-hidden="true" className="shrink-0" />
          <span className="min-w-0 flex-1 truncate">Search address, transaction, block…</span>
          <kbd className="hidden rounded border border-border bg-surface-2 px-1.5 font-mono text-[11px] sm:block">⌘K</kbd>
        </button>
        {origin.kind === "demo" && (
          <span className="shrink-0 rounded-full border border-signal/40 px-2 py-0.5 text-xs text-signal">{origin.label}</span>
        )}
        <div className="hidden shrink-0 items-center gap-2 text-xs text-muted lg:flex">
          <span className="inline-flex items-center gap-2 tabular-nums">
            <span className="h-1.5 w-1.5 animate-pulse-dot rounded-full bg-gain text-gain" aria-hidden="true" />
            block{" "}
            {headError && !head ? (
              <b className="font-medium text-loss" title={headError.message}>unavailable</b>
            ) : (
              <b className="font-mono font-medium text-fg">{head ? formatInt(head.height) : "…"}</b>
            )}
          </span>
        </div>
      </div>
      <NavLinks className="overflow-x-auto border-t border-border px-3 py-1.5 [scrollbar-width:none] md:hidden [&::-webkit-scrollbar]:hidden" />
    </header>
  );
}
