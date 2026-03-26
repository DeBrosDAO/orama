import { cn } from "../../lib/utils";

export interface MetricCardProps {
  label: string;
  value: string;
  className?: string;
}

export function MetricCard({ label, value, className }: MetricCardProps) {
  return (
    <div className={cn("flex flex-col gap-1", className)}>
      <span className="text-xs font-mono text-muted tracking-wider uppercase">
        {label}
      </span>
      <span className="text-2xl font-bold text-accent tabular-nums tracking-tight">{value}</span>
    </div>
  );
}
