import { Page } from "../components/layout/page";
import { Section } from "../components/layout/section";
import { DashedPanel } from "../components/ui/dashed-panel";
import { Badge } from "../components/ui/badge";
import { CrosshairDivider } from "../components/ui/crosshair-divider";
import { CHANGELOG } from "../data/changelog";
import { cn } from "../lib/utils";

const TYPE_PREFIX: Record<string, { symbol: string; color: string }> = {
  added: { symbol: "+", color: "text-accent-2" },
  changed: { symbol: "~", color: "text-amber-500" },
  fixed: { symbol: "\u2022", color: "text-accent" },
  removed: { symbol: "-", color: "text-red-500" },
};

export default function Changelog() {
  return (
    <Page title="Changelog">
      {/* Hero */}
      <Section padding="wide">
        <div className="flex flex-col gap-6 max-w-2xl">
          <Badge variant="default" className="w-fit">
            RELEASES
          </Badge>
          <h1 className="font-display font-bold text-4xl lg:text-5xl leading-tight text-fg">
            Changelog
          </h1>
          <p className="text-muted text-lg leading-relaxed">
            Track every update to the Orama ecosystem.
          </p>
        </div>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* Entries */}
      <Section>
        <div className="flex flex-col gap-6">
          {CHANGELOG.map((entry) => (
            <DashedPanel key={entry.version} className="p-6 sm:p-8">
              <div className="flex flex-col gap-4">
                {/* Header */}
                <div className="flex flex-col gap-1 sm:flex-row sm:items-center sm:justify-between">
                  <span className="font-display font-bold text-fg text-lg">
                    v{entry.version}
                  </span>
                  <span className="font-mono text-xs text-muted">
                    {entry.date}
                  </span>
                </div>

                {/* Summary */}
                <p className="text-muted text-sm">{entry.summary}</p>

                {/* Changes */}
                <ul className="flex flex-col gap-2 pt-2">
                  {entry.changes.map((change, i) => {
                    const prefix = TYPE_PREFIX[change.type];
                    return (
                      <li key={i} className="flex items-start gap-2">
                        <span
                          className={cn(
                            "font-mono shrink-0 w-4 text-center",
                            prefix.color,
                          )}
                        >
                          {prefix.symbol}
                        </span>
                        <span className="text-muted text-sm">
                          {change.text}
                        </span>
                      </li>
                    );
                  })}
                </ul>
              </div>
            </DashedPanel>
          ))}
        </div>
      </Section>
    </Page>
  );
}
