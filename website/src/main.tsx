import { StrictMode } from "react";
import { createRoot, hydrateRoot } from "react-dom/client";
import { BrowserRouter } from "react-router";
// Fonts are bundled with the site, not fetched from a third party: a
// privacy-first network shouldn't hand every visitor's IP to Google.
import "@fontsource-variable/inter";
import "@fontsource-variable/inter-tight";
import "@fontsource-variable/jetbrains-mono";
import "./index.css";
import { App } from "./app";
import { normalizePath } from "./content/routes";

const container = document.getElementById("root");
if (!container) throw new Error("main: #root element missing from index.html");

const app = (
  <StrictMode>
    <BrowserRouter>
      <App />
    </BrowserRouter>
  </StrictMode>
);

// Every public page ships as prerendered HTML stamped with the path it was
// rendered for. Hydrate only when that matches where we are: the static host
// answers unknown paths (the docs, a 404) with the home page's HTML, and
// hydrating that as another page would be a mismatch.
if (container.dataset.prerendered === normalizePath(window.location.pathname)) {
  hydrateRoot(container, app);
} else {
  container.replaceChildren();
  createRoot(container).render(app);
}
