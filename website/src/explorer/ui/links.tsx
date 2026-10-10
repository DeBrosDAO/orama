import { Link } from "react-router";
import type { ValidatorRef, WalletRef } from "../model/types";
import { explorerPaths } from "../model/routes";
import { shortAddress } from "../model/units";
import { cn } from "../../lib/utils";
import { Identicon } from "./identicon";

const linkClass =
  "relative z-10 pointer-events-auto inline-flex items-center gap-1.5 align-baseline border-b border-dotted border-fg/30 hover:border-fg/80 transition-colors";

export interface WalletLinkProps {
  wallet: WalletRef;
  className?: string;
  /** Hide the identicon (dense tables). */
  bare?: boolean;
}

/** A wallet as a name (or short address) with its identicon; opens the wallet page. */
export function WalletLink({ wallet, className, bare = false }: WalletLinkProps) {
  const named = wallet.label !== undefined;
  return (
    <Link
      to={explorerPaths.wallet(wallet.address)}
      title={wallet.address}
      className={cn(linkClass, "text-fg font-medium", className)}
    >
      {!bare && <Identicon seed={wallet.address} size={16} />}
      <span className={cn(!named && "font-mono text-[0.92em]")}>{wallet.label ?? shortAddress(wallet.address)}</span>
    </Link>
  );
}

export function ValidatorLink({ validator, className }: { validator: ValidatorRef; className?: string }) {
  return (
    <Link to={explorerPaths.wallet(validator.operator)} className={cn(linkClass, "text-fg font-medium", className)}>
      <Identicon seed={validator.operator} size={16} />
      {validator.moniker}
    </Link>
  );
}

export function BlockLink({ height, className }: { height: number; className?: string }) {
  return (
    <Link to={explorerPaths.block(height)} className={cn("font-mono hover:text-fg underline decoration-dotted underline-offset-4", className)}>
      {height.toLocaleString("en-US")}
    </Link>
  );
}
