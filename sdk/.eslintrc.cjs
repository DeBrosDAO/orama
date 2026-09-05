/**
 * `pnpm lint` ran `eslint src tests` with no configuration anywhere in the
 * package, so ESLint 8 exited 2 with "No files matching the pattern" before it
 * linted anything. The script has never worked; the plugin and parser were in
 * devDependencies with nothing to load them.
 *
 * This is deliberately narrow: rules that catch mistakes, not style. Style is
 * not worth a hundred findings on a first run.
 */
module.exports = {
  root: true,
  env: { es2022: true, node: true, browser: true },
  parser: "@typescript-eslint/parser",
  parserOptions: { ecmaVersion: 2022, sourceType: "module" },
  plugins: ["@typescript-eslint"],
  extends: ["eslint:recommended", "plugin:@typescript-eslint/recommended"],
  ignorePatterns: ["dist", "node_modules", "*.cjs"],
  rules: {
    // The SDK's public surface uses `any` where the gateway's reply is
    // genuinely untyped JSON. Tightening that is its own change.
    "@typescript-eslint/no-explicit-any": "off",
    // An unused argument named with a leading underscore is documentation.
    "@typescript-eslint/no-unused-vars": [
      "error",
      { argsIgnorePattern: "^_", varsIgnorePattern: "^_" },
    ],
  },
};
