import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    host: "::",
    port: 5173,
    strictPort: true,
    allowedHosts: true,
    proxy: {
      "/login": "http://localhost:8888",
      "/oauth2": "http://localhost:8888",
      "/api": "http://localhost:8888",
    },
  },
});
