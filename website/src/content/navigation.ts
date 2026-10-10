import { BLOG_PATH } from "../blog/posts";
import { DOCS_PATH, EXPLORER_PATH } from "./pages";
import { ROUTES } from "./routes";

/**
 * The top navigation: two menus, the blog, and the support button. The
 * desktop bar and the mobile menu both render this list.
 */

export interface NavItem {
  label: string;
  path: string;
  /** One line under the label in the open menu. */
  line: string;
}

export interface NavGroup {
  label: string;
  items: NavItem[];
}

export type NavEntry = NavGroup | NavItem;

export const isGroup = (e: NavEntry): e is NavGroup => "items" in e;

export const NAV: NavEntry[] = [
  {
    label: "Network",
    items: [
      { label: ROUTES.platform.title, path: ROUTES.platform.path, line: "Every service an app needs, live" },
      { label: ROUTES.howItWorks.title, path: ROUTES.howItWorks.path, line: "The network in five pictures" },
      { label: ROUTES.useCases.title, path: ROUTES.useCases.path, line: "What Orama makes possible" },
      { label: ROUTES.apps.title, path: ROUTES.apps.path, line: "AnChat and RootWallet" },
      { label: ROUTES.roadmap.title, path: ROUTES.roadmap.path, line: "Where the network goes next" },
      { label: "Explorer", path: EXPLORER_PATH, line: "The Orama chain, live" },
    ],
  },
  {
    label: "Information",
    items: [
      { label: ROUTES.whitepaper.title, path: ROUTES.whitepaper.path, line: "How the Orama cloud works" },
      { label: "Documentation", path: DOCS_PATH, line: "Build on Orama, run a node" },
    ],
  },
  { label: "Blog", path: BLOG_PATH, line: "Articles from the team" },
];

/** The call-to-action button: the donate page, labelled for what a visitor does there. */
export const SUPPORT_LINK = { label: "Support", path: ROUTES.donate.path };

/** Whether pathname is the item's page or a page under it. */
export function isActivePath(pathname: string, path: string): boolean {
  if (path === "/") return pathname === "/";
  return pathname === path || pathname.startsWith(`${path}/`);
}

export function isActiveEntry(pathname: string, entry: NavEntry): boolean {
  return isGroup(entry) ? entry.items.some((i) => isActivePath(pathname, i.path)) : isActivePath(pathname, entry.path);
}
