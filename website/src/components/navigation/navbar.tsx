import { useState, useCallback } from "react";
import { Link, useLocation } from "react-router";
import { Menu, X, ExternalLink } from "lucide-react";
import { cn } from "../../lib/utils";
import { NAV_LINKS } from "../../data/navigation";
import { MobileMenu } from "./mobile-menu";
import { Button } from "../ui/button";
import oramaIcon from "../../assets/orama-icon.png";

const linkClass =
  "px-3 py-1.5 text-xs tracking-wider uppercase font-mono rounded-full transition-colors duration-150";
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
          className={cn(
            "flex items-center h-11 rounded-full border border-border/60 bg-surface-2/80 backdrop-blur-xl shadow-[0_4px_24px_rgba(0,0,0,0.4)]",
            // Mobile: a full-width bar. Desktop: a pill that hugs its content —
            // a fixed max-width pill leaves a large empty gap either side of
            // three items.
            "w-full justify-between pl-3 pr-1.5",
            "md:w-auto md:justify-start md:gap-1 md:pl-3 md:pr-1.5",
          )}
        >
          <Link
            to="/"
            className="flex items-center gap-2 group shrink-0 pr-1 h-11"
            aria-label="Orama home"
          >
            <img
              src={oramaIcon}
              alt=""
              className="h-7 w-7 shrink-0 transition-transform duration-700 ease-in-out group-hover:rotate-[360deg]"
            />
            <span className="font-display text-sm font-bold tracking-widest text-fg">
              ORAMA
            </span>
          </Link>

          <span
            className="hidden md:block w-px h-4 bg-border/70 mx-1"
            aria-hidden="true"
          />

          <div className="hidden md:flex items-center gap-1">
            {NAV_LINKS.map((link) => {
              if (link.external) {
                return (
                  <a
                    key={link.href}
                    href={link.href}
                    target="_blank"
                    rel="noopener noreferrer"
                    className={cn(
                      linkClass,
                      "flex items-center gap-1",
                      inactiveClass,
                    )}
                  >
                    {link.label}
                    <ExternalLink className="w-2.5 h-2.5" />
                  </a>
                );
              }
              const isActive = pathname.startsWith(link.href);
              return (
                <Link
                  key={link.href}
                  to={link.href}
                  className={cn(
                    linkClass,
                    isActive ? activeClass : inactiveClass,
                  )}
                >
                  {link.label}
                </Link>
              );
            })}
          </div>

          <div className="hidden md:flex items-center ml-1">
            <Button variant="primary" size="sm" asChild>
              <Link to="/docs/developer/getting-started">Get Started</Link>
            </Button>
          </div>

          <button
            type="button"
            onClick={() => setMobileOpen((prev) => !prev)}
            className="md:hidden flex items-center justify-center w-11 h-11 text-muted hover:text-fg transition-colors"
            aria-label={mobileOpen ? "Close menu" : "Open menu"}
          >
            {mobileOpen ? <X size={20} /> : <Menu size={20} />}
          </button>
        </nav>
      </header>

      <MobileMenu open={mobileOpen} onClose={handleMobileClose} />
    </>
  );
}
