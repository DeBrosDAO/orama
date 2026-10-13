import { useState } from "react";
import type { FormEvent } from "react";
import { useQuery } from "../../data/use-query";
import { parseAccount } from "../../model/lookup";
import type { Vote } from "../../model/governance";
import { Card } from "../../ui/card";
import { Query } from "../../ui/query-states";

const OPTION_TEXT: Record<Vote["option"], string> = { yes: "voted yes", no: "voted no", abstain: "abstained", unspecified: "has a vote with no option" };

/** What the chain holds for one address's vote. */
export function VoteAnswer({ vote }: { vote: Vote | null }) {
  return (
    <p role="status" className="mt-3 text-sm">
      {vote === null ? "The chain holds no vote from this address on this proposal." : `This address ${OPTION_TEXT[vote.option]}.`}
    </p>
  );
}

/** Looks up how one address voted. The chain lists no voters, so the address has to be named. */
export function VoteLookup({ proposalId }: { proposalId: number }) {
  const [text, setText] = useState("");
  const [asked, setAsked] = useState<string | null>(null);
  const voter = parseAccount(text.trim());
  const invalid = text.trim() !== "" && voter === null;
  const { state, refetch } = useQuery((s) => (asked === null ? Promise.resolve(null) : s.assets.getVote(proposalId, asked).then((vote) => ({ vote }))), [proposalId, asked]);

  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (voter !== null) setAsked(voter);
  };

  return (
    <Card title="How did an address vote?">
      <form onSubmit={submit} className="flex items-center gap-2">
        <label htmlFor="vote-voter" className="sr-only">
          Voter address
        </label>
        <input
          id="vote-voter"
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
          disabled={voter === null}
          className="rounded-lg border border-border bg-surface-2 px-3 py-1.5 text-sm enabled:hover:border-fg/30 enabled:cursor-pointer disabled:opacity-50"
        >
          Look up
        </button>
      </form>
      {asked !== null && (
        <Query state={state} onRetry={refetch}>
          {(answer) => (answer === null ? null : <VoteAnswer vote={answer.vote} />)}
        </Query>
      )}
    </Card>
  );
}
