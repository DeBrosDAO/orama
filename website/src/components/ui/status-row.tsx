import { cn } from "../../lib/utils";
import { StatusDot } from "./status-dot";
import { Badge } from "./badge";

export interface StatusRowProps {
  name: string;
  type: string;
  description: string;
  status?: "active" | "warning" | "error" | "neutral";
  className?: string;
}

export function StatusRow({
  name,
  type,
  description,
  status = "neutral",
  className,
}: StatusRowProps) {
  return (
    <div
      className={cn(
        "flex flex-col gap-2 py-3 sm:flex-row sm:items-center sm:gap-4",
        className,
      )}
    >
      <div className="flex items-center gap-3 sm:min-w-48">
        <StatusDot status={status} />
        <span className="font-display font-semibold text-fg">{name}</span>
      </div>

      <Badge variant="default">{type}</Badge>

      <span className="text-sm text-muted">{description}</span>
    </div>
  );
}
