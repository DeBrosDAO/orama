import { APP_LINKS } from "./site";

/**
 * The two apps shown. AnChat's whole backend runs on Orama (open beta, on the
 * test network). RootWallet does not run on Orama: it is how people sign in
 * to Orama and where node operators keep their keys; it keeps everything on
 * the device (no cloud backup). Only messages use post-quantum encryption,
 * and only one-to-one calls are end-to-end encrypted, so the copy says so.
 */
export interface AppShowcase {
  id: "anchat" | "rootwallet";
  name: string;
  mark: string;
  tagline: string;
  /** Short status chip. */
  status: string;
  /** What the app is, as 3–4 icon-sized facts. */
  facts: string[];
  /** Heading over the chips: how the app and Orama relate. */
  relation: string;
  /** What it does with Orama, as chips. */
  poweredBy: string[];
  /** A single standout number. */
  metric: { value: string; label: string };
  links: { label: string; href: string }[];
}

export const APPS: AppShowcase[] = [
  {
    id: "anchat",
    name: "AnChat",
    mark: "/images/apps/anchat-mark.png",
    tagline: "The private messenger that runs on Orama.",
    status: "Open beta · iOS & Android",
    facts: [
      "End-to-end encrypted chats and one-to-one calls",
      "Post-quantum encryption for messages",
      "No phone number, no email: log in with a wallet",
      "Calls never reveal your IP to the person you call",
    ],
    relation: "Uses Orama for",
    poweredBy: [
      "Serverless functions",
      "Database",
      "Real-time messaging",
      "File storage",
      "Voice & video calls",
      "Push notifications",
    ],
    metric: { value: "120", label: "serverless functions run AnChat's backend on Orama" },
    links: [
      { label: "anchat.io", href: APP_LINKS.anchat },
      { label: "iOS (TestFlight)", href: APP_LINKS.anchatIos },
      { label: "Android", href: APP_LINKS.anchatAndroid },
    ],
  },
  {
    id: "rootwallet",
    name: "RootWallet",
    mark: "/images/apps/rootwallet-mark.png",
    tagline: "Your keys, in one place. Your way into Orama.",
    status: "Closed beta · desktop & command line",
    facts: [
      "Crypto, passwords, SSH keys and 2FA codes in one wallet",
      "Ethereum, Solana, Bitcoin and more",
      "Everything stays on your device",
      "Signs your Orama login: no account, no password",
    ],
    relation: "Works with Orama as",
    poweredBy: ["Your Orama sign-in", "Node operators' SSH keys", "Release signing"],
    metric: { value: "0", label: "accounts or passwords needed to sign in to Orama" },
    links: [{ label: "rootwallet.io", href: APP_LINKS.rootwallet }],
  },
];

/** Screens shown on /apps, left to right; width and height are the files' own. */
export const ANCHAT_SCREENS = [
  { src: "/images/apps/anchat-login.jpg", width: 480, height: 912, alt: "AnChat sign-in with an EVM or Solana wallet" },
  { src: "/images/apps/anchat-chat.jpg", width: 480, height: 909, alt: "An AnChat conversation" },
  { src: "/images/apps/anchat-call.jpg", width: 480, height: 916, alt: "An AnChat call in progress" },
];
