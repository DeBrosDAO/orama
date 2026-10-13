import { useState } from "react";
import type { FormEvent } from "react";
import { useQuery } from "../../data/use-query";
import { parseId } from "../../model/lookup";
import type { Bid } from "../../model/market";
import { Amount } from "../../ui/amount";
import { Card } from "../../ui/card";
import { Facts } from "../../ui/facts";
import { WalletLink } from "../../ui/links";
import { Query } from "../../ui/query-states";

/** What the chain holds for one bid. A bid that is not there was never made, or was cancelled, refunded or accepted. */
export function BidAnswer({ bid, id }: { bid: Bid | null; id: number }) {
  if (bid === null) {
    return <p className="mt-3 text-sm text-muted">The market holds no bid {id} on this listing: it was never made, or it was cancelled, refunded or accepted.</p>;
  }
  return (
    <div className="mt-3">
      <Facts rows={[["Bidder", <WalletLink wallet={{ address: bid.bidder }} />], ["Escrowed", <Amount norama={bid.amount} />]]} />
    </div>
  );
}

/** Looks up one bid of a listing by its number. The chain keeps no list of a listing's bids. */
export function BidLookup({ listingId }: { listingId: number }) {
  const [text, setText] = useState("");
  const [asked, setAsked] = useState<number | null>(null);
  const bidId = parseId(text.trim());
  const invalid = text.trim() !== "" && bidId === null;
  const { state, refetch } = useQuery((s) => (asked === null ? Promise.resolve(null) : s.assets.getBid(listingId, asked)), [listingId, asked]);

  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (bidId !== null) setAsked(bidId);
  };

  return (
    <Card title="Look up a bid">
      <form onSubmit={submit} className="flex items-center gap-2">
        <label htmlFor="bid-number" className="sr-only">
          Bid number
        </label>
        <input
          id="bid-number"
          inputMode="numeric"
          autoComplete="off"
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder="Bid number"
          aria-invalid={invalid}
          className="w-40 rounded-lg border border-border bg-bg px-3 py-1.5 text-sm placeholder:text-muted aria-[invalid=true]:border-loss"
        />
        <button
          type="submit"
          disabled={bidId === null}
          className="rounded-lg border border-border bg-surface-2 px-3 py-1.5 text-sm enabled:hover:border-fg/30 enabled:cursor-pointer disabled:opacity-50"
        >
          Look up
        </button>
      </form>
      <p className="mt-2 text-xs text-muted">Bids are numbered across the whole market, not per listing, and the chain does not list a listing's bids. A transaction that cancels or settles a bid names its number.</p>
      {asked !== null && (
        <Query state={state} onRetry={refetch}>
          {(bid) => (asked === null ? null : <BidAnswer bid={bid} id={asked} />)}
        </Query>
      )}
    </Card>
  );
}
