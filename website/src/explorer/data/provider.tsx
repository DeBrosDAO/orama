import { createContext, useContext, useEffect, useMemo, useState } from "react";
import type { ReactNode } from "react";
import { toError } from "./errors";
import { nextHead } from "./head";
import type { ExplorerDataSource } from "./source";
import type { Head } from "../model/types";

/** What the header and live pages need to know about the chain's height. */
interface HeadState {
  /** The latest head the source has announced; null until the first read. */
  head: Head | null;
  /** Why the head could not be read, or null. Cleared by the next successful head. */
  error: Error | null;
}

/** The source never changes while the app runs, so nothing that only reads it re-renders on a head tick. */
const SourceContext = createContext<ExplorerDataSource | null>(null);
const HeadContext = createContext<HeadState | null>(null);

export interface ExplorerProviderProps {
  source: ExplorerDataSource;
  children: ReactNode;
}

function useHeadFeed(source: ExplorerDataSource): HeadState {
  const [head, setHead] = useState<Head | null>(null);
  const [error, setError] = useState<Error | null>(null);

  useEffect(() => {
    let live = true;
    const accept = (h: Head) => {
      if (!live) return;
      setHead((prev) => nextHead(prev, h));
      setError(null);
    };
    source.getHead().then(accept, (err: unknown) => live && setError(toError(err)));
    const stop = source.subscribeHead(accept, (err) => live && setError(err));
    return () => {
      live = false;
      stop();
    };
  }, [source]);

  return useMemo(() => ({ head, error }), [head, error]);
}

export function ExplorerProvider({ source, children }: ExplorerProviderProps) {
  const headState = useHeadFeed(source);
  return (
    <SourceContext.Provider value={source}>
      <HeadContext.Provider value={headState}>{children}</HeadContext.Provider>
    </SourceContext.Provider>
  );
}

const OUTSIDE_PROVIDER = "explorer hooks must be used inside <ExplorerProvider>";

export function useSource(): ExplorerDataSource {
  const source = useContext(SourceContext);
  if (!source) throw new Error(OUTSIDE_PROVIDER);
  return source;
}

function useHeadState(): HeadState {
  const state = useContext(HeadContext);
  if (!state) throw new Error(OUTSIDE_PROVIDER);
  return state;
}

/** The latest head, or null until the source has answered. Re-renders on every new head. */
export function useHead(): Head | null {
  return useHeadState().head;
}

/** Why the head could not be read (source down), or null. Show it; a blank height hides the outage. */
export function useHeadError(): Error | null {
  return useHeadState().error;
}
