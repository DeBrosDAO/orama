import { createContext, useCallback, useContext, useMemo, useState } from "react";
import type { MouseEvent, ReactNode } from "react";
import { Link } from "react-router";
import * as Dialog from "@radix-ui/react-dialog";
import { X } from "lucide-react";
import { useQuery } from "../data/use-query";
import { amountOf } from "../model/describe";
import { explorerPaths } from "../model/routes";
import type { TxDetail } from "../model/types";
import { Amount } from "../ui/amount";
import { Badge } from "../ui/badge";
import { Card } from "../ui/card";
import { CopyChip } from "../ui/copy";
import { moreActionsNote } from "../ui/message-text";
import { Query, Skeleton } from "../ui/query-states";
import { RelTime } from "../ui/rel-time";
import { Sentence } from "../ui/sentence";

interface PeekContextValue {
  /** Open a side preview of a transaction without leaving the page. */
  openTx: (hash: string) => void;
}

const PeekContext = createContext<PeekContextValue | null>(null);

export function usePeek(): PeekContextValue {
  const ctx = useContext(PeekContext);
  if (!ctx) throw new Error("usePeek must be used inside <PeekProvider>");
  return ctx;
}

export function PeekProvider({ children }: { children: ReactNode }) {
  const [hash, setHash] = useState<string | null>(null);
  const openTx = useCallback((h: string) => setHash(h), []);
  const value = useMemo(() => ({ openTx }), [openTx]);
  return (
    <PeekContext.Provider value={value}>
      {children}
      <Dialog.Root open={hash !== null} onOpenChange={(o) => !o && setHash(null)}>
        <Dialog.Portal>
          <Dialog.Overlay className="fixed inset-0 z-[60] bg-black/60" />
          <Dialog.Content
            aria-describedby={undefined}
            className="explorer fixed inset-y-0 right-0 z-[61] w-[min(420px,100vw)] overflow-y-auto border-l border-fg/20 bg-[#0c0c0f] p-5 shadow-2xl"
          >
            <div className="mb-4 flex items-center justify-between">
              <Dialog.Title className="text-xs font-semibold uppercase tracking-[0.09em] text-muted">Transaction preview</Dialog.Title>
              <Dialog.Close aria-label="Close preview" className="rounded-md p-1 text-muted hover:text-fg cursor-pointer">
                <X size={18} />
              </Dialog.Close>
            </div>
            <div onClick={(e) => closeOnLinkClick(e, () => setHash(null))}>{hash && <PeekBody hash={hash} />}</div>
          </Dialog.Content>
        </Dialog.Portal>
      </Dialog.Root>
    </PeekContext.Provider>
  );
}

/** Following a link out of the drawer (a name in the sentence, "open full") must not leave it over the new page. */
function closeOnLinkClick(e: MouseEvent<HTMLElement>, close: () => void) {
  if (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
  if ((e.target as HTMLElement).closest("a")) close();
}

function PeekBody({ hash }: { hash: string }) {
  const { state, refetch } = useQuery((s) => s.getTx(hash), [hash]);
  return (
    <Query state={state} onRetry={refetch} loading={<Skeleton className="h-64 w-full" />}>
      {(tx) => (tx ? <PeekTx tx={tx} /> : <p className="text-sm text-muted">This transaction was not found.</p>)}
    </Query>
  );
}

function PeekTx({ tx }: { tx: TxDetail }) {
  const message = tx.messages[0];
  const amount = message ? amountOf(message) : null;
  const moreNote = moreActionsNote(tx.messages.length);
  return (
    <div className="space-y-4">
      <div className="flex items-center gap-2">
        {tx.status.ok ? <Badge tone="ok">✓ Completed</Badge> : <Badge tone="bad">✕ Failed</Badge>}
        <RelTime iso={tx.time} className="text-sm text-muted" />
      </div>
      {message && <p className="text-lg font-semibold leading-snug"><Sentence message={message} failed={!tx.status.ok} /></p>}
      <Card>
        {amount !== null && <div className="text-2xl font-semibold"><Amount norama={amount} /></div>}
        <p className="mt-1 text-sm text-muted">
          {tx.status.ok ? (
            <>Fee <Amount norama={tx.fee.burned} bare maxFraction={6} /> ORAMA, burned</>
          ) : (
            <>Nothing moved. The fee was still charged.</>
          )}
        </p>
      </Card>
      {moreNote && <p className="text-xs text-muted">{moreNote}</p>}
      <div className="grid gap-2">
        <Link
          to={explorerPaths.tx(tx.hash)}
          className="rounded-lg bg-fg px-3 py-2 text-center text-sm font-semibold text-bg hover:bg-fg/90"
        >
          Open full transaction →
        </Link>
        <CopyChip value={tx.hash} label="Copy transaction hash" withText className="justify-center py-1.5" />
      </div>
      <p className="text-xs text-muted">Closing this returns you exactly where you were.</p>
    </div>
  );
}
