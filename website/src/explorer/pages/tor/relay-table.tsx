import { isLive } from "../../model/nodes";
import type { NodeSet } from "../../model/nodes";
import { relayRows } from "../../model/tor";
import type { RelayRow, TorRelays } from "../../model/tor";
import { cn } from "../../../lib/utils";
import { Amount } from "../../ui/amount";
import { Badge } from "../../ui/badge";
import { Card } from "../../ui/card";
import { CopyChip } from "../../ui/copy";
import { NodeLink, WalletLink } from "../../ui/links";
import { CutNotice } from "../../ui/not-on-chain";
import { StatusBadge } from "../nodes/badges";

const HEAD_CELL = "pb-2 pr-3 font-medium";
const CELL = "py-2.5 pr-3 align-middle";
const FINGERPRINT_SHOWN = 12;

export function shortFingerprint(fingerprint: string): string {
  return fingerprint.length <= FINGERPRINT_SHOWN ? fingerprint : `${fingerprint.slice(0, FINGERPRINT_SHOWN)}…`;
}

function Row({ row }: { row: RelayRow }) {
  const { relay, node } = row;
  return (
    <tr className="border-t border-border/60 first:border-t-0 hover:bg-surface-2">
      <th scope="row" className={cn(CELL, "pl-2 text-left font-normal")}>
        <span className="inline-flex items-center gap-2">
          <span className="font-mono text-[0.92em]" title={relay.fingerprint}>{shortFingerprint(relay.fingerprint)}</span>
          <CopyChip value={relay.fingerprint} label="Copy the relay fingerprint" />
        </span>
      </th>
      <td className={CELL}>
        <NodeLink id={relay.nodeId} />
      </td>
      <td className={cn(CELL, "hidden md:table-cell")}>
        <WalletLink wallet={{ address: relay.operator }} bare />
      </td>
      <td className={cn(CELL, "hidden font-mono text-sm sm:table-cell")}>{relay.prefix16}</td>
      <td className={CELL}>
        <span className="flex flex-wrap gap-1">
          {relay.exit && <Badge tone="signal">declares exit</Badge>}
          {relay.jailed && <Badge tone="bad">jailed</Badge>}
          {!relay.exit && !relay.jailed && <span className="text-muted">relay</span>}
        </span>
      </td>
      <td className={cn(CELL, "hidden lg:table-cell")}>{node === null ? <span className="text-muted">–</span> : <StatusBadge status={node.status} />}</td>
      <td className={CELL}>{row.paid === null ? <span className="text-muted">–</span> : <Amount norama={row.paid} maxFraction={2} />}</td>
    </tr>
  );
}

/** Every relay x/relay holds, with the node it runs on. */
export function RelayTable({ tor, nodes }: { tor: TorRelays; nodes: NodeSet | null }) {
  const rows = relayRows(tor.relays, nodes?.nodes ?? [], tor.payouts);
  const live = nodes === null || nodes.truncated ? null : rows.filter((r) => r.node !== null && isLive(r.node)).length;
  return (
    <Card title="Registered relays">
      {tor.truncated && <CutNotice shown={tor.relays.length} what="relays" />}
      {rows.length === 0 ? (
        <p className="py-4 text-center text-sm text-muted">No relay is registered on this chain yet.</p>
      ) : (
        <>
          <p className="mb-3 text-sm text-muted">
            {rows.length} relay{rows.length === 1 ? "" : "s"} registered
            {live === null ? "" : `, ${live} on a node that still serves`}. A relay joins by proving its Tor identity belongs to a node, and each IPv4 /16 earns at most a
            fixed amount, so many relays in one network share one allowance. The last column is what the newest settled epoch paid the relay before its operator&apos;s
            split; a dash is a relay that epoch paid nothing.
          </p>
          <table className="w-full border-collapse text-left">
            <caption className="sr-only">Registered Tor relays: identity fingerprint, node, operator, network, whether the operator declares it an exit or it is jailed, and what the newest settled epoch paid it</caption>
            <thead>
              <tr className="text-[11px] uppercase tracking-[0.07em] text-muted">
                <th scope="col" className={cn(HEAD_CELL, "pl-2")}>Fingerprint</th>
                <th scope="col" className={HEAD_CELL}>Node</th>
                <th scope="col" className={cn(HEAD_CELL, "hidden md:table-cell")}>Operator</th>
                <th scope="col" className={cn(HEAD_CELL, "hidden sm:table-cell")}>Network (/16)</th>
                <th scope="col" className={HEAD_CELL}>Kind</th>
                <th scope="col" className={cn(HEAD_CELL, "hidden lg:table-cell")}>Node status</th>
                <th scope="col" className={HEAD_CELL}>Paid</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((row) => (
                <Row key={row.relay.fingerprint} row={row} />
              ))}
            </tbody>
          </table>
        </>
      )}
    </Card>
  );
}
