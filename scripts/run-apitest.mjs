// 运行 apitest 套件：优先用 apitest/.venv 里的解释器，没有虚拟环境时退回系统 Python。
//
//   npm run test:api                 # 全部用例
//   npm run test:api -- --filter sessions
import { spawn } from "node:child_process";
import { existsSync } from "node:fs";
import { resolve } from "node:path";

const root = resolve(import.meta.dirname, "..");
const venv =
  process.platform === "win32"
    ? resolve(root, "apitest/.venv/Scripts/python.exe")
    : resolve(root, "apitest/.venv/bin/python");
const python = existsSync(venv) ? venv : process.platform === "win32" ? "python" : "python3";

const child = spawn(python, [resolve(root, "apitest/run_all.py"), ...process.argv.slice(2)], {
  cwd: root,
  stdio: "inherit",
  windowsHide: true,
});
child.on("error", (error) => {
  console.error("无法启动 Python：" + error.message);
  process.exit(2);
});
child.on("exit", (code) => process.exit(code ?? 1));
