import { defineConfig } from "tsup";

export default defineConfig({
  // The chain module is its own entry (@debros/orama/chain): its generated
  // protobuf codecs are large, and a caller that only uses the database or
  // pubsub clients should not carry them.
  entry: { index: "src/index.ts", chain: "src/chain/index.ts" },

  // Both module formats.
  //
  // The package was ESM-only while its README promised isomorphic use, so
  // `require("@debros/orama")` failed outright: Jest without ESM support,
  // ts-node in CJS mode, and CJS server code in a Next.js app all hit
  // ERR_REQUIRE_ESM at import. The exports map names the .cjs build under
  // "require" and the .js build under "import".
  format: ["esm", "cjs"],

  dts: true,
  sourcemap: true,
  clean: true,

  // shims injects the import.meta.url / __dirname equivalents each format
  // lacks, so one source builds to both without conditional code.
  shims: true,

  outDir: "dist",
});
