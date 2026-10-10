import { useEffect } from "react";
import type { ReactNode } from "react";
import { Link, useLocation } from "react-router";
import { useSource } from "../data/provider";
import { ExplorerHeader } from "./header";
import { TrailBar } from "./trail";

/** Header, investigation trail and footer around whatever page is showing. */
export function ExplorerShell({ children }: { children: ReactNode }) {
  const { pathname } = useLocation();
  const origin = useSource().origin;

  useEffect(() => {
    window.scrollTo(0, 0);
  }, [pathname]);

  return (
    <div className="explorer min-h-screen bg-bg text-fg">
      <ExplorerHeader />
      <TrailBar />
      <main>{children}</main>
      <footer className="mx-auto max-w-[1140px] px-4 py-10 text-center text-xs text-muted sm:px-6">
        <p>Live data from {origin.label}.</p>
        <p className="mt-1">
          <Link to="/" className="underline underline-offset-4 hover:text-fg">
            orama.network
          </Link>
        </p>
      </footer>
    </div>
  );
}
