// Development fallback for machines without Docker. Production uses Compose.
import EmbeddedPostgres, { runtimeDirectory } from "./postgres-runtime.mjs";
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
const databaseDir = resolve(runtimeDirectory, "development-db");
const pg = new EmbeddedPostgres({
  databaseDir,
  user: "postgres",
  password: config.db,
  port: 55432,
  persistent: true,
  initdbFlags: ["--encoding=UTF8", "--locale=C"],
  onLog: () => {},
  onError: (m) => {
    if (String(m).includes("FATAL")) console.error(m);
  },
});
try {
  await access(resolve(databaseDir, "PG_VERSION"));
} catch {
  await pg.initialise();
}
await pg.start();
const api = spawn(
  resolve(local, process.platform === "win32" ? "server.exe" : "server"),
  [],
  {
    cwd: root,
    windowsHide: true,
    stdio: "inherit",
    env: {
      ...process.env,
      DATABASE_URL: `postgres://postgres:${config.db}@127.0.0.1:55432/postgres?sslmode=disable`,
      ADMIN_PASSWORD: config.admin,
      UPLOAD_DIR: resolve(local, "uploads"),
      APP_ORIGIN: "http://127.0.0.1:5173",
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
  await pg.stop();
  process.exit();
}
process.on("SIGINT", stop);
process.on("SIGTERM", stop);
api.on("exit", stop);
web.on("exit", stop);
