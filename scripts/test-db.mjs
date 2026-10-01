// 数据库层测试（不需要 Docker）：启动便携 MySQL 与便携 Qdrant，再跑 backend 的 Go 测试。
//
//	npm run test:db
//
// 两个实例都用独立端口与临时数据目录，跑完即关闭；不会碰到 Compose 的卷或开发实例。
import { spawn } from "node:child_process";
import { mkdtemp, mkdir } from "node:fs/promises";
import { once } from "node:events";
import { resolve, delimiter } from "node:path";
import LocalMySQL, { runtimeDirectory } from "./mysql-runtime.mjs";
import LocalQdrant from "./qdrant-runtime.mjs";

const root = resolve(import.meta.dirname, "..");
const mysqlPort = Number(process.env.TEST_DB_MYSQL_PORT || 55434);
const qdrantPort = Number(process.env.TEST_DB_QDRANT_PORT || 16334);

await mkdir(runtimeDirectory, { recursive: true });
const directory = await mkdtemp(resolve(runtimeDirectory, "dbtest-"));
const mysql = new LocalMySQL({ databaseDir: resolve(directory, "db"), port: mysqlPort, database: "teaching" });
const qdrant = new LocalQdrant({ port: qdrantPort });

let exitCode = 1;
// 便携运行时把进度写在 stderr；在 PowerShell 里这会被当成错误记录，因此这里收集后改从 stdout 输出。
const runtimeMessages = [];
const originalError = console.error;
console.error = (...args) => runtimeMessages.push(args.map(String).join(" "));
const restoreConsole = () => {
  console.error = originalError;
  if (runtimeMessages.length > 0) console.log(runtimeMessages.join("\n"));
};
try {
  // 每次都用全新的数据目录，因此先初始化（正好覆盖首次运行的路径）。
  await mysql.initialise();
  await mysql.start();
  if (typeof mysql.ensureDatabase === "function") await mysql.ensureDatabase();
  console.log(`MySQL 就绪：${mysql.databaseUrl}`);
  await qdrant.start();
  console.log(`Qdrant 就绪：${qdrant.url}`);

  const goBinary = process.platform === "win32" ? resolve(root, ".tools/go/bin/go.exe") : "go";
  const child = spawn(goBinary, ["test", "./..."], {
    cwd: resolve(root, "backend"),
    stdio: "inherit",
    windowsHide: true,
    env: {
      ...process.env,
      PATH: `${resolve(root, ".tools/go/bin")}${delimiter}${process.env.PATH}`,
      GOPROXY: process.env.GOPROXY || "https://goproxy.cn,direct",
      MYSQL_TEST_DSN: mysql.databaseUrl,
      QDRANT_TEST_URL: qdrant.url,
    },
  });
  const [code] = await once(child, "exit");
  exitCode = code ?? 1;
  console.log(exitCode === 0 ? "PASS: 数据库层测试全部通过（便携 MySQL + 便携 Qdrant）。" : `FAIL: go test 退出码 ${exitCode}`);
} catch (error) {
  console.error = originalError;
  console.error("test:db 失败：" + (error && error.message ? error.message : error));
  exitCode = 1;
} finally {
  await qdrant.stop().catch(() => {});
  await mysql.stop().catch(() => {});
  restoreConsole();
}
process.exit(exitCode);
