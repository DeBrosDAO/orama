import type { ReactNode } from "react";
import { Section } from "../layout/section";
import { DashedPanel } from "../ui/dashed-panel";

export interface CtaBandProps {
  title: string;
  line?: string;
  children: ReactNode;
}

export function CtaBand({ title, line, children }: CtaBandProps) {
  return (
    <Section padding="wide">
      <DashedPanel withBackground withCorners className="text-center">
        <div className="flex flex-col items-center gap-5 py-6">
          <h2 className="font-display font-bold text-3xl md:text-4xl text-fg tracking-tight text-balance">
            {title}
          </h2>
          {line && <p className="text-sm sm:text-base text-muted max-w-lg">{line}</p>}
          <div className="flex flex-col sm:flex-row w-full sm:w-auto gap-3 justify-center pt-2">
            {children}
          </div>
        </div>
      </DashedPanel>
    </Section>
  );
}
