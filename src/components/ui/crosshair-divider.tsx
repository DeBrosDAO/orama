import { cn } from "../../lib/utils";

export interface CrosshairDividerProps {
  className?: string;
}

export function CrosshairDivider({ className }: CrosshairDividerProps) {
  return (
    <div className={cn("flex items-center gap-4", className)}>
      <div className="flex-1 border-t border-dashed border-border" />
      <svg
        width="16"
        height="16"
        viewBox="0 0 16 16"
        fill="none"
        className="shrink-0 text-accent/50"
      >
        <line
          x1="8"
          y1="0"
          x2="8"
          y2="16"
          stroke="currentColor"
          strokeWidth="1"
          strokeDasharray="2 2"
        />
        <line
          x1="0"
          y1="8"
          x2="16"
          y2="8"
          stroke="currentColor"
          strokeWidth="1"
          strokeDasharray="2 2"
        />
        <circle cx="8" cy="8" r="2.5" stroke="currentColor" strokeWidth="1" />
      </svg>
      <div className="flex-1 border-t border-dashed border-border" />
    </div>
  );
}
