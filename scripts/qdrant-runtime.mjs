// Qdrant 便携运行时：把官方 Windows 二进制解压到 .tools/qdrant（已忽略提交），
// 在本机端口上启动一个只用于开发与测试的实例。Docker 环境请用 Compose 中的 qdrant 服务。
import { spawn, execFile } from "node:child_process";
import { once } from "node:events";
import { cp, mkdir, access, writeFile, readdir } from "node:fs/promises";
import { createWriteStream } from "node:fs";
import { Readable } from "node:stream";
import { pipeline } from "node:stream/promises";
import { resolve, dirname } from "node:path";
import { tmpdir } from "node:os";
import { createHash } from "node:crypto";

const project = resolve(import.meta.dirname, "..");
const toolDirectory = resolve(project, ".tools/qdrant");
const runtimeDirectory = resolve(
  tmpdir(),
  "zhixu-qdrant-" + createHash("sha256").update(project).digest("hex").slice(0, 12),
);
const defaultVersion = "v1.15.1";
export const defaultQdrantUrl =
  process.env.QDRANT_ZIP_URL ||
  `https://github.com/qdrant/qdrant/releases/download/${defaultVersion}/qdrant-x86_64-pc-windows-msvc.zip`;

async function exists(path) {
  try {
    await access(path);
    return true;
  } catch {
    return false;
  }
}

async function fetchDownload(url, destination) {
  // 没有超时时，被阻断的连接会一直挂着，后面的回退链也就永远不会触发。
  const response = await fetch(url, { redirect: "follow", signal: AbortSignal.timeout(60_000) });
  if (!response.ok || !response.body) {
    throw new Error(`HTTP ${response.status}`);
  }
  await pipeline(Readable.fromWeb(response.body), createWriteStream(destination));
}

function run(command, args) {
  return new Promise((resolvePromise, reject) => {
    execFile(command, args, { windowsHide: true }, (error, stdout, stderr) =>
      error ? reject(new Error(String(stderr || error.message).trim())) : resolvePromise(stdout),
    );
  });
}

// 下载在部分网络环境下会失败，这里按 fetch → curl.exe → PowerShell 依次回退，并把失败原因一起报出来。
async function download(url, destination) {
  const failures = [];
  try {
    await fetchDownload(url, destination);
    return;
  } catch (error) {
    failures.push(`fetch: ${error.message}`);
  }
  if (process.platform === "win32") {
    try {
      await run("curl.exe", ["-L", "--fail", "--silent", "--show-error", "-o", destination, url]);
      return;
    } catch (error) {
      failures.push(`curl: ${error.message}`);
    }
    try {
      await run("powershell", [
        "-NoProfile",
        "-Command",
        `$ProgressPreference='SilentlyContinue'; Invoke-WebRequest -Uri '${url}' -OutFile '${destination}'`,
      ]);
      return;
    } catch (error) {
      failures.push(`powershell: ${error.message}`);
    }
  }
  throw new Error(`下载 Qdrant 失败（${failures.join("；")}）`);
}

export default class LocalQdrant {
  constructor(options = {}) {
    this.port = options.port || 16333;
    // 存储与配置按端口隔离，否则并行的实例（例如 dev:local 与 test:db）会争抢同一目录。
    this.storage = options.storage || resolve(runtimeDirectory, `storage-${this.port}`);
    this.configPath = resolve(runtimeDirectory, `config-${this.port}.yaml`);
    this.child = null;
    this.binary = resolve(toolDirectory, "qdrant.exe");
  }

  get url() {
    return `http://127.0.0.1:${this.port}`;
  }

  // prepare 首次运行时下载并解压官方二进制。
  async prepare() {
    await mkdir(toolDirectory, { recursive: true });
    if (await exists(this.binary)) return;
    const archive = resolve(toolDirectory, "qdrant.zip");
    console.log(`首次运行：下载 Qdrant 到 ${toolDirectory}（来源 ${defaultQdrantUrl}）`);
    await download(defaultQdrantUrl, archive);
    const { execFile } = await import("node:child_process");
    await new Promise((resolvePromise, reject) => {
      execFile(
        "powershell",
        ["-NoProfile", "-Command", `Expand-Archive -LiteralPath '${archive}' -DestinationPath '${toolDirectory}' -Force`],
        { windowsHide: true },
        (error) => (error ? reject(error) : resolvePromise()),
      );
    });
    // 有些发行包会多一层目录，这里把可执行文件统一放到工具目录根部。
    if (!(await exists(this.binary))) {
      for (const entry of await readdir(toolDirectory, { withFileTypes: true })) {
        if (!entry.isDirectory()) continue;
        const candidate = resolve(toolDirectory, entry.name, "qdrant.exe");
        if (await exists(candidate)) {
          await cp(candidate, this.binary);
          break;
        }
      }
    }
    if (!(await exists(this.binary))) {
      throw new Error(`解压后没有找到 qdrant.exe：${toolDirectory}`);
    }
  }

  async ready() {
    try {
      const response = await fetch(`${this.url}/readyz`, { signal: AbortSignal.timeout(2000) });
      return response.ok;
    } catch {
      return false;
    }
  }

  async start() {
    await this.prepare();
    await mkdir(this.storage, { recursive: true });
    await writeFile(this.configPath, `storage:\n  storage_path: ${this.storage.replace(/\\/g, "/")}\n`);
    this.child = spawn(
      this.binary,
      ["--config-path", this.configPath],
      {
        cwd: runtimeDirectory,
        windowsHide: true,
        stdio: ["ignore", "ignore", "pipe"],
        env: { ...process.env, QDRANT__SERVICE__HTTP_PORT: String(this.port) },
      },
    );
    let logs = "";
    this.child.stderr.on("data", (chunk) => (logs += chunk));
    for (let i = 0; i < 60; i++) {
      if (this.child.exitCode !== null) throw new Error(`Qdrant 退出：${logs}`);
      if (await this.ready()) return;
      await new Promise((done) => setTimeout(done, 500));
    }
    throw new Error(`Qdrant 启动超时：${logs}`);
  }

  async stop() {
    if (!this.child) return;
    const child = this.child;
    this.child = null;
    if (child.exitCode !== null) return;
    child.kill();
    await Promise.race([once(child, "exit"), new Promise((done) => setTimeout(done, 5000))]);
  }
}
