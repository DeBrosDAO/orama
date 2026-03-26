import type { ReactNode } from "react";
import { cn } from "../../lib/utils";

interface CornerMarkerProps {
  position: "top-left" | "top-right" | "bottom-left" | "bottom-right";
}

function CornerMarker({ position }: CornerMarkerProps) {
  const positionClasses = {
    "top-left": "-top-px -left-px",
    "top-right": "-top-px -right-px",
    "bottom-left": "-bottom-px -left-px",
    "bottom-right": "-bottom-px -right-px",
  };

  return (
    <div className={cn("absolute w-3 h-3", positionClasses[position])}>
      <div className="absolute top-1/2 left-0 w-full h-px bg-accent/30" />
      <div className="absolute left-1/2 top-0 h-full w-px bg-accent/30" />
    </div>
  );
}

export interface DashedPanelProps {
  className?: string;
  children: ReactNode;
  withBackground?: boolean;
  withCorners?: boolean;
}

export function DashedPanel({
  className,
  children,
  withBackground = false,
  withCorners = false,
}: DashedPanelProps) {
  return (
    <div
      className={cn(
        "relative border border-dashed border-border",
        withBackground && "bg-bg/50 p-6 sm:p-8",
        className,
      )}
    >
      {withCorners && (
        <>
          <CornerMarker position="top-left" />
          <CornerMarker position="top-right" />
          <CornerMarker position="bottom-left" />
          <CornerMarker position="bottom-right" />
        </>
      )}
      {children}
    </div>
  );
}
