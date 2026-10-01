// Ollama 便携运行时：把官方 Windows 包解压到 .tools/ollama（已忽略提交），
// 在宿主机端口上启动，并从 Ollama 官方模型仓库拉取嵌入模型。
//
// 本机网络无法访问 Docker Hub 与 HuggingFace，但可以访问 GitHub 发布页与 registry.ollama.ai，
// 因此这里用「GitHub 发布包 + 官方模型仓库」这条路径提供真实的嵌入模型。
import { spawn, execFile } from "node:child_process";
import { mkdir, access, readFile, writeFile } from "node:fs/promises";
import { createWriteStream } from "node:fs";
import { once } from "node:events";
import { resolve } from "node:path";

const project = resolve(import.meta.dirname, "..");
const toolDirectory = resolve(project, ".tools/ollama");
const defaultVersion = "v0.34.1";
const defaultZipUrl =
  process.env.OLLAMA_ZIP_URL ||
  `https://github.com/ollama/ollama/releases/download/${defaultVersion}/ollama-windows-amd64.zip`;

async function exists(path) {
  try {
    await access(path);
    return true;
  } catch {
    return false;
  }
}

function run(command, args, options = {}) {
  return new Promise((resolvePromise, reject) => {
    execFile(command, args, { windowsHide: true, ...options }, (error, stdout, stderr) =>
      error ? reject(new Error(String(stderr || error.message).trim())) : resolvePromise(stdout),
    );
  });
}

export default class LocalOllama {
  constructor(options = {}) {
    this.port = options.port || Number(process.env.OLLAMA_PORT || 11434);
    this.models = options.models || resolve(project, ".local/ollama-models");
    this.binary = resolve(toolDirectory, "ollama.exe");
    this.child = null;
  }

  get url() {
    return `http://127.0.0.1:${this.port}`;
  }

  get openAIBaseURL() {
    return `${this.url}/v1`;
  }

  async prepare() {
    if (await exists(this.binary)) return;
    await mkdir(toolDirectory, { recursive: true });
    const archive = resolve(toolDirectory, "ollama.zip");
    console.log(`首次运行：下载 Ollama 到 ${toolDirectory}（来源 ${defaultZipUrl}）`);
    // Windows 自带的 curl 需要关闭证书吊销检查，否则在离线 CRL 环境会直接失败。
    await run("curl.exe", ["-L", "--fail", "--ssl-no-revoke", "--retry", "3", "-o", archive, defaultZipUrl]);
    await run("powershell", [
      "-NoProfile",
      "-Command",
      `Expand-Archive -LiteralPath '${archive}' -DestinationPath '${toolDirectory}' -Force`,
    ]);
    if (!(await exists(this.binary))) {
      throw new Error(`解压后没有找到 ollama.exe：${toolDirectory}`);
    }
  }

  async ready() {
    try {
      const response = await fetch(`${this.url}/api/tags`, { signal: AbortSignal.timeout(2000) });
      return response.ok;
    } catch {
      return false;
    }
  }

  async start() {
    await this.prepare();
    await mkdir(this.models, { recursive: true });
    this.child = spawn(this.binary, ["serve"], {
      cwd: toolDirectory,
      windowsHide: true,
      stdio: ["ignore", "ignore", "pipe"],
      env: {
        ...process.env,
        OLLAMA_HOST: `127.0.0.1:${this.port}`,
        OLLAMA_MODELS: this.models,
      },
    });
    let logs = "";
    this.child.stderr.on("data", (chunk) => (logs += chunk));
    for (let i = 0; i < 60; i++) {
      if (this.child.exitCode !== null) throw new Error(`Ollama 退出：${logs}`);
      if (await this.ready()) return;
      await new Promise((done) => setTimeout(done, 500));
    }
    throw new Error(`Ollama 启动超时：${logs}`);
  }

  async models_() {
    const response = await fetch(`${this.url}/api/tags`);
    const payload = await response.json();
    return (payload.models || []).map((model) => model.name);
  }

  async hasModel(name) {
    return (await this.models_()).includes(name);
  }

  // pull 从官方模型仓库拉取模型；首次约 640 MB。
  async pull(name) {
    if (await this.hasModel(name)) return;
    console.log(`拉取嵌入模型 ${name}（来自 registry.ollama.ai，首次约 640 MB）`);
    const response = await fetch(`${this.url}/api/pull`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name, stream: true }),
    });
    if (!response.ok || !response.body) throw new Error(`拉取模型失败：HTTP ${response.status}`);
    const decoder = new TextDecoder();
    let buffer = "";
    let lastLine = "";
    for await (const chunk of response.body) {
      buffer += decoder.decode(chunk, { stream: true });
      const lines = buffer.split("\n");
      buffer = lines.pop() ?? "";
      for (const line of lines) {
        if (!line.trim()) continue;
        const event = JSON.parse(line);
        if (event.error) throw new Error(`拉取模型失败：${event.error}`);
        const text = `${event.status || ""} ${event.completed && event.total ? Math.round((event.completed / event.total) * 100) + "%" : ""}`.trim();
        if (text && text !== lastLine) {
          lastLine = text;
          if (text.includes("%")) process.stdout.write(`\r  ${text}          `);
        }
      }
    }
    process.stdout.write("\n");
    if (!(await this.hasModel(name))) throw new Error(`拉取后仍未找到模型 ${name}`);
  }

  // embed 返回一次嵌入结果，用于校验维度与数值有效性。
  async embed(model, input) {
    const response = await fetch(`${this.openAIBaseURL}/embeddings`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ model, input: [input] }),
    });
    if (!response.ok) throw new Error(`嵌入接口返回 HTTP ${response.status}`);
    const payload = await response.json();
    return payload.data?.[0]?.embedding || [];
  }

  async stop() {
    if (!this.child) return;
    const child = this.child;
    this.child = null;
    if (child.exitCode !== null) return;
    // Ollama 在 Windows 上需要显式结束整个进程树。
    await run("taskkill", ["/PID", String(child.pid), "/T", "/F"]).catch(() => child.kill());
    await Promise.race([once(child, "exit"), new Promise((done) => setTimeout(done, 5000))]);
  }
}

// 供脚本直接运行时使用：node scripts/ollama-runtime.mjs [start|stop]
if (process.argv[1] && process.argv[1].endsWith("ollama-runtime.mjs")) {
  const command = process.argv[2] || "start";
  const model = process.env.EMBEDDING_MODEL || "qwen3-embedding:0.6b";
  const runtime = new LocalOllama();
  if (command === "stop") {
    await runtime.stop();
    console.log("Ollama 已停止");
  } else {
    await runtime.start();
    await runtime.pull(model);
    const vector = await runtime.embed(model, "课程考核方式");
    const statePath = resolve(project, ".local/ollama-runtime.json");
    await writeFile(statePath, JSON.stringify({ url: runtime.url, baseURL: runtime.openAIBaseURL, model, dimensions: vector.length, pid: runtime.child?.pid }, null, 2));
    console.log(`Ollama 就绪：${runtime.openAIBaseURL}，模型 ${model}，维度 ${vector.length}`);
    console.log(`状态写入 ${statePath}；用 node scripts/ollama-runtime.mjs stop 停止。`);
    const existing = await readFile(statePath, "utf8").catch(() => "");
    if (!existing.includes('"keepAlive"')) {
      console.log("提示：该进程会持续运行，供 Compose 中的 api/worker 通过 host.docker.internal 使用。");
    }
  }
}
