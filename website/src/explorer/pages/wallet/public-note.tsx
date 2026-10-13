import { Link } from "react-router";
import { Lock } from "lucide-react";
import { useExplorerPaths } from "../../network/active";

/**
 * Said on every wallet page: it covers public activity only. Value inside the shielded pool belongs to
 * no address, so it is not in these balances and a private transfer is not in this activity.
 */
export function PublicNote() {
  const paths = useExplorerPaths();
  return (
    <aside className="flex items-start gap-3 rounded-xl border border-border bg-surface p-4 text-[13px] text-muted" aria-label="What this page covers">
      <Lock size={15} aria-hidden="true" className="mt-0.5 shrink-0" />
      <p className="max-w-[85ch]">
        <b className="font-medium text-fg">This page shows public activity only.</b> Value held in the shielded pool belongs to no address, so it is not in
        these balances, and a private transfer is not listed here. Shielding and unshielding do appear, because the signer and the amount are public when
        value crosses between this address and the pool.{" "}
        <Link to={paths.assets.shielded} className="underline underline-offset-4 hover:text-fg">
          About the shielded pool
        </Link>
      </p>
    </aside>
  );
}
