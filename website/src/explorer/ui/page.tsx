import type { ReactNode } from "react";
import { cn } from "../../lib/utils";

/** The centred content column every explorer page sits in. */
export function Page({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={cn("mx-auto w-full max-w-[1140px] space-y-4 px-4 py-6 sm:px-6", className)}>{children}</div>;
}
