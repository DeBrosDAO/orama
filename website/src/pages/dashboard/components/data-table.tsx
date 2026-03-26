import { cn } from "../../../lib/utils";
import { DashedPanel } from "../../../components/ui/dashed-panel";
import type { ReactNode } from "react";

export interface Column<T> {
  key: string;
  label: string;
  align?: "left" | "right";
  render: (item: T) => ReactNode;
}

interface DataTableProps<T> {
  columns: Column<T>[];
  data: T[];
  keyExtractor: (item: T) => string;
}

export function DataTable<T>({ columns, data, keyExtractor }: DataTableProps<T>) {
  return (
    <DashedPanel className="p-4 sm:p-6">
      {/* Header */}
      <div className={cn("hidden sm:grid gap-4 pb-3 border-b border-dashed border-border")} style={{ gridTemplateColumns: `repeat(${columns.length}, minmax(0, 1fr))` }}>
        {columns.map((col) => (
          <span
            key={col.key}
            className={cn(
              "font-mono text-xs text-muted uppercase tracking-wider",
              col.align === "right" && "text-right",
            )}
          >
            {col.label}
          </span>
        ))}
      </div>

      {/* Rows */}
      {data.map((item, i) => (
        <div
          key={keyExtractor(item)}
          className={cn(
            "grid grid-cols-1 sm:gap-4 gap-2 py-3",
            i < data.length - 1 && "border-b border-dashed border-border",
          )}
          style={{ gridTemplateColumns: undefined }}
        >
          <div
            className="hidden sm:grid gap-4"
            style={{ gridTemplateColumns: `repeat(${columns.length}, minmax(0, 1fr))` }}
          >
            {columns.map((col) => (
              <div key={col.key} className={cn(col.align === "right" && "text-right")}>
                {col.render(item)}
              </div>
            ))}
          </div>
          {/* Mobile: stack */}
          <div className="flex flex-col gap-1 sm:hidden">
            {columns.map((col) => (
              <div key={col.key}>{col.render(item)}</div>
            ))}
          </div>
        </div>
      ))}

      {data.length === 0 && (
        <div className="py-8 text-center">
          <span className="font-mono text-xs text-muted">No data</span>
        </div>
      )}
    </DashedPanel>
  );
}
