import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Build the SPA into ./dist for embedding via go:embed.
export default defineConfig({
  plugins: [react()],
  base: "/",
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
  server: {
    // During local dev, proxy API calls to a running gotifacts instance.
    proxy: {
      "/api": "http://localhost:8080",
    },
  },
});
