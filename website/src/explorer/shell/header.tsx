import { Link, NavLink, useLocation } from "react-router";
import { Search } from "lucide-react";
import { useHead, useHeadError } from "../data/provider";
import { parseExplorerPath } from "../model/routes";
import { useExplorerPaths } from "../network/active";
import { formatInt } from "../model/units";
import { cn } from "../../lib/utils";
import oramaIcon from "../../assets/orama-icon.png";
import { AssetsMenu } from "./assets-menu";
import { usePalette } from "./palette";

const PILL = "rounded-full px-3 py-1.5 text-[13px] transition-colors";
const ON = "bg-white/[0.07] text-fg";
const OFF = "text-muted hover:text-fg";

interface NavItem {
  label: string;
  to: string;
  end: boolean;
  /** A page inside this section (a validator's own page) keeps the section lit without being the section's own address. */
  inside: boolean;
}

export function navItems(paths: ReturnType<typeof useExplorerPaths>, kind: string): NavItem[] {
  return [
    { label: "Explore", to: paths.home, end: true, inside: false },
    { label: "Validators", to: paths.validators, end: false, inside: kind === "validator" },
    { label: "Nodes", to: paths.nodes, end: false, inside: kind === "node" },
    { label: "Storage", to: paths.storage, end: false, inside: false },
    { label: "Tor", to: paths.tor, end: false, inside: false },
    { label: "Epochs", to: paths.epochs, end: false, inside: false },
    { label: "Rewards", to: paths.rewards, end: false, inside: false },
    { label: "Emission", to: paths.emission, end: false, inside: false },
    { label: "Supply", to: paths.economics, end: false, inside: false },
  ];
}

/** The explorer's sections as one navigation landmark; `aria-current` says which page the reader is on. */
function NavLinks({ className, narrow = false }: { className?: string; narrow?: boolean }) {
  const paths = useExplorerPaths();
  const { pathname } = useLocation();
  const kind = parseExplorerPath(pathname).kind;
  return (
    <nav aria-label="Explorer sections" className={cn("flex items-center gap-1", className)}>
      {navItems(paths, kind).map((n) =>
        n.inside ? (
          <Link key={n.to} to={n.to} aria-current="true" className={cn(PILL, ON)}>
            {n.label}
          </Link>
        ) : (
          <NavLink key={n.to} to={n.to} end={n.end} className={({ isActive }) => cn(PILL, isActive ? ON : OFF)}>
            {n.label}
          </NavLink>
        ),
      )}
      <AssetsMenu inline={narrow} />
    </nav>
  );
}

export function ExplorerHeader() {
  const { open } = usePalette();
  const head = useHead();
  const headError = useHeadError();
  return (
    <header className="sticky top-0 z-40 border-b border-border bg-surface/90 backdrop-blur-xl">
      <div className="mx-auto flex h-14 max-w-[1140px] items-center gap-3 px-4 sm:gap-5 sm:px-6">
        <Link to="/" className="flex shrink-0 items-center gap-2" aria-label="Orama home">
          <img src={oramaIcon} alt="" className="h-6 w-6" />
          <span className="font-display text-sm font-bold tracking-[0.14em]">ORAMA</span>
        </Link>
        <NavLinks className="hidden xl:flex" />
        <button
          type="button"
          onClick={open}
          aria-keyshortcuts="Control+K Meta+K /"
          className="mx-auto flex min-w-0 max-w-md flex-1 items-center gap-2.5 rounded-lg border border-border bg-bg px-3 py-2 text-left text-[13px] text-muted hover:border-fg/30 cursor-pointer xl:mx-0 xl:ml-auto xl:w-40 xl:flex-none"
        >
          <Search size={15} aria-hidden="true" className="shrink-0" />
          {/* With the sections beside it the button has no room for the long text. */}
          <span className="min-w-0 flex-1 truncate xl:hidden">Search address, transaction, block…</span>
          <span className="hidden min-w-0 flex-1 truncate xl:inline">Search</span>
          <kbd className="hidden rounded border border-border bg-surface-2 px-1.5 font-mono text-[11px] sm:block">⌘K</kbd>
        </button>
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
      <NavLinks narrow className="overflow-x-auto border-t border-border px-3 py-1.5 [scrollbar-width:none] xl:hidden [&::-webkit-scrollbar]:hidden" />
    </header>
  );
}
