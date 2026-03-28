import { useState, useEffect, useCallback } from "react";
import { Link, useLocation } from "react-router";
import { Menu, X, ExternalLink, ChevronDown } from "lucide-react";
import { cn } from "../../lib/utils";
import { NAV_LINKS, MORE_LINKS } from "../../data/navigation";
import { MobileMenu } from "./mobile-menu";
import { Button } from "../ui/button";
import oramaIcon from "../../assets/orama-icon.png";

const linkClass = "px-3 py-1.5 text-xs tracking-wider uppercase font-mono rounded-full transition-colors duration-150";
const activeClass = "text-fg bg-white/[0.06]";
const inactiveClass = "text-muted hover:text-fg hover:bg-white/[0.04]";

export function Navbar({ bannerVisible = true }: { bannerVisible?: boolean }) {
  const [mobileOpen, setMobileOpen] = useState(false);
  const [moreOpen, setMoreOpen] = useState(false);
  const { pathname } = useLocation();
  const handleMobileClose = useCallback(() => setMobileOpen(false), []);

  useEffect(() => {
    setMoreOpen(false);
  }, [pathname]);

  return (
    <>
      <header className={cn("fixed left-0 right-0 z-50 flex justify-center px-4 pt-2 transition-[top] duration-300", bannerVisible ? "top-16" : "top-4")}>
        <nav className="flex items-center justify-between w-full max-w-5xl h-12 px-5 bg-surface-2/80 backdrop-blur-xl border border-border/60 rounded-full shadow-[0_4px_24px_rgba(0,0,0,0.4)]">
          <Link to="/" className="flex items-center gap-2 group">
            <img src={oramaIcon} alt="Orama" className="h-8 w-8 shrink-0 transition-transform duration-700 ease-in-out group-hover:rotate-[360deg]" />
          </Link>

          <div className="hidden md:flex items-center gap-1">
            {NAV_LINKS.map((link) => {
              if (link.external) {
                return (
                  <a
                    key={link.href}
                    href={link.href}
                    target="_blank"
                    rel="noopener noreferrer"
                    className={cn(linkClass, "flex items-center gap-1", inactiveClass)}
                  >
                    {link.label}
                    <ExternalLink className="w-2.5 h-2.5" />
                  </a>
                );
              }
              const isActive = pathname === link.href;
              return (
                <Link
                  key={link.href}
                  to={link.href}
                  className={cn(linkClass, isActive ? activeClass : inactiveClass)}
                >
                  {link.label}
                </Link>
              );
            })}

            {/* More dropdown */}
            <div className="relative">
              <button
                type="button"
                onClick={() => setMoreOpen((prev) => !prev)}
                className={cn(
                  linkClass,
                  "flex items-center gap-1 cursor-pointer",
                  moreOpen || MORE_LINKS.some((l) => pathname === l.href)
                    ? activeClass
                    : inactiveClass,
                )}
              >
                More
                <ChevronDown
                  className={cn(
                    "w-3 h-3 transition-transform duration-200",
                    moreOpen && "rotate-180",
                  )}
                />
              </button>

              {moreOpen && (
                <>
                  <div
                    className="fixed inset-0 z-40"
                    onClick={() => setMoreOpen(false)}
                  />
                  <div className="absolute top-full right-0 mt-2 z-50 w-48 py-1 bg-surface-2/95 backdrop-blur-xl border border-border/60 rounded-lg shadow-[0_4px_24px_rgba(0,0,0,0.4)]">
                    {MORE_LINKS.map((link) => (
                      <Link
                        key={link.href}
                        to={link.href}
                        onClick={() => setMoreOpen(false)}
                        className={cn(
                          "block px-4 py-2.5 text-xs font-mono tracking-wider uppercase transition-colors",
                          pathname === link.href
                            ? "text-fg bg-white/[0.06]"
                            : "text-muted hover:text-fg hover:bg-white/[0.04]",
                        )}
                      >
                        {link.label}
                      </Link>
                    ))}
                  </div>
                </>
              )}
            </div>
          </div>

          <div className="hidden md:flex items-center">
            <Button variant="primary" size="sm" asChild>
              <Link to="/whitelist">Join the Waitlist</Link>
            </Button>
          </div>

          <button
            type="button"
            onClick={() => setMobileOpen((prev) => !prev)}
            className="md:hidden flex items-center justify-center w-9 h-9 text-muted hover:text-fg transition-colors"
            aria-label={mobileOpen ? "Close menu" : "Open menu"}
          >
            {mobileOpen ? <X size={20} /> : <Menu size={20} />}
          </button>
        </nav>
      </header>

      <MobileMenu
        open={mobileOpen}
        onClose={handleMobileClose}
      />
    </>
  );
}
