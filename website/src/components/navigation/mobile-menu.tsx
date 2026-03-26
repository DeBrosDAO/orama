import { useEffect } from "react";
import { Link, useLocation } from "react-router";
import { ExternalLink } from "lucide-react";
import { NAV_LINKS, MORE_LINKS } from "../../data/navigation";
import { Button } from "../ui/button";
import { cn } from "../../lib/utils";

const menuLinkClass = "py-3 text-2xl font-display font-semibold tracking-tight transition-colors duration-150";

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
    if (open) {
      document.body.style.overflow = "hidden";
    } else {
      document.body.style.overflow = "";
    }
    return () => {
      document.body.style.overflow = "";
    };
  }, [open]);

  return (
    <div
      className={cn(
        "fixed inset-0 z-40 bg-bg/95 backdrop-blur-md md:hidden flex flex-col transition-all duration-300",
        open
          ? "opacity-100 pointer-events-auto"
          : "opacity-0 pointer-events-none",
      )}
    >
      <div className="h-20 shrink-0" />

      <nav className="flex flex-col px-6 gap-1">
        {NAV_LINKS.map((link) => {
          if (link.external) {
            return (
              <a
                key={link.href}
                href={link.href}
                target="_blank"
                rel="noopener noreferrer"
                className={cn(menuLinkClass, "flex items-center gap-2 text-muted hover:text-fg")}
              >
                {link.label}
                <ExternalLink className="w-4 h-4" />
              </a>
            );
          }
          const isActive = location.pathname === link.href;
          return (
            <Link
              key={link.href}
              to={link.href}
              className={cn(menuLinkClass, isActive ? "text-fg" : "text-muted hover:text-fg")}
            >
              {link.label}
            </Link>
          );
        })}

        <div className="border-t border-border/30 mt-3 pt-3">
          {MORE_LINKS.map((link) => {
            const isActive = location.pathname === link.href;
            return (
              <Link
                key={link.href}
                to={link.href}
                className={cn(menuLinkClass, "text-lg", isActive ? "text-fg" : "text-muted hover:text-fg")}
              >
                {link.label}
              </Link>
            );
          })}
        </div>
      </nav>

      <div className="mt-auto px-6 pb-8">
        <Button variant="primary" size="lg" className="w-full" asChild>
          <Link to="/invest">Investors Dashboard</Link>
        </Button>
      </div>
    </div>
  );
}
