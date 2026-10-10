import { useLocation } from "react-router";
import { Page } from "../components/layout/page";
import { EXPLORER_PATH } from "../content/pages";
import { normalizePath } from "../content/routes";
import { ExplorerApp } from "../explorer/explorer-app";

/**
 * The explorer keeps its own layout; Page only keeps the head in step. Its
 * front page is indexed; a block, transaction or wallet page is live data
 * with no lasting value in search, so it is kept out.
 */
export default function Explorer() {
  const isFront = normalizePath(useLocation().pathname) === EXPLORER_PATH;
  return (
    <Page route={{ path: EXPLORER_PATH, title: "Explorer", description: "" }} noindex={!isFront} breadcrumbs="none">
      <ExplorerApp />
    </Page>
  );
}
