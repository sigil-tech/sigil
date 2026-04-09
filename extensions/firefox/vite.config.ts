import { defineConfig } from "vite";
import { resolve } from "path";

export default defineConfig({
  build: {
    outDir: "dist",
    emptyOutDir: true,
    rollupOptions: {
      input: {
        background: resolve(__dirname, "src/background.ts"),
        popup: resolve(__dirname, "src/popup.ts"),
      },
      output: {
        entryFileNames: "[name].js",
        // Firefox MV2 background scripts need IIFE format.
        format: "iife",
      },
    },
    minify: false,
    target: "es2022",
  },
  resolve: {
    alias: {
      "@sigil/browser-shared": resolve(__dirname, "../browser-shared/src"),
    },
  },
});
