import { Link } from "react-router";
import { groupedInteger } from "../../model/paging";
import type { ShieldedPool } from "../../model/shielded";
import { SHIELDED_POOL_ADDRESS } from "../../model/shielded";
import { useExplorerPaths } from "../../network/active";
import { Amount } from "../../ui/amount";
import { Card } from "../../ui/card";
import { Help } from "../../ui/help";
import { Stat } from "../../ui/stat";
import { AMOUNT_FRACTION } from "./constants";

const HELD_TIP = "The pool's coins sit in one module account. It also holds the coins behind unshields that wait in the queue, so this is the pool plus that queue.";
const COMMITMENTS_TIP = "Every action of every accepted bundle adds one note commitment to the pool's tree. An action may carry no real value, so this is not a count of notes or of payments.";
const NULLIFIERS_TIP = "Each action of an accepted bundle records one nullifier. The chain keeps them all so that a note cannot be spent twice.";

/** What the chain exposes of the pool as a whole. */
export function PoolStats({ pool }: { pool: ShieldedPool }) {
  const paths = useExplorerPaths();
  return (
    <div className="grid gap-4 sm:grid-cols-3">
      <Stat
        label={
          <>
            Held by the pool
            <Help tip={HELD_TIP} />
          </>
        }
        value={<Amount norama={pool.held} maxFraction={AMOUNT_FRACTION} />}
        detail={
          <Link to={paths.wallet(SHIELDED_POOL_ADDRESS)} className="underline underline-offset-4 hover:text-fg">
            the pool's account
          </Link>
        }
      />
      <Stat
        label={
          <>
            Note commitments
            <Help tip={COMMITMENTS_TIP} />
          </>
        }
        value={groupedInteger(pool.noteCommitments)}
        detail="added to the pool's tree"
      />
      <Stat
        label={
          <>
            Nullifiers
            <Help tip={NULLIFIERS_TIP} />
          </>
        }
        value={groupedInteger(pool.nullifiers)}
        detail="recorded as spent"
      />
    </div>
  );
}

/** The rules that bound the pool, as the module states them, with the figures the chain's parameters set. */
export function RulesCard({ pool }: { pool: ShieldedPool }) {
  const { params } = pool;
  return (
    <Card title="Rules that protect the pool">
      <ul className="list-disc space-y-1.5 pl-5 text-sm">
        <li>
          Turnstile: the pool cannot pay out more than went in. This is a rule of x/shielded, not a figure this page measures: the gateway withholds
          the query that would split the pool from the unshield queue.
        </li>
        <li>
          24-hour cap: the net amount leaving the pool in a rolling day is at most 2% of the pool, or <Amount norama={params.unshieldFloor} /> if that is
          larger. Unshields into a bond that do not fit wait in a queue.
        </li>
        <li>
          Unshielding goes only to the signer's own staking bond, node bond or fee balance, at most <Amount norama={params.maxFeeTopup} /> for fees in one
          transaction, so the pool is not a way to pay someone else.
        </li>
        <li>
          Each action of a bundle burns a nullifier fee of <Amount norama={params.nullifierFee} />; a bundle holds at most {params.maxActionsPerBundle} actions.
        </li>
      </ul>
    </Card>
  );
}
