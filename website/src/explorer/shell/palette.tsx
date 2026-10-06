import { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react";
import type { ReactNode } from "react";
import { isTypingTarget } from "../ui/dom";
import { SearchPalette } from "./search-palette";

interface PaletteContextValue {
  open: () => void;
}

const PaletteContext = createContext<PaletteContextValue | null>(null);

export function usePalette(): PaletteContextValue {
  const ctx = useContext(PaletteContext);
  if (!ctx) throw new Error("usePalette must be used inside <PaletteProvider>");
  return ctx;
}

export function PaletteProvider({ children }: { children: ReactNode }) {
  const [isOpen, setOpen] = useState(false);
  const open = useCallback(() => setOpen(true), []);

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      const cmdK = e.key.toLowerCase() === "k" && (e.metaKey || e.ctrlKey);
      if (cmdK || (e.key === "/" && !isTypingTarget(e.target))) {
        e.preventDefault();
        setOpen(true);
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  const value = useMemo(() => ({ open }), [open]);
  return (
    <PaletteContext.Provider value={value}>
      {children}
      <SearchPalette open={isOpen} onOpenChange={setOpen} />
    </PaletteContext.Provider>
  );
}
