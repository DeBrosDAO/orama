import { SERVICES } from "../../content/services";
import { cn } from "../../lib/utils";
import { StatusBadge } from "../ui/status-badge";

export interface ServiceGridProps {
  /** Icon + name only, for the home page overview. */
  compact?: boolean;
}

export function ServiceGrid({ compact = false }: ServiceGridProps) {
  if (compact) {
    return (
      <ul className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-4 gap-px bg-border border border-border">
        {SERVICES.map(({ id, icon: Icon, name, insteadOf }) => (
          <li key={id} className="flex flex-col items-start gap-3 p-5 bg-surface group">
            <Icon size={22} strokeWidth={1.5} className="text-fg transition-transform duration-300 group-hover:scale-110" />
            <span className="font-display font-semibold text-sm sm:text-base text-fg">{name}</span>
            <span className="font-mono text-[10px] tracking-wider uppercase text-muted">
              {insteadOf ? `vs ${insteadOf}` : "No big-cloud equivalent"}
            </span>
          </li>
        ))}
        <li className="flex flex-col items-start justify-center gap-2 p-5 bg-surface">
          <span className="font-display font-bold text-3xl text-fg">{SERVICES.length}</span>
          <span className="font-mono text-[10px] tracking-wider uppercase text-muted">services, all live</span>
        </li>
      </ul>
    );
  }

  return (
    <ul className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
      {SERVICES.map(({ id, icon: Icon, name, line, insteadOf }) => (
        <li
          key={id}
          id={id}
          className={cn(
            "flex flex-col gap-4 p-5 sm:p-6 border border-dashed border-border bg-surface/60",
            "transition-colors hover:border-fg/25 hover:bg-white/[0.02]",
          )}
        >
          <div className="flex items-center gap-3">
            <span className="flex items-center justify-center w-11 h-11 shrink-0 border border-border rounded-sm">
              <Icon size={20} strokeWidth={1.5} className="text-fg" />
            </span>
            <h3 className="flex-1 min-w-0 font-display font-semibold text-lg text-fg leading-tight">{name}</h3>
            <StatusBadge tone="now">Live</StatusBadge>
          </div>
          <p className="text-sm text-muted flex-1">{line}</p>
          <span className="font-mono text-[10px] tracking-wider uppercase text-accent pt-3 border-t border-dashed border-border">
            {insteadOf ? `Instead of ${insteadOf}` : "No big-cloud equivalent"}
          </span>
        </li>
      ))}
      <li className="flex flex-col justify-center gap-3 p-6 border border-fg/20 bg-white/[0.02]">
        <span className="font-display font-bold text-5xl text-fg">{SERVICES.length}</span>
        <p className="font-display font-semibold text-lg text-fg">services. One address. One login.</p>
        <p className="text-sm text-muted">Your app reaches all of them through a single gateway.</p>
      </li>
    </ul>
  );
}
