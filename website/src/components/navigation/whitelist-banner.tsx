import { useState } from "react";
import { X, ArrowRight } from "lucide-react";
import { cn } from "../../lib/utils";

export function WhitelistBanner({ onDismiss }: { onDismiss?: () => void }) {
  const [dismissed, setDismissed] = useState(false);

  if (dismissed) return null;

  return (
    <div className="fixed top-0 left-0 right-0 z-50 flex justify-center px-4 pt-4">
      <div
        className={cn(
          "flex items-center justify-between w-full max-w-5xl h-10 px-5",
          "rounded-full",
          "shadow-[0_4px_24px_rgba(161,161,170,0.15)]",
        )}
        style={{
          background: "linear-gradient(135deg, #d4d4d8 0%, #a1a1aa 40%, #71717a 100%)",
        }}
      >
        <a
          href="/whitelist"
          target="_blank"
          rel="noopener noreferrer"
          className="flex items-center gap-3 flex-1 justify-center group"
        >
          <span className="w-1.5 h-1.5 rounded-full bg-black animate-pulse-dot shrink-0" />
          <span className="text-xs font-mono font-semibold tracking-wider text-black">
            Orama Network L1 Blockchain — Join Waitlist
          </span>
          <ArrowRight className="w-3.5 h-3.5 text-black/50 group-hover:text-black group-hover:translate-x-0.5 transition-all shrink-0" />
        </a>
        <button
          type="button"
          onClick={() => { setDismissed(true); onDismiss?.(); }}
          className="flex items-center justify-center w-6 h-6 text-black/40 hover:text-black transition-colors cursor-pointer ml-2"
          aria-label="Dismiss banner"
        >
          <X size={14} />
        </button>
      </div>
    </div>
  );
}
