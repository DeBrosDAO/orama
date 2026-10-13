import { Card } from "../../ui/card";

const PUBLIC = [
  "That a transaction is a shield, an unshield or a private transfer, and that the chain accepted or refused it.",
  "For a shield or an unshield: who signed it and the amount that crossed between the public side and the pool.",
  "For a private transfer: its fee, the number of actions in it, the size of its proof, the block and the time.",
  "The pool as a whole: what its account holds, the size of its note tree and how many nullifiers are recorded.",
];

const PRIVATE = [
  "Who sent a private transfer, who received it, and how much moved.",
  "Which notes a transfer or an unshield spent, and which earlier shield or transfer funded them.",
  "Who owns value inside the pool, and how much any address holds there.",
];

/** What a reader can and cannot learn, said once on the page about the pool. */
export function VisibilityCard() {
  return (
    <Card title="What is public and what is private">
      <p className="max-w-[75ch] text-sm text-muted">
        Sending ORAMA is public unless the sender chooses the pool: a bank send shows its sender, receiver and amount. The shielded pool is the
        private way to hold and move ORAMA. The explorer shows only what the chain makes public and implies nothing more.
      </p>
      <div className="mt-4 grid gap-5 md:grid-cols-2">
        <div>
          <h3 className="mb-2 text-xs font-medium text-muted">On chain</h3>
          <ul className="list-disc space-y-1.5 pl-5 text-sm">
            {PUBLIC.map((line) => (
              <li key={line}>{line}</li>
            ))}
          </ul>
        </div>
        <div>
          <h3 className="mb-2 text-xs font-medium text-muted">Not on chain</h3>
          <ul className="list-disc space-y-1.5 pl-5 text-sm">
            {PRIVATE.map((line) => (
              <li key={line}>{line}</li>
            ))}
          </ul>
        </div>
      </div>
    </Card>
  );
}
