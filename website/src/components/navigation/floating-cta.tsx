import { useState, useEffect, useCallback } from "react";
import { useLocation } from "react-router";
import { cn } from "../../lib/utils";

export function FloatingCTA() {
  const [visible, setVisible] = useState(false);
  const [footerVisible, setFooterVisible] = useState(false);
  const location = useLocation();
  const hasPlayer = location.pathname === "/about";

  const handleScroll = useCallback(() => {
    const scrolled = window.scrollY > window.innerHeight * 0.8;
    setVisible(scrolled);
  }, []);

  useEffect(() => {
    window.addEventListener("scroll", handleScroll, { passive: true });
    return () => window.removeEventListener("scroll", handleScroll);
  }, [handleScroll]);

  // Detect footer to avoid overlap
  useEffect(() => {
    const footer = document.querySelector("footer");
    if (!footer) return;

    const observer = new IntersectionObserver(
      ([entry]) => setFooterVisible(entry.isIntersecting),
      { threshold: 0 },
    );
    observer.observe(footer);
    return () => observer.disconnect();
  }, []);

  return (
    <div
      className={cn(
        "fixed z-40 flex left-1/2 -translate-x-1/2 sm:left-auto sm:translate-x-0 sm:right-6 lg:right-20 transition-all duration-300",
        visible ? "opacity-100 translate-y-0" : "opacity-0 translate-y-4 pointer-events-none",
        footerVisible ? "bottom-[140px]" : hasPlayer ? "bottom-20 sm:bottom-16" : "bottom-6",
      )}
    >
      <div className="flex items-center overflow-hidden rounded-full border border-white/20 bg-white/10 backdrop-blur-md shadow-lg">
        <a
          href="https://t.me/debrosportal"
          target="_blank"
          rel="noopener noreferrer"
          className="h-12 px-4 sm:px-5 flex items-center text-xs font-mono tracking-wider uppercase text-white transition-colors duration-200 hover:bg-white hover:text-black"
        >
          Join the Waitlist
        </a>
      </div>
    </div>
  );
}
