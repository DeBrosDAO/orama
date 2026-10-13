import { Check, Flag } from "lucide-react";
import { MILESTONES } from "../../content/roadmap";
import type { MilestoneState } from "../../content/roadmap";
import { cn } from "../../lib/utils";
import { StatusBadge } from "../ui/status-badge";
import type { BadgeTone } from "../ui/status-badge";

const BADGE: Record<MilestoneState, { tone: BadgeTone; text: string }> = {
  done: { tone: "now", text: "Done" },
  next: { tone: "future", text: "Next" },
  later: { tone: "future", text: "Planned" },
};

export interface RoadmapTrackProps {
  /** Show the bullet points under each stop (full roadmap page). */
  detailed?: boolean;
}

/**
 * The roadmap as a track: a rail with a stop per milestone, horizontal on
 * wide screens and vertical on phones, ending at the destination.
 */
export function RoadmapTrack({ detailed = false }: RoadmapTrackProps) {
  return (
    <div className="relative">
      {/* The rail. */}
      <div
        aria-hidden="true"
        className="absolute left-[15px] top-4 bottom-4 w-px md:left-4 md:right-4 md:top-[15px] md:bottom-auto md:h-px md:w-auto border-l md:border-l-0 md:border-t border-dashed border-border"
      />
      <ol className="relative grid grid-cols-1 md:grid-cols-4 gap-8 md:gap-4">
      {MILESTONES.map((m) => {
        const done = m.state === "done";
        return (
          <li key={m.id} className="relative flex md:flex-col gap-4 md:gap-5 pl-0">
            <span
              className={cn(
                "relative z-10 flex items-center justify-center w-8 h-8 shrink-0 rounded-full border font-mono text-[11px]",
                done
                  ? "bg-fg text-bg border-fg"
                  : "bg-bg border-dashed border-signal/60 text-signal",
              )}
            >
              {done ? <Check size={14} strokeWidth={3} /> : m.step}
            </span>
            <div className="flex flex-col gap-2 min-w-0">
              <StatusBadge tone={BADGE[m.state].tone} className="self-start">
                {BADGE[m.state].text}
              </StatusBadge>
              <h3 className="font-display font-semibold text-fg text-lg leading-tight">
                {m.title}
              </h3>
              <p className="text-sm text-muted">{m.line}</p>
              {detailed && (
                <ul className="mt-1 flex flex-col gap-1.5">
                  {m.points.map((p) => (
                    <li key={p} className="flex items-start gap-2 text-sm text-accent">
                      <span className={cn("mt-2 w-1 h-1 rounded-full shrink-0", done ? "bg-fg" : "bg-signal")} />
                      {p}
                    </li>
                  ))}
                </ul>
              )}
            </div>
          </li>
        );
      })}
      </ol>
    </div>
  );
}

export function RoadmapDestination() {
  return (
    <div className="flex flex-col items-center gap-3 text-center">
      <span className="flex items-center justify-center w-12 h-12 rounded-full border border-dashed border-fg/30">
        <Flag size={18} className="text-fg" />
      </span>
      <p className="font-display font-semibold text-fg text-xl">A cloud owned by people.</p>
      <p className="text-sm text-muted max-w-md">
        Where the three steps lead: anyone can run part of the network, and anyone can build on it.
      </p>
    </div>
  );
}
