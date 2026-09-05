/**
 * Deliberately narrow: rules that catch mistakes, not style. It mirrors the
 * app SDK's configuration so both packages fail on the same things.
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
    // Guardian replies are untyped JSON at the boundary.
    "@typescript-eslint/no-explicit-any": "off",
    // An unused argument named with a leading underscore is documentation.
    "@typescript-eslint/no-unused-vars": [
      "error",
      { argsIgnorePattern: "^_", varsIgnorePattern: "^_" },
    ],
  },
};
