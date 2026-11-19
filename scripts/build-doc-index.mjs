import { glob, readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { SitemapStream, streamToPromise } from "sitemap";
import { renderLlmsTxt } from "@geosuite/llms-txt-generator";

const docs = join(import.meta.dirname, "../docs");
const domain = (await readFile(join(docs, "CNAME"), "utf8")).trim();
const site = new URL(`https://${domain}/`);
const apiFiles = (await Array.fromAsync(glob("api/**/*.md", { cwd: docs }))).sort();

async function docLink(file) {
  const markdown = await readFile(join(docs, file), "utf8");
  return {
    loc: new URL(file.split("/").map(encodeURIComponent).join("/"), site).href,
    title: markdown.match(/^# +(.+)$/m)?.[1].trim() || file,
  };
}

const guide = await docLink("gettingstarted.md");
const api = await Promise.all(apiFiles.map(docLink));
const sitemap = new SitemapStream();
const xml = streamToPromise(sitemap);
sitemap.write({ url: site.href });
for (const { loc } of [guide, ...api]) sitemap.write({ url: loc });
sitemap.end();

const llms = renderLlmsTxt(new Map([["/guide", [guide]], ["/api", api]]), {
  name: "Tower API 文档",
  summary: "Tower API 入门、OAuth 2.0 授权流程和各资源的接口参考。",
  sectionTitles: { "/guide": "入门", "/api": "API 参考" },
});

await Promise.all([
  writeFile(join(docs, "sitemap.xml"), Buffer.concat([await xml, Buffer.from("\n")])),
  writeFile(join(docs, "llms.txt"), llms),
]);
