import { useState, useCallback } from "react";
import { Link, useLocation } from "react-router";
import { Menu, X } from "lucide-react";
import { cn } from "../../lib/utils";
import { NAV_ROUTES, ROUTES } from "../../content/routes";
import { MobileMenu } from "./mobile-menu";
import { Button } from "../ui/button";
import oramaIcon from "../../assets/orama-icon.png";

const linkClass =
  "px-3 py-1.5 text-xs tracking-wider uppercase font-mono rounded-full transition-colors duration-150 whitespace-nowrap";
const activeClass = "text-fg bg-white/[0.06]";
const inactiveClass = "text-muted hover:text-fg hover:bg-white/[0.04]";

export function Navbar() {
  const [mobileOpen, setMobileOpen] = useState(false);
  const { pathname } = useLocation();
  const handleMobileClose = useCallback(() => setMobileOpen(false), []);

  return (
    <>
      <header className="fixed left-0 right-0 top-4 z-50 flex justify-center px-4">
        <nav
          aria-label="Main"
          className={cn(
            "flex items-center h-11 rounded-full border border-border/60 bg-surface-2/80 backdrop-blur-xl shadow-[0_4px_24px_rgba(0,0,0,0.4)]",
            "w-full justify-between pl-3 pr-1.5",
            "lg:w-auto lg:justify-start lg:gap-1",
          )}
        >
          <Link to="/" className="flex items-center gap-2 group shrink-0 pr-1 h-11" aria-label="Orama home">
            <img
              src={oramaIcon}
              alt=""
              className="h-7 w-7 shrink-0 transition-transform duration-700 ease-in-out group-hover:rotate-[360deg]"
            />
            <span className="font-display text-sm font-bold tracking-widest text-fg">ORAMA</span>
          </Link>

          <span className="hidden lg:block w-px h-4 bg-border/70 mx-1" aria-hidden="true" />

          <div className="hidden lg:flex items-center gap-0.5">
            {NAV_ROUTES.map((route) => (
              <Link
                key={route.path}
                to={route.path}
                className={cn(linkClass, pathname.startsWith(route.path) ? activeClass : inactiveClass)}
              >
                {route.nav}
              </Link>
            ))}
          </div>

          <div className="hidden lg:flex items-center ml-1">
            <Button variant="primary" size="sm" className="rounded-full" asChild>
              <Link to={ROUTES.investors.path}>Investors</Link>
            </Button>
          </div>

          <button
            type="button"
            onClick={() => setMobileOpen((prev) => !prev)}
            className="lg:hidden flex items-center justify-center w-11 h-11 text-muted hover:text-fg transition-colors"
            aria-label={mobileOpen ? "Close menu" : "Open menu"}
            aria-expanded={mobileOpen}
          >
            {mobileOpen ? <X size={20} /> : <Menu size={20} />}
          </button>
        </nav>
      </header>

      <MobileMenu open={mobileOpen} onClose={handleMobileClose} />
    </>
  );
}
