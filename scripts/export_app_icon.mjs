// 从左上角现有场记板组件及其样式导出图标，不维护另一份图案或配色。
import { readFileSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";

const project = new URL("../", import.meta.url);
const require = createRequire(new URL("frontend/package.json", project));
const { createElement } = require("react");
const { renderToStaticMarkup } = require("react-dom/server");
const { Clapperboard } = require("lucide-react");
const read = (path) => readFileSync(new URL(path, project), "utf8");
const styles = read("frontend/src/styles/app.css");
const tokens = read("frontend/src/styles/tokens.css");
const brandRule = styles.match(/\.brand-mark\s*\{([^}]+)\}/)?.[1];
if (!brandRule) throw new Error("未找到现有品牌标记样式");
const dimension = (name) => {
  const match = brandRule.match(
    new RegExp(`(?:^|;)\\s*${name}:\\s*([\\d.]+)px\\s*;`),
  );
  if (!match) throw new Error(`品牌标记缺少 ${name}`);
  return Number(match[1]);
};
const color = (property) => {
  const token = brandRule.match(
    new RegExp(`(?:^|;)\\s*${property}:\\s*var\\((--[\\w-]+)\\)`),
  )?.[1];
  const value =
    token &&
    tokens.match(new RegExp(`${token}:\\s*(#[\\da-fA-F]{6})\\s*;`))?.[1];
  if (!value) throw new Error(`品牌标记缺少 ${property} 颜色变量`);
  return value;
};
const size = Number(
  read("frontend/src/App.tsx").match(
    /className="brand-mark">\s*<Clapperboard size=\{(\d+)\}/,
  )?.[1],
);
const width = dimension("width");
const height = dimension("height");
if (!(size > 0 && size <= width && size <= height))
  throw new Error("未找到左上角场记板尺寸");
const rendered = renderToStaticMarkup(
  createElement(Clapperboard, {
    size,
    color: color("color"),
  }),
);
// 展平 SVG 视口，兼容本机用于 PNG 导出的 SVG Tiny 渲染器。
const attributes = rendered.match(/^<svg\s+([^>]+)>/)?.[1];
const viewBox = attributes?.match(/viewBox="0 0 ([\d.]+) ([\d.]+)"/);
if (!viewBox) throw new Error("现有场记板视口格式已变化");
const drawingAttributes = Array.from(
  attributes.matchAll(/([\w:-]+)="([^"]*)"/g),
)
  .filter(([, name]) => !["xmlns", "width", "height", "viewBox"].includes(name))
  .map(([, name, value]) => `${name}="${value}"`)
  .join(" ");
const transform = `translate(${(width - size) / 2} ${(height - size) / 2}) scale(${size / Number(viewBox[1])} ${size / Number(viewBox[2])})`;
const glyph = rendered
  .replace(/^<svg[^>]*>/, `<g ${drawingAttributes} transform="${transform}">`)
  .replace(/<\/svg>$/, "</g>");
const svg = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${width} ${height}"><rect width="${width}" height="${height}" rx="${dimension("border-radius")}" fill="${color("background")}"/>${glyph}</svg>\n`;
for (const output of ["build/appicon.svg", "frontend/public/favicon.svg"]) {
  writeFileSync(new URL(output, project), svg);
}
console.log(
  "已导出左上角现有场记板图标：build/appicon.svg、frontend/public/favicon.svg",
);
