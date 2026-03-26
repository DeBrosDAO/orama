import type { ReactNode } from "react";
import { cn } from "../../lib/utils";

export interface ContainerProps {
  className?: string;
  children: ReactNode;
}

export function Container({ className, children }: ContainerProps) {
  return (
    <div className={cn("max-w-6xl mx-auto px-4 sm:px-6 lg:px-8", className)}>
      {children}
    </div>
  );
}
