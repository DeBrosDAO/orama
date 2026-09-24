import { SERVICES } from "../services";
import { ROUTES } from "../routes";
import { ANCHAT_GROUP_URL, APP_LINKS, GITHUB_URL } from "../site";

/**
 * One company, two products. Orama traction was read from the live networks
 * (read-only) on 2026-09-24; RootWallet traction comes from the RootWallet
 * team. The team is described in general terms only: no names.
 */
export const COMPANY = {
  name: "Orama Network",
  form: "Swiss AG in Zug, formed when the round closes",
  team: "A team of three engineers",
  /** €700k of engineering over 18 months ≈ €39k a month ≈ seven people at ~€65k a year. */
  hires: "a team of about seven: the three of us plus about four new engineers",
} as const;

export interface ProductCard {
  id: "orama" | "rootwallet";
  name: string;
  role: string;
  line: string;
  traction: { value: string; label: string }[];
  asOf: string;
  links: ProductLink[];
}

export interface ProductLink {
  label: string;
  href: string;
  /** Opens another site in a new tab. */
  external: boolean;
}

export const PRODUCTS: ProductCard[] = [
  {
    id: "orama",
    name: "Orama Network",
    role: "The cloud",
    line: "Everything an app needs to run, built for machines no single company controls.",
    traction: [
      { value: String(SERVICES.length), label: "services live" },
      { value: "120", label: "serverless functions run AnChat's backend" },
      { value: "2.86M", label: "database writes by AnChat since July" },
      { value: "3/3", label: "test-network machines healthy, 78 days without a reboot" },
    ],
    asOf: "Measured on the live test network, 24 Sep 2026",
    links: [
      { label: "The platform", href: ROUTES.platform.path, external: false },
      { label: "Whitepaper", href: ROUTES.whitepaper.path, external: false },
      { label: "Source code", href: GITHUB_URL, external: true },
    ],
  },
  {
    id: "rootwallet",
    name: "RootWallet",
    role: "The keys",
    line: "Crypto, passwords, 2FA codes and server keys in one wallet that stays on your device.",
    traction: [
      { value: "~100", label: "beta users today, inside AnChat" },
      { value: "2", label: "products built on RootWallet" },
      { value: "Built in", label: "Orama's command line signs in with RootWallet" },
      { value: "Q1 2027", label: "public launch" },
    ],
    asOf: "Closed beta, September 2026",
    links: [{ label: "rootwallet.io", href: APP_LINKS.rootwallet, external: true }],
  },
];

/** How each product brings users to the other. */
export const FLYWHEEL = [
  { from: "RootWallet", to: "Orama", line: "Orama developers sign in with RootWallet: no account, no password." },
  { from: "Orama", to: "RootWallet", line: "Every app on Orama can embed RootWallet, so its users become wallet users." },
  { from: "AnChat", to: "Both", line: "Proof it works: an independent messenger runs on Orama and gives every user a RootWallet." },
] as const;

/** Answers "who are you?" with what the team built, not who they are. */
export const SHIPPED = [
  { value: "11", label: "cloud services live on Orama", derived: "services" },
  { value: "120", label: "serverless functions run AnChat's whole backend" },
  { value: "3", label: "RootWallet apps: desktop, terminal, and mobile in development" },
  { value: "1", label: "independent app already built on both: AnChat" },
] as const;

/** Everything an investor can open and try, in one place. */
export const SEE_FOR_YOURSELF: { name: string; line: string; links: ProductLink[] }[] = [
  {
    name: "Orama Network",
    line: "The cloud: what it does, how it works, and the code.",
    links: [
      { label: "orama.network", href: ROUTES.home.path, external: false },
      { label: "The platform", href: ROUTES.platform.path, external: false },
      { label: "Whitepaper", href: ROUTES.whitepaper.path, external: false },
      { label: "GitHub", href: GITHUB_URL, external: true },
    ],
  },
  {
    name: "RootWallet",
    line: "The keys: the wallet and vault that signs you in to Orama.",
    links: [{ label: "rootwallet.io", href: APP_LINKS.rootwallet, external: true }],
  },
  {
    name: "AnChat",
    line: "The proof: an independent messenger running on Orama, with a RootWallet for every user.",
    links: [
      { label: "anchat.io", href: APP_LINKS.anchat, external: true },
      { label: "iOS (TestFlight)", href: APP_LINKS.anchatIos, external: true },
      { label: "Android", href: APP_LINKS.anchatAndroid, external: true },
      { label: "Orama group on AnChat", href: ANCHAT_GROUP_URL, external: true },
    ],
  },
];
