// Development fallback for machines without Docker. Production uses Compose.
import LocalMySQL, { runtimeDirectory } from "./mysql-runtime.mjs";
import LocalQdrant from "./qdrant-runtime.mjs";
import { spawn } from "node:child_process";
import { mkdir, access, readFile, writeFile } from "node:fs/promises";
import { resolve } from "node:path";
import { randomBytes } from "node:crypto";
const root = resolve(import.meta.dirname, "..");
const local = resolve(root, ".local");
await mkdir(local, { recursive: true });
const configPath = resolve(local, "dev-credentials.json");
let config;
try {
  config = JSON.parse(await readFile(configPath, "utf8"));
} catch {
  config = {
    db: randomBytes(24).toString("hex"),
    admin: randomBytes(16).toString("hex"),
  };
  await writeFile(configPath, JSON.stringify(config, null, 2));
}
// The portable runtime uses the insecure-initialized root account, so this file
// only carries the app admin password; the database stays local and disposable.
const databaseDir = resolve(runtimeDirectory, "development-db");
const mysql = new LocalMySQL({ databaseDir, port: 55432, database: "teaching" });
try {
  await access(resolve(databaseDir, "mysql"));
} catch {
  await mysql.initialise();
}
await mysql.start();
// 向量库同样用便携二进制：本地预览的知识库页面因此可以真正跑起来（索引仍需可用的课程网关）。
const qdrant = new LocalQdrant({ port: Number(process.env.QDRANT_PORT || 16333) });
await qdrant.start();
const api = spawn(
  resolve(local, process.platform === "win32" ? "server.exe" : "server"),
  [],
  {
    cwd: root,
    windowsHide: true,
    stdio: "inherit",
    env: {
      ...process.env,
      DATABASE_URL: mysql.databaseUrl,
      ADMIN_PASSWORD: config.admin,
      UPLOAD_DIR: resolve(local, "uploads"),
      APP_ORIGIN: "http://127.0.0.1:5173",
      KNOWLEDGE_ENABLED: "true",
      QDRANT_URL: qdrant.url,
      QDRANT_COLLECTION: process.env.QDRANT_COLLECTION || "campusclaw_chunks",
      EMBEDDING_DIMENSIONS: process.env.EMBEDDING_DIMENSIONS || "1024",
      EMBEDDING_PROFILE_ID: process.env.EMBEDDING_PROFILE_ID || "gateway-embedding-v1",
    },
  },
);
const web = spawn(
  process.execPath,
  [
    resolve(root, "frontend/node_modules/vite/bin/vite.js"),
    "--host",
    "127.0.0.1",
    "--port",
    "5173",
  ],
  { cwd: resolve(root, "frontend"), windowsHide: true, stdio: "inherit" },
);
console.log(
  "Local preview: http://127.0.0.1:5173; admin credentials: .local/dev-credentials.json",
);
let stopping = false;
async function stop() {
  if (stopping) return;
  stopping = true;
  api.kill();
  web.kill();
  await qdrant.stop();
  await mysql.stop();
  process.exit();
}
process.on("SIGINT", stop);
process.on("SIGTERM", stop);
api.on("exit", stop);
web.on("exit", stop);
