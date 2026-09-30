import { defineConfig } from "tsup";

export default defineConfig({
  entry: ["src/index.ts"],

  // Both module formats, for the same reason the app SDK ships both: the
  // consumers are CLIs and operator tooling, not only bundled applications.
  format: ["esm", "cjs"],

  // tsup 8 injects the deprecated `baseUrl` into its declaration build;
  // TypeScript 6 rejects it unless the deprecation is acknowledged.
  dts: { compilerOptions: { ignoreDeprecations: "6.0" } },
  sourcemap: true,
  clean: true,
  shims: true,

  outDir: "dist",
});
