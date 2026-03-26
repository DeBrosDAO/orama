import type { ReactNode } from "react";
import { cn } from "../../lib/utils";

function CornerMarker({
  position,
}: {
  position: "top-left" | "top-right" | "bottom-left" | "bottom-right";
}) {
  const positionClasses = {
    "top-left": "-top-1 -left-1",
    "top-right": "-top-1 -right-1",
    "bottom-left": "-bottom-1 -left-1",
    "bottom-right": "-bottom-1 -right-1",
  };

  return (
    <div className={cn("absolute w-2.5 h-2.5", positionClasses[position])}>
      <div className="absolute top-1/2 left-0 w-full h-px bg-accent/30" />
      <div className="absolute left-1/2 top-0 h-full w-px bg-accent/30" />
    </div>
  );
}

export interface CodeBlockProps {
  children: ReactNode;
  label?: string;
  className?: string;
}

export function CodeBlock({ children, label, className }: CodeBlockProps) {
  return (
    <div className={cn("flex flex-col gap-3", className)}>
      <div className="relative border border-dashed border-border p-6">
        <CornerMarker position="top-left" />
        <CornerMarker position="top-right" />
        <CornerMarker position="bottom-left" />
        <CornerMarker position="bottom-right" />

        <pre className="font-mono text-sm text-accent leading-relaxed overflow-x-auto">
          {children}
        </pre>
      </div>

      {label && (
        <p className="text-xs font-mono text-muted text-center tracking-wider uppercase">
          {label}
        </p>
      )}
    </div>
  );
}
