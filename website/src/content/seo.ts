import type { RouteMeta } from "./routes";
import { ANCHAT_GROUP_URL, CONTACT_EMAIL, GITHUB_URL, SITE_NAME, SITE_URL, X_URL } from "./site";

/**
 * schema.org structured data for search engines, emitted as JSON-LD into
 * every prerendered page. Only facts that are true today go here.
 */

const ORGANIZATION = {
  "@type": "Organization",
  "@id": `${SITE_URL}/#organization`,
  name: SITE_NAME,
  url: `${SITE_URL}/`,
  logo: `${SITE_URL}/logo.png`,
  email: CONTACT_EMAIL,
  sameAs: [GITHUB_URL, X_URL, ANCHAT_GROUP_URL],
};

const WEBSITE = {
  "@type": "WebSite",
  "@id": `${SITE_URL}/#website`,
  name: SITE_NAME,
  url: `${SITE_URL}/`,
  publisher: { "@id": `${SITE_URL}/#organization` },
  inLanguage: "en",
};

const SOURCE_CODE = {
  "@type": "SoftwareSourceCode",
  name: SITE_NAME,
  codeRepository: GITHUB_URL,
  programmingLanguage: "Go",
  license: "https://www.gnu.org/licenses/agpl-3.0.html",
  url: `${SITE_URL}/`,
  publisher: { "@id": `${SITE_URL}/#organization` },
};

export function pageUrl(route: RouteMeta): string {
  return route.path === "/" ? `${SITE_URL}/` : `${SITE_URL}${route.path}`;
}

export function structuredData(route: RouteMeta, title: string): string {
  const page = {
    "@type": route.path === "/whitepaper" ? "TechArticle" : "WebPage",
    "@id": `${pageUrl(route)}#webpage`,
    url: pageUrl(route),
    name: title,
    description: route.description,
    isPartOf: { "@id": `${SITE_URL}/#website` },
    inLanguage: "en",
  };
  const graph = route.path === "/" ? [ORGANIZATION, WEBSITE, SOURCE_CODE, page] : [ORGANIZATION, WEBSITE, page];
  // "<" is escaped so no string in the data can close the <script> element.
  return JSON.stringify({ "@context": "https://schema.org", "@graph": graph }).replace(/</g, "\\u003c");
}
