import { useEffect, useRef, useState } from "react";
import { Link, NavLink, useLocation } from "react-router";
import { ChevronDown } from "lucide-react";
import type { AssetPaths } from "../model/assets-routes";
import { parseExplorerPath } from "../model/routes";
import { useExplorerPaths } from "../network/active";
import { cn } from "../../lib/utils";
import { closesMenu, focusLeftMenu } from "./menu-keys";

interface MenuItem {
  label: string;
  to: string;
  /** The page kinds that belong to this item, so a token's own page keeps "Tokens" lit. */
  kinds: readonly string[];
}

/** The pages for what the chain holds and decides, in the order a reader goes from money to rules. */
export function menuItems(paths: AssetPaths): MenuItem[] {
  return [
    { label: "Tokens", to: paths.tokens, kinds: ["tokens", "token"] },
    { label: "NFTs", to: paths.nfts, kinds: ["nfts", "nft", "collection"] },
    { label: "Contracts", to: paths.contracts, kinds: ["contracts", "code", "contract"] },
    { label: "Market", to: paths.market, kinds: ["market", "listing"] },
    { label: "Governance", to: paths.governance, kinds: ["governance", "proposal"] },
    { label: "Shielded pool", to: paths.shielded, kinds: ["shielded"] },
  ];
}

const PILL = "rounded-full px-3 py-1.5 text-[13px] transition-colors";
const ON = "bg-white/[0.07] text-fg";
const OFF = "text-muted hover:text-fg";

/**
 * One page of the menu. On its own page the link is the current page (`aria-current="page"`, which the
 * router sets); on a page inside it (a token's own page) it is lit and says the reader is within it.
 */
function ItemLink({ item, kind, classes }: { item: MenuItem; kind: string; classes: (lit: boolean) => string }) {
  const inside = item.kinds.includes(kind) && kind !== item.kinds[0];
  if (inside) {
    return (
      <Link to={item.to} aria-current="true" className={classes(true)}>
        {item.label}
      </Link>
    );
  }
  return (
    <NavLink to={item.to} className={({ isActive }) => classes(isActive)}>
      {item.label}
    </NavLink>
  );
}

/** The same pages as plain links, for the narrow header where a menu would be cut off by its scroller. */
function InlineLinks({ items, kind }: { items: readonly MenuItem[]; kind: string }) {
  return (
    <>
      {items.map((item) => (
        <ItemLink key={item.to} item={item} kind={kind} classes={(lit) => cn(PILL, "shrink-0", lit ? ON : OFF)} />
      ))}
    </>
  );
}

function Dropdown({ items, kind }: { items: readonly MenuItem[]; kind: string }) {
  const [open, setOpen] = useState(false);
  const root = useRef<HTMLDivElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const { pathname } = useLocation();
  const current = items.some((item) => item.kinds.includes(kind));

  useEffect(() => setOpen(false), [pathname]);
  useEffect(() => {
    if (!open) return;
    const onPointer = (event: PointerEvent) => {
      if (root.current && !root.current.contains(event.target as Node)) setOpen(false);
    };
    const onKey = (event: KeyboardEvent) => {
      if (!closesMenu(event.key)) return;
      setOpen(false);
      // A link inside the menu is about to disappear with it: focus goes back to the button that opened it.
      if (root.current?.contains(document.activeElement)) trigger.current?.focus();
    };
    document.addEventListener("pointerdown", onPointer);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("pointerdown", onPointer);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  return (
    <div ref={root} className="relative" onBlur={(event) => focusLeftMenu(root.current, event.relatedTarget) && setOpen(false)}>
      <button
        ref={trigger}
        type="button"
        aria-expanded={open}
        aria-controls={open ? "explorer-browse-menu" : undefined}
        onClick={() => setOpen((v) => !v)}
        className={cn(PILL, "inline-flex cursor-pointer items-center gap-1", current ? ON : OFF)}
      >
        Browse
        <ChevronDown size={13} aria-hidden="true" className={cn("transition-transform", open && "rotate-180")} />
      </button>
      {open && (
        <div id="explorer-browse-menu" className="absolute left-0 top-full z-50 mt-2 w-48 rounded-xl border border-border bg-surface p-1.5 shadow-xl">
          {items.map((item) => (
            <ItemLink
              key={item.to}
              item={item}
              kind={kind}
              classes={(lit) => cn("block rounded-lg px-3 py-2 text-[13px]", lit ? ON : "text-muted hover:bg-white/[0.04] hover:text-fg")}
            />
          ))}
        </div>
      )}
    </div>
  );
}

/** Tokens, NFTs, contracts, the market, governance and the shielded pool: a menu on a wide header, plain links on a narrow one. */
export function AssetsMenu({ inline = false }: { inline?: boolean }) {
  const paths = useExplorerPaths();
  const { pathname } = useLocation();
  const kind = parseExplorerPath(pathname).kind;
  const items = menuItems(paths.assets);
  return inline ? <InlineLinks items={items} kind={kind} /> : <Dropdown items={items} kind={kind} />;
}
