import type { TokenDetail } from "../../model/tokens";
import { heldPowers } from "../../model/tokens";
import { groupedInteger } from "../../model/paging";
import { useExplorerPaths } from "../../network/active";
import { Link } from "react-router";
import { Amount } from "../../ui/amount";
import { Badge } from "../../ui/badge";
import { Card } from "../../ui/card";
import { CopyChip } from "../../ui/copy";
import { Absent, Facts } from "../../ui/facts";
import { WalletLink } from "../../ui/links";

/** The token's name, symbol and denom, and the flags that change what can be done with it. */
export function TokenHeader({ token }: { token: TokenDetail }) {
  return (
    <header>
      <div className="flex flex-wrap items-center gap-2 text-[13px] text-muted">
        <span>Token</span>
        {token.paused && <Badge tone="bad">Paused</Badge>}
        {token.shieldable && <Badge tone="signal">Can enter the shielded pool</Badge>}
      </div>
      <h1 className="mt-1.5 font-display text-3xl font-semibold tracking-tight">
        {token.name} <span className="text-muted">({token.symbol})</span>
      </h1>
      <p className="mt-1 flex flex-wrap items-center gap-2 text-sm text-muted">
        <span className="font-mono text-xs [overflow-wrap:anywhere]">{token.denom}</span>
        <CopyChip value={token.denom} label="Copy the token denom" />
      </p>
    </header>
  );
}

/** What the chain records about the token. */
export function TokenFacts({ token }: { token: TokenDetail }) {
  return (
    <Card title="Token">
      <Facts
        rows={[
          ["Creator", <WalletLink wallet={{ address: token.creator }} />],
          ["Supply", <span className="font-mono tabular-nums">{groupedInteger(token.issued)}</span>],
          ["Description", token.description === "" ? <Absent>None written</Absent> : token.description],
          ["Metadata deposit", <Amount norama={token.depositAmount} />],
          ["Shielded pool", token.shieldable ? "May enter (a one-way setting)" : "May not enter"],
        ]}
      />
    </Card>
  );
}

/** The capabilities the token still carries: who can still change its supply or who may hold it, as the chain records them. */
export function PowersCard({ token }: { token: TokenDetail }) {
  const paths = useExplorerPaths();
  const powers = heldPowers(token.extensions);
  const { permanentDelegate, transferHook } = token.extensions;
  return (
    <Card title="Capabilities still held">
      {powers.length === 0 ? (
        <p className="text-sm text-muted">
          None. A capability is set when the token is created and can only be given up, never added, so this token's creator holds no special power over
          its supply or its holders.
        </p>
      ) : (
        <ul className="list-disc space-y-1 pl-5 text-sm">
          {powers.map((p) => (
            <li key={p}>{p}</li>
          ))}
        </ul>
      )}
      {(permanentDelegate !== null || transferHook !== null) && (
        <div className="mt-3">
          <Facts
            rows={[
              ["Permanent delegate", permanentDelegate === null ? undefined : <WalletLink wallet={{ address: permanentDelegate }} />],
              [
                "Transfer hook",
                transferHook === null ? undefined : (
                  <Link to={paths.assets.contract(transferHook)} className="font-mono text-xs underline decoration-dotted underline-offset-4">
                    {transferHook}
                  </Link>
                ),
              ],
            ]}
          />
        </div>
      )}
    </Card>
  );
}
