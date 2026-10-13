import type { WalletBalance } from "../../model/types";
import { Amount } from "../../ui/amount";
import { Card } from "../../ui/card";
import { BALANCE_FRACTION } from "./constants";
import { SplitBar } from "./split-bar";

export function BalanceCard({ balance }: { balance: WalletBalance }) {
  return (
    <Card title="Balance">
      <div className="flex flex-wrap items-baseline gap-x-2.5">
        <span className="text-[32px] font-semibold leading-tight tracking-tight">
          <Amount norama={balance.total} bare maxFraction={BALANCE_FRACTION} className="font-sans" />
        </span>
        <span className="text-sm text-muted">ORAMA total</span>
      </div>
      <SplitBar balance={balance} />
    </Card>
  );
}
