/**
 * Every public page: its path, its label in the navigation, and the title and
 * description used for the browser tab, search results and link previews.
 * The router, the prerenderer and the sitemap all read this list.
 */
export interface RouteMeta {
  path: string;
  /** Label in the top navigation; omitted pages are reached from elsewhere. */
  nav?: string;
  /** Short name, used in menus and links. */
  title: string;
  /**
   * The browser-tab and search-result title, when it should say more than the
   * short name. Kept so the full title stays within ~60 characters.
   */
  headTitle?: string;
  description: string;
}

export const ROUTES = {
  home: {
    path: "/",
    title: "Orama Network: the cloud, with nobody in the middle",
    description:
      "Everything an app needs (database, storage, hosting, functions, calls, notifications), built for machines run by independent people, not one giant company.",
  },
  platform: {
    path: "/platform",
    nav: "Platform",
    title: "Platform",
    headTitle: "Hosting, database, storage & serverless",
    description:
      "Hosting, database, cache, storage, real-time messaging, serverless functions, domains, calls, push notifications and wallet login, all live on Orama.",
  },
  howItWorks: {
    path: "/how-it-works",
    nav: "How it works",
    title: "How it works",
    headTitle: "How a decentralized cloud works",
    description:
      "Independent machines, one encrypted network, and a private cluster for every app. Orama explained in five pictures.",
  },
  useCases: {
    path: "/use-cases",
    nav: "Use cases",
    title: "Use cases",
    headTitle: "Use cases for a decentralized cloud",
    description:
      "Private messaging, free speech, Web3 backends, de-Googled phones, remote and satellite-connected places: what Orama makes possible.",
  },
  apps: {
    path: "/apps",
    nav: "Apps",
    title: "Apps",
    headTitle: "Apps: AnChat & RootWallet",
    description:
      "AnChat, the private messenger running on Orama, and RootWallet, the key and login app for the network.",
  },
  roadmap: {
    path: "/roadmap",
    nav: "Roadmap",
    title: "Roadmap",
    headTitle: "Roadmap: stable network, OramaOS, Orama One",
    description:
      "From working proof of concept to a stable network, OramaOS, and Orama One: node hardware in people's hands.",
  },
  investors: {
    path: "/investors",
    title: "Investors",
    headTitle: "Invest in Orama Network and RootWallet",
    description:
      "€1.5M equity round for one Swiss company with two products: Orama Network, the cloud, and RootWallet, the keys. No token.",
  },
  donate: {
    path: "/donate",
    title: "Donate",
    headTitle: "Donate to Orama: BTC, XMR, ETH, SOL",
    description:
      "Orama is open source. Donations in Bitcoin, Monero, Ethereum or Solana fund its development.",
  },
  whitepaper: {
    path: "/whitepaper",
    title: "Whitepaper",
    headTitle: "Whitepaper: how the Orama cloud works",
    description:
      "The Orama Network whitepaper: what the network is, how it works, and what runs on it today.",
  },
} as const satisfies Record<string, RouteMeta>;

export type RouteKey = keyof typeof ROUTES;

export const ROUTE_LIST: RouteMeta[] = Object.values(ROUTES);

export const NAV_ROUTES: RouteMeta[] = ROUTE_LIST.filter((r) => "nav" in r);

/**
 * "/platform/" and "/platform" are the same page. Self-contained on purpose:
 * scripts/prerender.mjs inlines this function's source into every page, so
 * the pre-hydration guard and src/main.tsx can never normalise differently.
 */
export function normalizePath(p: string): string {
  return p.length > 1 ? p.replace(/\/+$/, "") || "/" : p;
}

/** The docs are kept but unlisted: reachable from the footer only. */
export const DOCS_PATH = "/docs";

export function documentTitle(route: RouteMeta): string {
  const name = route.headTitle ?? route.title;
  return route.path === "/" ? name : `${name} · Orama Network`;
}
