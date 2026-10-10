import { useEffect, useState } from "react";
import type { KeyboardEvent } from "react";
import { useNavigate } from "react-router";
import { moveSelection } from "./palette-model";
import type { PaletteRow } from "./palette-model";

/** Selection, arrow-key movement and "go to the chosen row" for the palette; forgets the query on close. */
export function usePaletteController(
  open: boolean,
  onOpenChange: (open: boolean) => void,
  rows: readonly PaletteRow[],
  resetKey: string,
  clearQuery: () => void,
) {
  const navigate = useNavigate();
  const [selected, setSelected] = useState(0);

  useEffect(() => setSelected(0), [rows.length, resetKey]);
  useEffect(() => {
    if (!open) clearQuery();
  }, [open, clearQuery]);

  function go(row: PaletteRow | undefined) {
    if (!row) return;
    onOpenChange(false);
    navigate(row.to);
  }

  function onKeyDown(e: KeyboardEvent) {
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      setSelected((i) => moveSelection(i, e.key === "ArrowDown" ? 1 : -1, rows.length));
    } else if (e.key === "Enter") {
      e.preventDefault();
      go(rows[selected]);
    }
  }

  return { selected, setSelected, go, onKeyDown };
}
