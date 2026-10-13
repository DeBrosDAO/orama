import js from "@eslint/js";
import globals from "globals";
import tseslint from "typescript-eslint";

/**
 * Deliberately narrow: rules that catch mistakes, not style. Style is not
 * worth a hundred findings on a first run.
 */
export default tseslint.config(
  { ignores: ["dist", "node_modules", "**/*.cjs"] },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  {
    languageOptions: {
      ecmaVersion: 2022,
      sourceType: "module",
      globals: { ...globals.es2022, ...globals.node, ...globals.browser },
    },
    rules: {
      // Guardian replies are untyped JSON at the boundary.
      "@typescript-eslint/no-explicit-any": "off",
      // An unused argument named with a leading underscore is documentation.
      "@typescript-eslint/no-unused-vars": [
        "error",
        { argsIgnorePattern: "^_", varsIgnorePattern: "^_" },
      ],
    },
  },
);
