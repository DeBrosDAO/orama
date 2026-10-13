import type { WalletProfile } from "../../model/types";
import { SentenceParts } from "../../ui/sentence";
import { useNow } from "../../ui/use-now";
import { describeWallet } from "./describe-wallet";

/** The wallet in plain words: an amber-edged note above the detail. */
export function Insight({ profile }: { profile: WalletProfile }) {
  const now = useNow();
  return (
    <aside className="rounded-r-xl border-l-2 border-signal bg-gradient-to-r from-signal/10 to-transparent px-4 py-3 text-[15px] leading-relaxed">
      <h2 className="mb-0.5 text-[11px] font-bold uppercase tracking-[0.09em] text-signal">In plain words</h2>
      <p>
        <SentenceParts parts={describeWallet(profile, now)} />
      </p>
    </aside>
  );
}
