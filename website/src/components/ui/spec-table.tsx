import { cn } from "../../lib/utils";

export interface SpecTableRow {
  label: string;
  value: React.ReactNode;
}

export interface SpecTableProps {
  rows: SpecTableRow[];
  className?: string;
}

export function SpecTable({ rows, className }: SpecTableProps) {
  return (
    <div className={cn("border border-dashed border-border", className)}>
      {rows.map((row, i) => (
        <div
          key={i}
          className={cn(
            "flex items-center justify-between px-4 py-3",
            i < rows.length - 1 && "border-b border-dashed border-border",
          )}
        >
          <span className="font-mono text-sm text-muted">{row.label}</span>
          <span className="text-sm text-fg">{row.value}</span>
        </div>
      ))}
    </div>
  );
}
