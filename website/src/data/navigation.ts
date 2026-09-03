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

export const GITHUB_URL = "https://github.com/DeBrosDAO/orama";

export const NAV_LINKS: NavItem[] = [
  { label: "Docs", href: "/docs" },
  { label: "GitHub", href: GITHUB_URL, external: true },
];

export const FOOTER_COLUMNS: FooterColumn[] = [
  {
    title: "Documentation",
    links: [
      { label: "Getting Started", href: "/docs/developer/getting-started" },
      { label: "CLI Reference", href: "/docs/developer/cli-reference" },
      { label: "SDK Reference", href: "/docs/developer/sdk-reference" },
      { label: "Run a Node", href: "/docs/operator/getting-started" },
    ],
  },
  {
    title: "Project",
    links: [
      { label: "GitHub", href: GITHUB_URL, external: true },
      { label: "Contributing", href: "/docs/contributor/dev-setup" },
      { label: "Architecture", href: "/docs/contributor/architecture" },
    ],
  },
  {
    title: "Community",
    links: [
      { label: "X", href: "https://x.com/debrosofficial", external: true },
      { label: "Telegram", href: "https://t.me/debrosportal", external: true },
      { label: "DeBros", href: "https://debros.io", external: true },
    ],
  },
];

export const SOCIAL_LINKS: SocialLink[] = [
  { label: "GitHub", href: GITHUB_URL, icon: "github" },
  { label: "X", href: "https://x.com/debrosofficial", icon: "twitter" },
  { label: "Telegram", href: "https://t.me/debrosportal", icon: "send" },
  {
    label: "YouTube",
    href: "https://www.youtube.com/@DeBrosOfficial",
    icon: "youtube",
  },
];
