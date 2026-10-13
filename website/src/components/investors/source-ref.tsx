import { sourceLabel, sourceNumber } from "../../content/investors/sources";

/** A citation: [n], linking to the numbered source list at the page bottom. */
export function SourceRef({ ids }: { ids: readonly string[] }) {
  return (
    <sup className="ml-0.5 font-mono text-[10px] font-normal text-muted">
      {ids.map((id) => {
        const n = sourceNumber(id);
        return (
          <a key={id} href={`#src-${n}`} aria-label={`Source ${n}: ${sourceLabel(id)}`} className="hover:text-fg transition-colors">
            [{n}]
          </a>
        );
      })}
    </sup>
  );
}
