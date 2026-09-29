// Windows PostgreSQL cannot bootstrap from non-ASCII paths under a legacy codepage.
// Use an ASCII runtime directory for local testing only; Docker does not need this.
import { spawn } from "node:child_process";
import { once } from "node:events";
import { cp, mkdir, access, writeFile, unlink } from "node:fs/promises";
import { resolve, dirname } from "node:path";
import { tmpdir } from "node:os";
import { createHash, randomBytes } from "node:crypto";
import { createRequire } from "node:module";
const require = createRequire(import.meta.url);
const { Client } = require("pg");
const project = resolve(import.meta.dirname, "..");
export const runtimeDirectory = resolve(
  tmpdir(),
  "zhixu-" + createHash("sha256").update(project).digest("hex").slice(0, 12),
);
export default class LocalPostgres {
  constructor(options) {
    this.options = options;
    this.databaseDir = options.databaseDir;
    this.child = null;
  }
  async prepare() {
    await mkdir(runtimeDirectory, { recursive: true });
    const packageName =
      process.platform === "win32"
        ? "@embedded-postgres/windows-x64"
        : `@embedded-postgres/${process.platform === "darwin" ? "darwin" : "linux"}-${process.arch}`;
    const binary = await import(packageName);
    this.bin = resolve(runtimeDirectory, "native", "bin");
    const ext = process.platform === "win32" ? ".exe" : "";
    this.postgres = resolve(this.bin, "postgres" + ext);
    this.initdb = resolve(this.bin, "initdb" + ext);
    try {
      await access(this.initdb);
    } catch {
      await cp(
        resolve(dirname(binary.initdb), ".."),
        resolve(runtimeDirectory, "native"),
        { recursive: true },
      );
    }
  }
  async initialise() {
    await this.prepare();
    const passwordFile = resolve(
      runtimeDirectory,
      "pw-" + randomBytes(8).toString("hex"),
    );
    await writeFile(passwordFile, this.options.password + "\n", {
      mode: 0o600,
    });
    try {
      const child = spawn(
        this.initdb,
        [
          "-D",
          this.databaseDir,
          "-U",
          this.options.user,
          "--auth=scram-sha-256",
          "--pwfile=" + passwordFile,
          "--encoding=UTF8",
          "--locale=C",
        ],
        { windowsHide: true, cwd: runtimeDirectory },
      );
      let output = "";
      child.stdout.on("data", (b) => (output += b));
      child.stderr.on("data", (b) => (output += b));
      const [code] = await once(child, "exit");
      if (code !== 0)
        throw new Error("PostgreSQL initialization failed: " + output);
    } finally {
      await unlink(passwordFile);
    }
  }
  getPgClient() {
    return new Client({
      host: "127.0.0.1",
      port: this.options.port,
      user: this.options.user,
      password: this.options.password,
      database: "postgres",
    });
  }
  async start() {
    await this.prepare();
    this.child = spawn(
      this.postgres,
      [
        "-D",
        this.databaseDir,
        "-p",
        String(this.options.port),
        "-h",
        "127.0.0.1",
      ],
      {
        windowsHide: true,
        cwd: runtimeDirectory,
        stdio: ["ignore", "ignore", "pipe"],
      },
    );
    let logs = "";
    this.child.stderr.on("data", (b) => (logs += b));
    for (let i = 0; i < 100; i++) {
      if (this.child.exitCode !== null)
        throw new Error("PostgreSQL exited: " + logs);
      const c = this.getPgClient();
      try {
        await c.connect();
        await c.end();
        return;
      } catch {
        await c.end().catch(() => {});
        await new Promise((r) => setTimeout(r, 100));
      }
    }
    throw new Error("PostgreSQL startup timed out: " + logs);
  }
  async stop() {
    if (!this.child || this.child.exitCode !== null) return;
    const child = spawn(
      resolve(this.bin, process.platform === "win32" ? "pg_ctl.exe" : "pg_ctl"),
      ["-D", this.databaseDir, "stop", "-m", "fast", "-w"],
      { windowsHide: true, stdio: "ignore" },
    );
    await once(child, "exit");
  }
}
