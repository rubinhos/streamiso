// Builds the two pages into dist/, which the catalog serves: dist/index.html at "/", dist/player.html at
// "/player", and everything else under "/static/".
import { resolve } from "node:path";
import preact from "@preact/preset-vite";
import { defineConfig } from "vite";

export default defineConfig({
  plugins: [preact()],
  base: "/static/",
  build: {
    outDir: "dist",
    emptyOutDir: true,
    rollupOptions: {
      input: {
        index: resolve(__dirname, "index.html"),
        player: resolve(__dirname, "player.html"),
      },
    },
  },
  server: {
    // npm run dev: the API and the covers come from a running catalog
    proxy: { "/api": process.env.STREAMISO_URL || "http://localhost:8080" },
  },
});
