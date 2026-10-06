import type { ReactNode } from "react";
import { Link } from "react-router";
import type { QueryState } from "../data/use-query";
import { explorerPaths } from "../model/routes";
import { cn } from "../../lib/utils";
import { Card } from "./card";

export function Skeleton({ className }: { className?: string }) {
  return <div className={cn("animate-pulse rounded-md bg-surface-3", className)} aria-hidden="true" />;
}

export interface QueryProps<T> {
  state: QueryState<T>;
  /** Shown while loading. Defaults to a plain block. */
  loading?: ReactNode;
  onRetry?: () => void;
  children: (data: T) => ReactNode;
}

/** Renders the loading, error or ready state of a query; `children` only ever sees data. */
export function Query<T>({ state, loading, onRetry, children }: QueryProps<T>) {
  if (state.status === "loading") {
    return <div role="status" aria-label="Loading">{loading ?? <Skeleton className="h-40 w-full" />}</div>;
  }
  if (state.status === "error") return <ErrorBox message={state.error.message} onRetry={onRetry} />;
  return <>{children(state.data)}</>;
}

export function ErrorBox({ message, onRetry }: { message: string; onRetry?: () => void }) {
  return (
    <Card>
      <div role="alert" className="space-y-3">
        <p className="font-medium text-loss">Could not load this.</p>
        <p className="text-sm text-muted">{message}</p>
        {onRetry && (
          <button type="button" onClick={onRetry} className="rounded-lg border border-border bg-surface-2 px-3 py-1.5 text-sm hover:border-fg/30 cursor-pointer">
            Try again
          </button>
        )}
      </div>
    </Card>
  );
}

export interface NotFoundProps {
  title: string;
  hint: string;
}

/** For a lookup that resolved to nothing: say what was missing and how to get back. */
export function NotFoundBox({ title, hint }: NotFoundProps) {
  return (
    <Card>
      <div className="space-y-3 py-6 text-center">
        <h1 className="font-display text-2xl font-semibold">{title}</h1>
        <p className="mx-auto max-w-prose text-sm text-muted">{hint}</p>
        <Link to={explorerPaths.home} className="inline-block rounded-lg border border-border bg-surface-2 px-3 py-1.5 text-sm hover:border-fg/30">
          Back to the explorer
        </Link>
      </div>
    </Card>
  );
}
