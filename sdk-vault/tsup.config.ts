import { defineConfig } from "tsup";

export default defineConfig({
  entry: ["src/index.ts"],

  // Both module formats, for the same reason the app SDK ships both: the
  // consumers are CLIs and operator tooling, not only bundled applications.
  format: ["esm", "cjs"],

  dts: true,
  sourcemap: true,
  clean: true,
  shims: true,

  outDir: "dist",
});
