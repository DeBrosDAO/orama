import { Link } from "react-router";
import { ChevronLeft, ChevronRight } from "lucide-react";
import { explorerPaths } from "../../model/routes";
import { formatInt, shortHash } from "../../model/units";
import { cn } from "../../../lib/utils";
import { CopyChip } from "../../ui/copy";
import type { BlockNeighbours } from "./block-nav";

const STEP = "inline-flex items-center gap-1 rounded-lg border border-border bg-surface-2 px-2.5 py-1.5 text-sm";
const ICON_SIZE = 16;

function Step({ to, label, children }: { to: number | null; label: string; children: React.ReactNode }) {
  if (to === null) {
    return (
      <button type="button" disabled aria-label={label} className={cn(STEP, "cursor-not-allowed opacity-40")}>
        {children}
      </button>
    );
  }
  return (
    <Link to={explorerPaths.block(to)} aria-label={label} className={cn(STEP, "hover:border-fg/30")}>
      {children}
    </Link>
  );
}

export interface BlockHeaderProps {
  height: number;
  hash: string;
  nav: BlockNeighbours;
}

/** "Block 1,284,410", with buttons to step to the blocks either side. */
export function BlockHeader({ height, hash, nav }: BlockHeaderProps) {
  return (
    <header className="flex flex-wrap items-center gap-x-4 gap-y-2">
      <div className="min-w-0 flex-1 basis-64">
        <h1 className="font-display text-2xl font-semibold tracking-tight">Block {formatInt(height)}</h1>
        <div className="mt-1 flex flex-wrap items-center gap-2 text-[13px] text-muted">
          <span>Hash</span>
          <span className="font-mono" title={hash}>{shortHash(hash)}</span>
          <CopyChip value={hash} label="Copy block hash" />
        </div>
      </div>
      <nav aria-label="Neighbouring blocks" className="flex gap-2">
        <Step to={nav.prev} label="Previous block">
          <ChevronLeft size={ICON_SIZE} aria-hidden="true" />
          Previous
        </Step>
        <Step to={nav.next} label="Next block">
          Next
          <ChevronRight size={ICON_SIZE} aria-hidden="true" />
        </Step>
      </nav>
    </header>
  );
}
