import { useEffect } from "react";
import type { ReactNode } from "react";
import { ArrowRight, ArrowLeftRight, Hash, Layers, Users, Wallet } from "lucide-react";
import { Identicon } from "../ui/identicon";
import { cn } from "../../lib/utils";
import { optionId } from "./palette-model";
import type { PaletteRow } from "./palette-model";

const ICON_SIZE = 16;
const NAMED_ICON_SIZE = 20;

function iconFor(row: PaletteRow): ReactNode {
  switch (row.icon) {
    case "wallet":
      return <Wallet size={ICON_SIZE} />;
    case "tx":
      return <ArrowLeftRight size={ICON_SIZE} />;
    case "hash":
      return <Hash size={ICON_SIZE} />;
    case "block":
      return <Layers size={ICON_SIZE} />;
    case "validators":
      return <Users size={ICON_SIZE} />;
    case "named":
      return <Identicon seed={row.seed ?? row.to} size={NAMED_ICON_SIZE} />;
  }
}

interface OptionProps {
  row: PaletteRow;
  id: string;
  selected: boolean;
  onHover: () => void;
  onChoose: () => void;
}

function Option({ row, id, selected, onHover, onChoose }: OptionProps) {
  useEffect(() => {
    if (selected) document.getElementById(id)?.scrollIntoView({ block: "nearest" });
  }, [selected, id]);
  return (
    <div
      role="option"
      id={id}
      aria-selected={selected}
      onMouseEnter={onHover}
      onClick={onChoose}
      className={cn("flex w-full items-center gap-3 rounded-lg px-3 py-2.5 text-left text-sm cursor-pointer", selected ? "bg-surface-3" : "hover:bg-surface-3/60")}
    >
      <span aria-hidden="true" className="grid h-8 w-8 shrink-0 place-items-center rounded-lg bg-surface-3 text-muted">
        {iconFor(row)}
      </span>
      <span className="min-w-0 flex-1 truncate">
        {row.text}
        {row.code && <span className="ml-1.5 font-mono text-[0.92em] text-muted">{row.code}</span>}
      </span>
      <span className="shrink-0 text-xs text-muted">{row.hint}</span>
      <ArrowRight size={14} className="shrink-0 text-muted/60" aria-hidden="true" />
    </div>
  );
}

export interface PaletteListProps {
  listId: string;
  rows: readonly PaletteRow[];
  selected: number;
  onSelect: (index: number) => void;
  onChoose: (row: PaletteRow) => void;
}

/** The results as an ARIA listbox; the search input points at the selected option. */
export function PaletteList({ listId, rows, selected, onSelect, onChoose }: PaletteListProps) {
  return (
    <div role="listbox" id={listId} aria-label="Search results">
      {rows.map((row, i) => (
        <Option
          key={row.key}
          row={row}
          id={optionId(listId, i)}
          selected={i === selected}
          onHover={() => onSelect(i)}
          onChoose={() => onChoose(row)}
        />
      ))}
    </div>
  );
}
