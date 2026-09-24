/**
 * Links, addresses and identity for the site. Every outbound link lives here
 * so a changed handle or email is a one-line edit.
 */

export const SITE_URL = "https://orama.network";
export const SITE_NAME = "Orama Network";

export const GITHUB_URL = "https://github.com/DeBrosDAO/orama";
export const X_URL = "https://x.com/orama_network";
/** Orama's public group on AnChat. */
export const ANCHAT_GROUP_URL = "https://g.anchat.io/orama";

export const EMAILS = {
  info: "info@orama.network",
  dev: "dev@orama.network",
  support: "support@orama.network",
  team: "team@orama.network",
} as const;

/** Where investors are pointed. */
export const INVESTOR_EMAIL = EMAILS.team;

export interface ContactEmail {
  label: string;
  address: string;
}

export const CONTACT_EMAILS: ContactEmail[] = [
  { label: "General", address: EMAILS.info },
  { label: "Developers", address: EMAILS.dev },
  { label: "Support", address: EMAILS.support },
  { label: "Team & investors", address: EMAILS.team },
];

export const APP_LINKS = {
  anchat: "https://anchat.io",
  anchatIos: "https://testflight.apple.com/join/GzQ2gvx4",
  anchatAndroid:
    "https://play.google.com/store/apps/details?id=debros.anchat_lite",
  rootwallet: "https://rootwallet.io",
} as const;
