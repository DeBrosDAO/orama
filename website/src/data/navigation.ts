export interface NavItem {
  label: string;
  href: string;
  external?: boolean;
}

export interface FooterColumn {
  title: string;
  links: NavItem[];
}

export interface SocialLink {
  label: string;
  href: string;
  icon: string;
}

export const NAV_LINKS: NavItem[] = [
  { label: "Blockchain", href: "/blockchain" },
  { label: "Compute", href: "/compute" },
  { label: "Investors", href: "/investors" },
  { label: "Waitlist", href: "/whitelist" },
  { label: "Whitepaper", href: "/whitepaper" },
];

export const MORE_LINKS: NavItem[] = [
  { label: "Contributors", href: "/contributors" },
  { label: "Documentation", href: "/docs" },
];

export const FOOTER_COLUMNS: FooterColumn[] = [
  {
    title: "Platform",
    links: [
      { label: "Compute", href: "/compute" },
      { label: "Blockchain", href: "/blockchain" },
      { label: "Investors", href: "/investors" },
      { label: "Contributors", href: "/contributors" },
    ],
  },
  {
    title: "Resources",
    links: [
      { label: "Documentation", href: "/docs" },
      { label: "Whitepaper", href: "/whitepaper" },
    ],
  },
  {
    title: "Community",
    links: [
      { label: "GitHub", href: "https://github.com/DeBrosDAO", external: true },
      { label: "X", href: "https://x.com/debrosofficial", external: true },
      { label: "Telegram", href: "https://t.me/debrosportal", external: true },
      { label: "AnChat", href: "https://anchat.io", external: true },
    ],
  },
];

export const SOCIAL_LINKS: SocialLink[] = [
  { label: "GitHub", href: "https://github.com/DeBrosDAO", icon: "github" },
  { label: "X", href: "https://x.com/debrosofficial", icon: "twitter" },
  { label: "Telegram", href: "https://t.me/debrosportal", icon: "send" },
  { label: "YouTube", href: "https://www.youtube.com/@DeBrosOfficial", icon: "youtube" },
];
