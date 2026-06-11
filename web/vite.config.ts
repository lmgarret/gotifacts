import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Minimal ambient so we can read a shell env var in dev without pulling in
// @types/node (which the project deliberately omits).
declare const process: { env: Record<string, string | undefined> };

type ProxyReq = { setHeader(name: string, value: string): void };
type ProxyServer = { on(event: "proxyReq", cb: (req: ProxyReq) => void): void };

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
    // Set GOTIFACTS_DEV_USER to simulate your auth proxy injecting the
    // forward-auth identity header (so /api/* authenticates). Put that same
    // user in the server's GOTIFACTS_ADMIN_USERS to reach the Keys view.
    proxy: {
      "/api": {
        target: "http://localhost:8080",
        changeOrigin: true,
        configure: (proxy) => {
          const devUser = process.env.GOTIFACTS_DEV_USER;
          if (!devUser) return;
          (proxy as unknown as ProxyServer).on("proxyReq", (req) => {
            req.setHeader("Remote-User", devUser);
          });
        },
      },
    },
  },
});
