import path from "path";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { SitemapStream, streamToPromise } from "sitemap";
import { renderLlmsTxt } from "@geosuite/llms-txt-generator";

function siteIndexes() {
  return {
    name: "agentstower-site-indexes",
    async generateBundle() {
      // Go substitutes the deployment's public website URL at request time.
      const site = "https://agentstower.invalid";
      const sitemap = new SitemapStream();
      const xml = streamToPromise(sitemap);
      sitemap.end({ url: site + "/" });
      this.emitFile({ type: "asset", fileName: "sitemap.xml", source: await xml });

      const links = [
        { title: "AgentsTower 管理站", loc: site + "/" },
        { title: "MCP 接入指南", loc: site + "/api/skill.md" },
      ];
      const llms = renderLlmsTxt(new Map([["/", links]]), {
        name: "AgentsTower",
        summary: "绑定 Tower 账号，为 Agent 配置和测试 MCP 工具。",
        sectionTitles: { "/": "入口" },
      });
      this.emitFile({ type: "asset", fileName: "llms.txt", source: llms });
    },
  };
}

export default defineConfig({
  plugins: [react(), tailwindcss(), siteIndexes()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  server: {
    host: "::",
    port: 5173,
    strictPort: true,
    allowedHosts: true,
    proxy: {
      "/sitemap.xml": "http://localhost:8888",
      "/llms.txt": "http://localhost:8888",
      "/login": "http://localhost:8888",
      "/oauth2": "http://localhost:8888",
      "/api": "http://localhost:8888",
    },
  },
});
