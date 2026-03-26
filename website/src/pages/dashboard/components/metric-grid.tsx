import { DashedPanel } from "../../../components/ui/dashed-panel";
import { MetricCard } from "../../../components/ui/metric-card";
import { cn } from "../../../lib/utils";

interface Metric {
  label: string;
  value: string;
}

interface MetricGridProps {
  metrics: Metric[];
  className?: string;
}

export function MetricGrid({ metrics, className }: MetricGridProps) {
  const cols = metrics.length <= 2 ? "grid-cols-2" : metrics.length === 3 ? "grid-cols-2 sm:grid-cols-3" : "grid-cols-2 sm:grid-cols-4";
  return (
    <div className={cn("grid gap-4", cols, className)}>
      {metrics.map((m) => (
        <DashedPanel key={m.label} className="p-4">
          <MetricCard label={m.label} value={m.value} />
        </DashedPanel>
      ))}
    </div>
  );
}
