import { useEffect, useState } from "react";
import { Check, Copy } from "lucide-react";
import { cn } from "../../lib/utils";

const COPIED_RESET_MS = 1800;

export interface CopyButtonProps {
  value: string;
  label: string;
  className?: string;
}

export function CopyButton({ value, label, className }: CopyButtonProps) {
  const [state, setState] = useState<"idle" | "copied" | "failed">("idle");

  useEffect(() => {
    if (state === "idle") return;
    const t = window.setTimeout(() => setState("idle"), COPIED_RESET_MS);
    return () => window.clearTimeout(t);
  }, [state]);

  async function copy() {
    try {
      await navigator.clipboard.writeText(value);
      setState("copied");
    } catch {
      // Clipboard access can be denied (permissions, insecure context). Say
      // so instead of pretending: the address is still selectable on screen.
      setState("failed");
    }
  }

  return (
    <button
      type="button"
      onClick={copy}
      aria-label={label}
      className={cn(
        "inline-flex items-center gap-1.5 px-3 py-1.5 border border-border/70 rounded-sm font-mono text-[11px] tracking-wider uppercase text-muted hover:text-fg hover:border-fg/30 transition-colors cursor-pointer",
        className,
      )}
    >
      {state === "copied" ? <Check size={12} /> : <Copy size={12} />}
      <span aria-live="polite">
        {state === "copied" ? "Copied" : state === "failed" ? "Select & copy" : "Copy"}
      </span>
    </button>
  );
}
