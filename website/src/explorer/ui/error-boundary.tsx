import { Component } from "react";
import type { ReactNode } from "react";
import { Link } from "react-router";
import { explorerPaths } from "../model/routes";
import { Card } from "./card";

interface State {
  failed: boolean;
}

/**
 * The backstop for a page that throws while rendering (for example a record
 * with a malformed amount). Without it one bad record unmounts the whole
 * explorer; with it the reader gets a clear message and a way back.
 */
export class ErrorBoundary extends Component<{ children: ReactNode }, State> {
  state: State = { failed: false };

  static getDerivedStateFromError(): State {
    return { failed: true };
  }

  render(): ReactNode {
    if (!this.state.failed) return this.props.children;
    return (
      <div className="mx-auto w-full max-w-[1140px] px-4 py-6 sm:px-6">
        <Card>
          <div role="alert" className="space-y-3 py-6 text-center">
            <h1 className="font-display text-2xl font-semibold">This page could not be shown</h1>
            <p className="mx-auto max-w-prose text-sm text-muted">
              Some of the data for it was not in the shape the explorer expects. Nothing was changed. Go back to the
              start and try again.
            </p>
            <Link to={explorerPaths.home} reloadDocument className="inline-block rounded-lg border border-border bg-surface-2 px-3 py-1.5 text-sm hover:border-fg/30">
              Back to the explorer
            </Link>
          </div>
        </Card>
      </div>
    );
  }
}
