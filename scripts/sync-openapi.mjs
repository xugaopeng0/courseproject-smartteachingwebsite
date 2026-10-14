// 同步对外契约与文档页面资源（都不在 go:embed 能直接读取的目录里，因此同步到 backend/ 下）。
//
//	node scripts/sync-openapi.mjs          # 写入
//	node scripts/sync-openapi.mjs --check  # 只校验是否一致（CI 与测试用，不一致时非零退出）
//
// 契约的唯一来源是 docs/openapi.json；backend/openapi.json 是它的字节级副本，
// openapi_test.go 会在每次 go test 时校验两者一致，避免副本过期。
import { readFile, writeFile, mkdir, readdir } from "node:fs/promises";
import { resolve } from "node:path";

const root = resolve(import.meta.dirname, "..");
const check = process.argv.includes("--check");
const source = resolve(root, "docs/openapi.json");
const target = resolve(root, "backend/openapi.json");
const swaggerSource = resolve(root, "frontend/node_modules/swagger-ui-dist");
const swaggerTarget = resolve(root, "backend/swaggerui");

// 文档页面只需要这几个文件，其余（尤其是 .map 与 ES 变体）不复制。
const assets = [
  "swagger-ui.css",
  "swagger-ui-bundle.js",
  "swagger-ui-standalone-preset.js",
  "favicon-16x16.png",
  "favicon-32x32.png",
  "LICENSE",
  "NOTICE",
];

const contract = await readFile(source, "utf8");
const existing = await readFile(target, "utf8").catch(() => null);
const contractMatches = existing === contract;

const assetStates = [];
for (const name of assets) {
  const wanted = await readFile(resolve(swaggerSource, name));
  const current = await readFile(resolve(swaggerTarget, name)).catch(() => null);
  assetStates.push({ name, wanted, matches: current !== null && current.equals(wanted) });
}
const assetsMatch = assetStates.every((item) => item.matches);

if (check) {
  const problems = [];
  if (!contractMatches) problems.push("backend/openapi.json 与 docs/openapi.json 不一致");
  for (const item of assetStates.filter((entry) => !entry.matches)) problems.push(`缺少或过期：backend/swaggerui/${item.name}`);
  if (problems.length > 0) {
    console.error("契约与文档资源需要同步：\n  " + problems.join("\n  "));
    console.error("请运行 node scripts/sync-openapi.mjs");
    process.exit(1);
  }
  console.log(`契约与 ${assets.length} 个文档资源均已同步。`);
  process.exit(0);
}

await mkdir(swaggerTarget, { recursive: true });
if (!contractMatches) {
  // 保持 LF 与无 BOM，确保与源文件字节一致。
  await writeFile(target, contract, "utf8");
}
for (const item of assetStates) {
  if (!item.matches) await writeFile(resolve(swaggerTarget, item.name), item.wanted);
}
console.log(`已同步契约到 backend/openapi.json，并复制 ${assets.length} 个文档资源到 backend/swaggerui/。`);
