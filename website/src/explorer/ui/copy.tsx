import { useEffect, useState } from "react";
import { Check, Copy } from "lucide-react";
import { cn } from "../../lib/utils";

const RESET_MS = 1600;
type CopyState = "idle" | "copied" | "failed";
/** Spoken when the state changes; the live span stays mounted so the change is announced. */
const ANNOUNCEMENT: Record<CopyState, string> = { idle: "", copied: "Copied", failed: "Copy failed. Select the text and copy it." };

export interface CopyButtonProps {
  value: string;
  label: string;
  className?: string;
  /** Show the word next to the icon. */
  withText?: boolean;
}

/** Copies `value`. Says so, and says so honestly when the browser refuses. */
export function CopyChip({ value, label, className, withText = false }: CopyButtonProps) {
  const [state, setState] = useState<CopyState>("idle");

  useEffect(() => {
    if (state === "idle") return;
    const t = window.setTimeout(() => setState("idle"), RESET_MS);
    return () => window.clearTimeout(t);
  }, [state]);

  async function copy() {
    try {
      await navigator.clipboard.writeText(value);
      setState("copied");
    } catch {
      setState("failed");
    }
  }

  return (
    <button
      type="button"
      onClick={copy}
      aria-label={label}
      className={cn(
        "inline-flex items-center gap-1.5 rounded-full border border-border bg-surface-2 px-2 py-0.5 text-xs text-muted hover:text-fg hover:border-fg/30 transition-colors cursor-pointer",
        className,
      )}
    >
      {state === "copied" ? <Check size={12} /> : <Copy size={12} />}
      {(withText || state !== "idle") && <span aria-hidden="true">{state === "copied" ? "Copied" : state === "failed" ? "Select and copy" : "Copy"}</span>}
      <span className="sr-only" aria-live="polite">
        {ANNOUNCEMENT[state]}
      </span>
    </button>
  );
}
