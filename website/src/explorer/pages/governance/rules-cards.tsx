import type { EnactedState, GovernanceParams } from "../../model/governance";
import { durationText } from "../../model/governance";
import { formatPct } from "../../model/units";
import { Amount } from "../../ui/amount";
import { Card } from "../../ui/card";
import { Absent, Facts } from "../../ui/facts";
import { WalletLink } from "../../ui/links";

/** The rules proposals are decided by. The chain fixes some at genesis; a proposal may change the rest. */
export function ParamsCard({ params }: { params: GovernanceParams }) {
  return (
    <Card title="How proposals are decided">
      <Facts
        rows={[
          ["Voting period", durationText(params.votingPeriodSeconds)],
          ["Token house quorum", `${formatPct(params.tokenQuorum)} of bonded stake must take part`],
          ["Token house threshold", `${formatPct(params.tokenPassThreshold)} of the weight taking part must vote yes`],
          ["House bond", <Amount norama={params.houseBond} />],
          ["Smallest operator house", `${params.minHouseSize} eligible operators`],
          ["Eligible operators per network", `at most ${params.maxEligiblePerPrefix16} in one /16 network and ${params.maxEligiblePerAsn} in one ASN`],
          ["Veto window", `${durationText(params.vetoWindowSeconds)}, in which the operator house may veto a passed parameter change`],
          ["Execution delays", `${durationText(params.parameterTimelockSeconds)} for a parameter change, ${durationText(params.upgradeTimelockSeconds)} for an upgrade, ${durationText(params.spendTimelockSeconds)} for a spend`],
          ["Bonded stake that opens parameter proposals", <Amount norama={params.bootstrapExitStake} />],
        ]}
      />
    </Card>
  );
}

function listOf(addresses: readonly string[]) {
  if (addresses.length === 0) return <Absent>None</Absent>;
  return (
    <ul className="space-y-1">
      {addresses.map((a) => (
        <li key={a}>
          <WalletLink wallet={{ address: a }} />
        </li>
      ))}
    </ul>
  );
}

/** What enacted proposals have set, which other modules read. */
export function EnactedCard({ enacted }: { enacted: EnactedState }) {
  const split = enacted.emissionSplit;
  const upgrade = enacted.scheduledUpgrade;
  return (
    <Card title="Set by enacted proposals">
      <Facts
        rows={[
          ["Scheduled upgrade", upgrade === null ? <Absent>None</Absent> : `${upgrade.name} at block ${upgrade.height.toLocaleString("en-US")}`],
          [
            "Emission split",
            split === null ? (
              <Absent>The genesis split stands</Absent>
            ) : (
              `${split.validator}% validators, ${split.storage}% storage, ${split.relay}% relay, ${split.development}% development`
            ),
          ],
          ["Power bound M", enacted.powerBoundsActive ? "Activated" : <Absent>Not activated</Absent>],
          ["Relay reporters", listOf(enacted.relayReporters)],
          ["Code upload allow-list", listOf(enacted.codeUploadAllow)],
          ["Adapter allow-list", listOf(enacted.adapterAllow)],
        ]}
      />
    </Card>
  );
}
