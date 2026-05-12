import type { ReactNode } from "react";
import { cn } from "../../lib/utils";

const paddingVariants = {
  default: "py-16 sm:py-24",
  narrow: "py-8 sm:py-12",
  wide: "py-12 sm:py-24 lg:py-32",
  none: "py-0",
} as const;

export interface SectionProps {
  id?: string;
  className?: string;
  children: ReactNode;
  padding?: keyof typeof paddingVariants;
}

export function Section({
  id,
  className,
  children,
  padding = "default",
}: SectionProps) {
  return (
    <section
      id={id}
      className={cn(
        "max-w-6xl mx-auto px-4 sm:px-6 lg:px-8",
        paddingVariants[padding],
        className,
      )}
    >
      {children}
    </section>
  );
}
