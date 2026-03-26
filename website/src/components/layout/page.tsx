import { useEffect } from "react";
import type { ReactNode } from "react";

export interface PageProps {
  title: string;
  children: ReactNode;
}

export function Page({ title, children }: PageProps) {
  useEffect(() => {
    document.title = `${title} — Orama`;
  }, [title]);

  useEffect(() => {
    window.scrollTo(0, 0);
  }, []);

  return <>{children}</>;
}
