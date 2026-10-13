// Finds the Google Chrome or Chromium the build drives headless (the investor
// PDF, the link-preview images): CHROME_PATH, or the usual install locations.

import { existsSync } from "node:fs";

const CHROME_CANDIDATES = [
  "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
  "/Applications/Chromium.app/Contents/MacOS/Chromium",
  "/usr/bin/google-chrome",
  "/usr/bin/google-chrome-stable",
  "/usr/bin/chromium",
  "/usr/bin/chromium-browser",
];

/** The browser executable; `who` names the script in the error. */
export function findChrome(who) {
  const fromEnv = process.env.CHROME_PATH;
  if (fromEnv) {
    if (!existsSync(fromEnv)) throw new Error(`${who}: CHROME_PATH=${fromEnv} does not exist`);
    return fromEnv;
  }
  const found = CHROME_CANDIDATES.find((p) => existsSync(p));
  if (!found) {
    throw new Error(
      `${who}: no Chrome or Chromium found (looked in ${CHROME_CANDIDATES.join(", ")}). ` +
        "Install one, or set CHROME_PATH to its executable.",
    );
  }
  return found;
}
