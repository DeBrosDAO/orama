import type { ReactNode } from "react";
import { cn } from "../../lib/utils";

const TH = "px-3 py-2 font-medium";
const TD = "px-3 py-2";

export interface Column<T> {
  header: string;
  cell: (row: T) => ReactNode;
  align?: "right";
  /** Smaller type and muted colour, for a column of identifiers. */
  quiet?: boolean;
}

export interface DataTableProps<T> {
  caption: string;
  columns: readonly Column<T>[];
  rows: readonly T[];
  rowKey: (row: T) => string;
  minWidth?: number;
}

/** A plain table of one list: a header row, one row per entry. */
export function DataTable<T>({ caption, columns, rows, rowKey, minWidth = 560 }: DataTableProps<T>) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-left text-sm" style={{ minWidth }}>
        <caption className="sr-only">{caption}</caption>
        <thead className="text-xs text-muted">
          <tr>
            {columns.map((c) => (
              <th key={c.header} scope="col" className={cn(TH, c.align === "right" && "text-right")}>
                {c.header}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr key={rowKey(row)} className="border-t border-border hover:bg-surface-2/60">
              {columns.map((c) => (
                <td key={c.header} className={cn(TD, c.align === "right" && "text-right", c.quiet && "text-muted")}>
                  {c.cell(row)}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
