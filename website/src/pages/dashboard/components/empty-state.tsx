import { DashedPanel } from "../../../components/ui/dashed-panel";
import { Button } from "../../../components/ui/button";
import type { LucideIcon } from "lucide-react";

interface EmptyStateProps {
  icon: LucideIcon;
  title: string;
  description: string;
  actionLabel?: string;
  onAction?: () => void;
}

export function EmptyState({ icon: Icon, title, description, actionLabel, onAction }: EmptyStateProps) {
  return (
    <DashedPanel withCorners className="p-8 sm:p-12">
      <div className="flex flex-col items-center gap-4 text-center">
        <Icon size={32} className="text-muted/50" />
        <h3 className="font-display font-bold text-lg text-fg">{title}</h3>
        <p className="text-sm text-muted max-w-sm">{description}</p>
        {actionLabel && onAction && (
          <Button size="sm" onClick={onAction}>{actionLabel}</Button>
        )}
      </div>
    </DashedPanel>
  );
}
