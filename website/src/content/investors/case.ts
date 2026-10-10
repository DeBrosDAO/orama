import { FUNDING_TOTAL_EUR, PAID_BETA_MONTH, formatEurShort } from "../funding";
import { COMPANY } from "./products";

/** Why the company can win, what could go wrong, and the questions investors ask. */

export const MOAT = [
  { title: "The whole backend", line: "Database, functions, storage, calls and push on one network. Decentralized rivals each do one piece." },
  { title: "Standard code", line: "Node, Go and Next.js run as they are. No new language, unlike ICP." },
  { title: "No token", line: "Customers pay in money for a service they use. No speculation, no token to prop up." },
  { title: "Keys and cloud together", line: "Orama's command line signs in with RootWallet, and every app can embed it. Neither product is easy to copy alone." },
  { title: "Nothing in the cloud", line: "RootWallet keeps every secret on the device. Cloud password managers would have to rebuild." },
  { title: "Already in use", line: "AnChat runs its whole backend on Orama and gives every user a RootWallet." },
] as const;

export const RISKS = [
  { risk: "App data sits on other people's machines", answer: "Today every node is run by our team. OramaOS adds an encrypted disk with no remote login, then trust tiers let apps choose certified or EU-only nodes." },
  { risk: "Big clouds are cheaper at scale", answer: "We don't sell against their price. We sell what they can't offer: no single owner, privacy by default, EU data residency." },
  { risk: "Too much to build", answer: `We lead with three services (database and functions, real-time and calls, push), and the round pays ${COMPANY.hires}.` },
  { risk: "Hardware is expensive", answer: "This round buys a small, pre-order-funded first edition. The custom design waits for the next round." },
  { risk: "A security incident", answer: "Independent audits of the network and the wallet are funded by this round. RootWallet never holds user funds." },
  { risk: "A crypto downturn", answer: "Orama earns from developers, not crypto prices. RootWallet's Premium plan will earn in any market." },
] as const;

export const FAQ: { q: string; a: string }[] = [
  {
    q: "What exactly are you raising?",
    a: `${formatEurShort(FUNDING_TOTAL_EUR)} in equity for one company that will own Orama Network and RootWallet. No token, no loans. The company is a ${COMPANY.form}, and both products' code and rights are assigned to it.`,
  },
  {
    q: "Why one company for two products?",
    a: "They feed each other. RootWallet is how people sign in to Orama, and every app on Orama can give its users a RootWallet. One team, one legal setup, two ways to earn.",
  },
  { q: "Is there a token?", a: "No. This is an equity round and there is no token." },
  {
    q: "Who is behind it?",
    a: `${COMPANY.team}, who built both products. Ask us for the full background.`,
  },
  {
    q: "When does revenue start?",
    a: `Orama's paid beta opens ${PAID_BETA_MONTH} months after funding. RootWallet earns from its public launch in Q1 2027.`,
  },
  {
    q: "Why so little money compared with Supabase or Vercel?",
    a: "The hard part is built and running. This round turns it into paying customers and proves the numbers for a larger round.",
  },
  {
    q: "Are you selling nodes that earn money?",
    a: "No. Orama One is a product: your own private cloud box. Joining the network is optional, and we make no earnings promises.",
  },
  {
    q: "How is this different from Akash or ICP?",
    a: "Akash sells compute only. ICP requires its own runtime and token. Orama is the whole backend for standard code, paid in money.",
  },
  {
    q: "Is it open source?",
    a: "Orama Network is open source under AGPL-3.0. RootWallet's source is available to investors on request.",
  },
  {
    q: "Is RootWallet regulated?",
    a: "RootWallet is non-custodial: users hold their own keys and funds, and it never takes custody of either.",
  },
  {
    q: "Has it been audited?",
    a: "Both products have been through internal security reviews. Independent audits are funded by this round.",
  },
  {
    q: "Who could buy the company?",
    a: "Cloud and infrastructure companies buy developer platforms, and wallet and payments companies buy wallet teams: Stripe bought Privy, Fireblocks bought Dynamic.",
  },
];

/** Shown beside the round terms and at the foot of the page. */
export const DISCLAIMER =
  "This page is information, not an offer or solicitation to buy securities in any jurisdiction. Any investment happens only " +
  "through definitive documents, with professional or qualified investors. Revenue figures are illustrations built on " +
  "stated assumptions; they are forward-looking and may not happen.";
