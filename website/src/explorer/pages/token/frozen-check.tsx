import { useState } from "react";
import type { FormEvent } from "react";
import { useQuery } from "../../data/use-query";
import { parseAccount } from "../../model/lookup";
import { Card } from "../../ui/card";
import { Query } from "../../ui/query-states";

/** The answer of one frozen check, shown under the form. */
export function FrozenAnswer({ frozen }: { frozen: boolean }) {
  return (
    <p role="status" className="mt-2 text-sm">
      {frozen ? "Frozen: this account cannot send, receive, mint or burn the token." : "Not frozen: x/token holds no freeze on this account."}
    </p>
  );
}

/** Asks x/token whether the creator froze one account. The chain lists no frozen accounts, so the account has to be named. */
export function FrozenCheck({ denom }: { denom: string }) {
  const [text, setText] = useState("");
  const [asked, setAsked] = useState<string | null>(null);
  const account = parseAccount(text.trim());
  const invalid = text.trim() !== "" && account === null;
  const { state, refetch } = useQuery((s) => (asked === null ? Promise.resolve(null) : s.assets.isFrozen(denom, asked)), [denom, asked]);

  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (account !== null) setAsked(account);
  };

  return (
    <Card title="Is an account frozen?">
      <form onSubmit={submit} className="flex items-center gap-2">
        <label htmlFor="frozen-account" className="sr-only">
          Account address
        </label>
        <input
          id="frozen-account"
          autoComplete="off"
          spellCheck={false}
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder="orama1…"
          aria-invalid={invalid}
          className="min-w-0 flex-1 rounded-lg border border-border bg-bg px-3 py-1.5 font-mono text-sm placeholder:font-sans placeholder:text-muted aria-[invalid=true]:border-loss"
        />
        <button
          type="submit"
          disabled={account === null}
          className="rounded-lg border border-border bg-surface-2 px-3 py-1.5 text-sm enabled:hover:border-fg/30 enabled:cursor-pointer disabled:opacity-50"
        >
          Check
        </button>
      </form>
      {asked !== null && (
        <Query state={state} onRetry={refetch}>
          {(frozen) => (frozen === null ? null : <FrozenAnswer frozen={frozen} />)}
        </Query>
      )}
    </Card>
  );
}
