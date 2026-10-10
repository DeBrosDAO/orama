import { useState } from "react";
import { useParams } from "react-router";
import { useQuery } from "../data/use-query";
import type { WalletFilter, WalletProfile, WalletRef } from "../model/types";
import { shortAddress } from "../model/units";
import { Page } from "../ui/page";
import { NotFoundBox, Query } from "../ui/query-states";
import { useDocumentTitle } from "../ui/use-document-title";
import { ActivityCard } from "./wallet/activity-card";
import { BalanceCard } from "./wallet/balance-card";
import { COUNTERPARTY_LIMIT } from "./wallet/constants";
import { CounterpartiesCard } from "./wallet/counterparties-card";
import { Insight } from "./wallet/insight";
import { MoneyMapCard } from "./wallet/money-map-card";
import { useWalletActivity } from "./wallet/use-wallet-activity";
import { WalletHeader } from "./wallet/wallet-header";
import { walletParam } from "./wallet/wallet-param";
import { WalletSkeleton } from "./wallet/wallet-skeleton";

const NOT_FOUND_TITLE = "Wallet not found";
const NOT_A_WALLET_HINT =
  "That does not look like an Orama wallet address. Wallet addresses start with orama1; check it for typos or a missing character.";
const NOT_FOUND_HINT =
  "A wallet appears here after its first transaction. If you expected activity, check the address for typos.";

/** Everything that depends on the loaded profile. Keyed by address, so a new wallet starts with clean filters. */
function WalletView({ profile }: { profile: WalletProfile }) {
  const address = profile.ref.address;
  const [filter, setFilter] = useState<WalletFilter>("all");
  const [counterparty, setCounterparty] = useState<WalletRef | null>(null);
  const activity = useWalletActivity(address, filter, counterparty?.address ?? null);
  const counterparties = useQuery((s) => s.getCounterparties(address, COUNTERPARTY_LIMIT), [address]);

  const toggle = (who: WalletRef) => setCounterparty((cur) => (cur?.address === who.address ? null : who));
  const clearAll = () => {
    setFilter("all");
    setCounterparty(null);
  };

  return (
    <>
      <WalletHeader profile={profile} loaded={activity.items} />
      <Insight profile={profile} />
      <div className="grid items-start gap-4 lg:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]">
        <div className="space-y-4">
          <BalanceCard balance={profile.balance} />
          <ActivityCard
            activity={activity}
            filter={filter}
            onFilter={setFilter}
            counterparty={counterparty}
            onClearCounterparty={() => setCounterparty(null)}
            onClearAll={clearAll}
          />
        </div>
        <div className="space-y-4">
          <CounterpartiesCard state={counterparties.state} onRetry={counterparties.refetch} selected={counterparty} onToggle={toggle} />
          <MoneyMapCard state={counterparties.state} onRetry={counterparties.refetch} />
        </div>
      </div>
    </>
  );
}

/** Loads one wallet. Keyed by address by its caller, so it never shows another wallet's profile. */
function WalletLoader({ address }: { address: string }) {
  const { state, refetch } = useQuery((s) => s.getWallet(address), [address]);
  const profile = state.status === "ready" && state.data?.ref.address === address ? state.data : null;
  useDocumentTitle(profile?.ref.label ?? `Wallet ${shortAddress(address)}`);
  return (
    <Query state={state} onRetry={refetch} loading={<WalletSkeleton />}>
      {(loaded) =>
        loaded === null ? (
          <NotFoundBox title={NOT_FOUND_TITLE} hint={NOT_FOUND_HINT} />
        ) : (
          <WalletView key={loaded.ref.address} profile={loaded} />
        )
      }
    </Query>
  );
}

/** `/explorer/wallet/:address` */
export function WalletPage() {
  const address = walletParam(useParams().address);
  useDocumentTitle(address === null ? NOT_FOUND_TITLE : null);
  return (
    <Page>
      {address === null ? (
        <NotFoundBox title={NOT_FOUND_TITLE} hint={NOT_A_WALLET_HINT} />
      ) : (
        <WalletLoader key={address} address={address} />
      )}
    </Page>
  );
}
