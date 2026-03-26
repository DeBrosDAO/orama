import { useState, useEffect } from "react";
import { useLocation } from "react-router";
import { ArrowUp } from "lucide-react";
import { cn } from "../../lib/utils";

export function ScrollToTop() {
  const [visible, setVisible] = useState(false);
  const location = useLocation();
  const hasPlayer = location.pathname === "/about";

  useEffect(() => {
    const handleScroll = () => {
      setVisible(window.scrollY > 400);
    };
    window.addEventListener("scroll", handleScroll);
    return () => window.removeEventListener("scroll", handleScroll);
  }, []);

  return (
    <button
      type="button"
      onClick={() => window.scrollTo({ top: 0, behavior: "smooth" })}
      className={cn(
        "fixed right-3 sm:right-6 z-40 w-10 h-10 flex items-center justify-center",
        "bg-surface-2/90 backdrop-blur-sm border border-border/60 rounded-full",
        "text-muted hover:text-fg hover:border-border transition-all duration-200",
        "shadow-[0_4px_12px_rgba(0,0,0,0.4)]",
        hasPlayer ? "bottom-20 sm:bottom-16" : "bottom-6",
        visible ? "opacity-100 translate-y-0" : "opacity-0 translate-y-4 pointer-events-none",
      )}
      aria-label="Scroll to top"
    >
      <ArrowUp className="w-4 h-4" />
    </button>
  );
}
