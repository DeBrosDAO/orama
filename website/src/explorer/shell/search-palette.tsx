import { useCallback, useId, useRef, useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { Search } from "lucide-react";
import { DETECTED, optionId } from "./palette-model";
import { PaletteList } from "./palette-rows";
import { usePaletteController } from "./use-palette-controller";
import { usePaletteSearch } from "./use-palette-search";
import type { PaletteSearch } from "./use-palette-search";

const SHIELDED_TEXT = "Shielded addresses are private by design. There is nothing to look up for them.";

/** The one polite live message: shielded, error or nothing found. Always mounted so changes are announced. */
function PaletteStatus({ search }: { search: PaletteSearch }) {
  const { trimmed, hit, rows, pending, error } = search;
  const nothing = trimmed !== "" && rows.length === 0 && !pending && hit === null && error === null;
  return (
    <div role="status">
      {hit?.kind === "shielded-address" && <p className="px-3 py-3 text-sm text-muted">{SHIELDED_TEXT}</p>}
      {error && <p className="px-3 py-3 text-sm text-loss">Name search failed: {error.message}</p>}
      {nothing && (
        <p className="px-3 py-6 text-sm text-muted">
          Nothing matches “{trimmed}”. Try a full wallet address, a 64-character transaction hash, a block number, or a name such as val-3.
        </p>
      )}
    </div>
  );
}

interface PaletteInputProps {
  inputRef: React.RefObject<HTMLInputElement | null>;
  listId: string;
  value: string;
  onChange: (value: string) => void;
  onKeyDown: (e: React.KeyboardEvent) => void;
  /** Index of the highlighted option, or null when there are no options. */
  activeIndex: number | null;
}

function PaletteInput({ inputRef, listId, value, onChange, onKeyDown, activeIndex }: PaletteInputProps) {
  return (
    <div className="flex items-center gap-3 border-b border-border px-4">
      <Search size={18} className="shrink-0 text-muted" aria-hidden="true" />
      <input
        ref={inputRef}
        role="combobox"
        aria-expanded={activeIndex !== null}
        aria-controls={listId}
        aria-autocomplete="list"
        aria-activedescendant={activeIndex !== null ? optionId(listId, activeIndex) : undefined}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={onKeyDown}
        placeholder="Paste an address, transaction hash or block number…"
        aria-label="Search"
        autoComplete="off"
        spellCheck={false}
        className="w-full bg-transparent py-4 text-[15px] text-fg focus-visible:outline-none! placeholder:text-muted/70"
      />
    </div>
  );
}

export function SearchPalette({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  const listId = useId();
  const inputRef = useRef<HTMLInputElement>(null);
  const [query, setQuery] = useState("");
  const clearQuery = useCallback(() => setQuery(""), []);
  const search = usePaletteSearch(query);
  const { trimmed, hit, rows } = search;
  const { selected, setSelected, go, onKeyDown } = usePaletteController(open, onOpenChange, rows, trimmed, clearQuery);

  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-[70] bg-black/70 backdrop-blur-sm" />
        <Dialog.Content
          aria-describedby={undefined}
          onOpenAutoFocus={(e) => {
            e.preventDefault();
            inputRef.current?.focus();
          }}
          className="explorer fixed left-1/2 top-[12vh] z-[71] w-[min(640px,calc(100vw-24px))] -translate-x-1/2 overflow-hidden rounded-2xl border border-fg/20 bg-[#0e0e11] shadow-2xl"
        >
          <Dialog.Title className="sr-only">Search the explorer</Dialog.Title>
          <PaletteInput
            inputRef={inputRef}
            listId={listId}
            value={query}
            onChange={setQuery}
            onKeyDown={onKeyDown}
            activeIndex={rows.length > 0 ? selected : null}
          />
          <div className="max-h-[52vh] overflow-y-auto p-2">
            {hit && <div className="px-3 pb-1 pt-2 text-xs text-muted">{DETECTED[hit.kind]}</div>}
            {!trimmed && <div className="px-3 pb-1 pt-2 text-xs text-muted">Jump to</div>}
            <PaletteList listId={listId} rows={rows} selected={selected} onSelect={setSelected} onChoose={go} />
            <PaletteStatus search={search} />
          </div>
          <div className="flex gap-4 border-t border-border px-4 py-2 text-xs text-muted/80">
            <span>↑ ↓ move</span>
            <span>↵ open</span>
            <span>esc close</span>
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
