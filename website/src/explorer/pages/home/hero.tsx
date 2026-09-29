import { Link } from "react-router";
import { Search } from "lucide-react";
import { useQuery } from "../../data/use-query";
import { explorerPaths } from "../../model/routes";
import type { ExampleTargets } from "../../model/types";
import { usePalette } from "../../shell/palette";
import { Skeleton } from "../../ui/query-states";
import { cn } from "../../../lib/utils";

const CHIP_BASE = "inline-flex items-center rounded-full border px-3 py-1 text-[13px] transition-colors";
const CHIP_LINK = `${CHIP_BASE} border-border bg-surface-2 text-muted hover:border-fg/30 hover:text-fg`;

const SEARCH_PLACEHOLDER = "Paste an address, transaction hash or block number…";

function ExampleChip({ to, children }: { to: string; children: string }) {
  return (
    <Link to={to} className={CHIP_LINK}>
      {children}
    </Link>
  );
}

/** An example the source could not find is left out, not shown as a dead chip. */
function TryRow() {
  const { state } = useQuery((s) => s.getExamples(), []);
  const examples: ExampleTargets | null = state.status === "ready" ? state.data : null;
  return (
    <div className="mt-3 flex flex-wrap items-center justify-center gap-2 text-[13px] text-muted">
      <span>Not sure? Try</span>
      {state.status === "loading" ? (
        <>
          <Skeleton className="h-[30px] w-40 rounded-full" />
          <Skeleton className="h-[30px] w-28 rounded-full" />
        </>
      ) : (
        <>
          {examples?.latestTxHash ? <ExampleChip to={explorerPaths.tx(examples.latestTxHash)}>the latest transaction</ExampleChip> : null}
          {examples?.busyWalletAddress ? <ExampleChip to={explorerPaths.wallet(examples.busyWalletAddress)}>a busy wallet</ExampleChip> : null}
        </>
      )}
      <ExampleChip to={explorerPaths.validators}>who runs the chain</ExampleChip>
    </div>
  );
}

export function Hero() {
  const { open } = usePalette();
  return (
    <div className="pt-6 text-center">
      <h1 className="mb-2.5 font-display text-[clamp(1.75rem,5vw,2.5rem)] font-bold leading-[1.1] tracking-tight">
        Follow any transaction on Orama.
      </h1>
      <p className="mx-auto mb-5 max-w-[56ch] text-[15.5px] text-muted">
        Paste a wallet, transaction or block number. We'll tell you what happened, in plain English.
      </p>
      <button
        type="button"
        onClick={open}
        className={cn(
          "mx-auto flex w-full max-w-[680px] cursor-pointer items-center gap-3 rounded-2xl border border-border bg-surface px-4 py-3.5 text-left text-[15px] text-muted shadow-xl transition-colors hover:border-fg/40 sm:py-4",
        )}
      >
        <Search size={18} aria-hidden="true" className="shrink-0" />
        <span className="min-w-0 flex-1 truncate">{SEARCH_PLACEHOLDER}</span>
        <kbd className="hidden rounded border border-border bg-surface-2 px-1.5 font-mono text-[11px] sm:block">⌘K</kbd>
      </button>
      <TryRow />
    </div>
  );
}
