import { FACTS } from "../../content/facts";
import { cn } from "../../lib/utils";

export function FactStrip({ className }: { className?: string }) {
  return (
    <dl
      className={cn(
        "grid grid-cols-2 md:grid-cols-4 border border-dashed border-border",
        className,
      )}
    >
      {FACTS.map((fact, i) => (
        <div
          key={fact.label}
          className={cn(
            "flex flex-col items-center gap-1 px-4 py-6 sm:py-8 text-center border-dashed border-border",
            i % 2 === 1 && "border-l",
            i >= 2 && "border-t md:border-t-0",
            i === 2 && "md:border-l",
          )}
        >
          <dt className="sr-only">{fact.label}</dt>
          <dd className="font-display font-bold text-3xl sm:text-4xl text-fg tabular-nums">
            {fact.value}
          </dd>
          <dd className="font-mono text-[11px] tracking-wider uppercase text-muted">
            {fact.label}
          </dd>
        </div>
      ))}
    </dl>
  );
}
