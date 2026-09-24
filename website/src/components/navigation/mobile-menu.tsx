import { useEffect } from "react";
import { Link, useLocation } from "react-router";
import { X } from "lucide-react";
import { NAV_ROUTES, ROUTES } from "../../content/routes";
import { Button } from "../ui/button";
import { cn } from "../../lib/utils";

const menuLinkClass =
  "py-3 text-2xl font-display font-semibold tracking-tight transition-colors duration-150";

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

      <nav aria-label="Mobile" className="flex flex-col px-6 gap-1">
        {[...NAV_ROUTES, ROUTES.whitepaper, ROUTES.donate].map((route) => (
          <Link
            key={route.path}
            to={route.path}
            className={cn(
              menuLinkClass,
              location.pathname.startsWith(route.path) ? "text-fg" : "text-muted hover:text-fg",
            )}
          >
            {"nav" in route ? route.nav : route.title}
          </Link>
        ))}
      </nav>

      <div className="mt-auto px-6 pb-8">
        <Button variant="primary" size="lg" className="w-full rounded-full" asChild>
          <Link to={ROUTES.investors.path}>Investors</Link>
        </Button>
      </div>
    </div>
  );
}
