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

/** The one public contact address: for users, developers and investors alike. */
export const CONTACT_EMAIL = "info@orama.network";

/**
 * The investor page as a PDF, printed from the built page at build time by
 * scripts/build-pdf.mjs and served next to it.
 */
export const INVESTOR_PDF = {
  page: "/investors",
  path: "/orama-network-investors.pdf",
} as const;

export const APP_LINKS = {
  anchat: "https://anchat.io",
  anchatIos: "https://testflight.apple.com/join/GzQ2gvx4",
  anchatAndroid:
    "https://play.google.com/store/apps/details?id=debros.anchat_lite",
  rootwallet: "https://rootwallet.io",
} as const;
