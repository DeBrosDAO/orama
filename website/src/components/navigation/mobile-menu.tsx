import { useEffect } from "react";
import { Link, useLocation } from "react-router";
import { X } from "lucide-react";
import { SUPPORT_LINK, NAV, isActivePath, isGroup } from "../../content/navigation";
import { Button } from "../ui/button";
import { cn } from "../../lib/utils";

const menuLinkClass =
  "py-2 text-xl font-display font-semibold tracking-tight transition-colors duration-150";

export interface MobileMenuProps {
  open: boolean;
  onClose: () => void;
}

export function MobileMenu({ open, onClose }: MobileMenuProps) {
  const location = useLocation();

  useEffect(() => {
    onClose();
  }, [location.pathname, onClose]);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  useEffect(() => {
    document.body.style.overflow = open ? "hidden" : "";
    return () => {
      document.body.style.overflow = "";
    };
  }, [open]);

  return (
    <div
      className={cn(
        "fixed inset-0 z-[60] bg-bg/95 lg:hidden flex flex-col transition-opacity duration-300",
        // backdrop-blur only while open: a permanently mounted full-screen
        // backdrop-filter costs a compositing layer on every frame.
        open
          ? "opacity-100 pointer-events-auto backdrop-blur-md"
          : "opacity-0 pointer-events-none invisible",
      )}
    >
      <div className="flex items-center justify-end px-6 pt-5 pb-2">
        <button
          type="button"
          onClick={onClose}
          className="flex items-center justify-center w-10 h-10 text-muted hover:text-fg transition-colors"
          aria-label="Close menu"
        >
          <X size={24} />
        </button>
      </div>

      <nav aria-label="Mobile" className="flex flex-col gap-6 px-6 overflow-y-auto">
        {NAV.map((entry) => {
          const items = isGroup(entry) ? entry.items : [entry];
          return (
            <div key={entry.label} className="flex flex-col">
              {isGroup(entry) && (
                <span className="mb-1 font-mono text-[11px] tracking-[0.2em] uppercase text-muted/70">{entry.label}</span>
              )}
              {items.map((item) => {
                const current = isActivePath(location.pathname, item.path);
                return (
                  <Link
                    key={item.path}
                    to={item.path}
                    aria-current={current ? "page" : undefined}
                    className={cn(menuLinkClass, current ? "text-fg" : "text-muted hover:text-fg")}
                  >
                    {item.label}
                  </Link>
                );
              })}
            </div>
          );
        })}
      </nav>

      <div className="mt-auto px-6 pt-6 pb-8">
        <Button variant="primary" size="lg" className="w-full rounded-full" asChild>
          <Link to={SUPPORT_LINK.path}>{SUPPORT_LINK.label}</Link>
        </Button>
      </div>
    </div>
  );
}
