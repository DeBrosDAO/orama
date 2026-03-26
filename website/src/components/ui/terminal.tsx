import { cn } from "../../lib/utils";

export interface TerminalLine {
  prefix?: string;
  text: string;
  className?: string;
}

export interface TerminalProps {
  lines: TerminalLine[];
  className?: string;
}

export function Terminal({ lines, className }: TerminalProps) {
  return (
    <div
      className={cn(
        "border border-border bg-surface overflow-hidden rounded-sm",
        "shadow-[0_0_24px_rgba(0,212,170,0.06)]",
        className,
      )}
    >
      {/* Title bar */}
      <div className="flex items-center gap-2 px-4 py-2.5 border-b border-border">
        <div className="flex items-center gap-1.5">
          <span className="w-2.5 h-2.5 rounded-full bg-red-500" />
          <span className="w-2.5 h-2.5 rounded-full bg-amber-500" />
          <span className="w-2.5 h-2.5 rounded-full bg-green-500" />
        </div>
        <span className="text-xs font-mono text-muted ml-2">terminal</span>
      </div>

      {/* Body */}
      <div className="p-4 font-mono text-sm leading-relaxed space-y-1 overflow-x-auto">
        {lines.map((line, i) => (
          <div key={i} className={cn("flex gap-2", line.className)}>
            {line.prefix && (
              <span
                className={cn(
                  "shrink-0 select-none",
                  line.prefix === "$" ? "text-fg" : "text-accent-2",
                )}
              >
                {line.prefix}
              </span>
            )}
            <span className="text-muted">{line.text}</span>
          </div>
        ))}
      </div>
    </div>
  );
}
