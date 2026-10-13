import type { ReactNode } from "react";
import { Card } from "./card";

export interface NotOnChainItem {
  what: string;
  why: ReactNode;
}

/** What a page cannot show because the chain does not keep it, said in place of a made-up figure. */
export function NotOnChain({ items }: { items: readonly NotOnChainItem[] }) {
  return (
    <Card title="Not on the chain">
      <dl className="grid gap-x-6 gap-y-3 text-[13px] sm:grid-cols-[minmax(0,14rem)_1fr]">
        {items.map((item) => (
          <div key={item.what} className="contents">
            <dt className="font-medium">{item.what}</dt>
            <dd className="text-muted">{item.why}</dd>
          </div>
        ))}
      </dl>
    </Card>
  );
}

/** A note that a list was cut at the number of entries the explorer reads. */
export function CutNotice({ shown, what }: { shown: number; what: string }) {
  return (
    <p role="note" className="rounded-lg border border-border bg-surface-2 px-3 py-2 text-[13px] text-muted">
      The chain holds more {what} than the {shown} the explorer reads, so every figure here covers those {shown} only.
    </p>
  );
}
