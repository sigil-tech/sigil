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
        format: "es",
      },
    },
    // Chrome extensions don't need minification during development.
    minify: false,
    // Needed for service worker top-level await.
    target: "es2022",
  },
  resolve: {
    alias: {
      "@sigil/browser-shared": resolve(__dirname, "../browser-shared/src"),
    },
  },
});
