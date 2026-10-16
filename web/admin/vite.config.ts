import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The build goes into the Go package that embeds it, with stable file names
// so that a rebuild changes only what changed.
export default defineConfig({
  plugins: [react()],
  base: "/admin/",
  build: {
    outDir: "../../internal/server/adminui",
    emptyOutDir: true,
    assetsDir: "assets",
    rollupOptions: {
      output: {
        entryFileNames: "assets/admin.js",
        chunkFileNames: "assets/[name].js",
        assetFileNames: "assets/admin[extname]",
      },
    },
  },
  server: {
    // npm run dev: the API of a bridge running on 127.0.0.1:8787.
    proxy: { "/admin/api": "http://127.0.0.1:8787" },
  },
  test: {
    environment: "jsdom",
    globals: true,
  },
});
