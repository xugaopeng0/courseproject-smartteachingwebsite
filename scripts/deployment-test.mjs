// Runs against the local Compose deployment, recreates its containers without
// deleting volumes, then removes only the temporary records created by this run.
import assert from "node:assert/strict";
import { readFile, mkdir } from "node:fs/promises";
import { resolve, delimiter } from "node:path";
import { randomBytes } from "node:crypto";
import { spawn } from "node:child_process";
import { chromium, expect } from "@playwright/test";
const root = resolve(import.meta.dirname, "..");
const config = Object.fromEntries(
  (await readFile(resolve(root, ".env"), "utf8"))
    .split(/\r?\n/)
    .filter((l) => l && !l.startsWith("#"))
    .map((l) => {
      const at = l.indexOf("=");
      return [l.slice(0, at), l.slice(at + 1)];
    }),
);
const origin = config.APP_ORIGIN || "http://localhost:8080";
const suffix = randomBytes(6).toString("hex"),
  password = randomBytes(16).toString("hex");
const dockerBin =
  process.platform === "win32"
    ? resolve(process.env.LOCALAPPDATA, "Programs/DockerDesktop/resources/bin")
    : "";
function docker(args, input) {
  return new Promise((resolvePromise, reject) => {
    const child = spawn(
      process.platform === "win32"
        ? resolve(dockerBin, "docker.exe")
        : "docker",
      args,
      {
        cwd: root,
        windowsHide: true,
        env: { ...process.env, PATH: dockerBin + delimiter + process.env.PATH },
        stdio: ["pipe", "pipe", "pipe"],
      },
    );
    let output = "";
    child.stdout.on("data", (b) => (output += b));
    child.stderr.on("data", (b) => (output += b));
    child.on("error", reject);
    child.on("exit", (code) =>
      code === 0 ? resolvePromise(output) : reject(new Error(output)),
    );
    child.stdin.end(input);
  });
}
function agent() {
  let cookie = "";
  return {
    async request(path, method = "GET", body, status = 200) {
      const response = await fetch(origin + "/api" + path, {
        method,
        headers: {
          Cookie: cookie,
          Origin: origin,
          ...(body instanceof FormData
            ? {}
            : { "Content-Type": "application/json" }),
        },
        body:
          body === undefined
            ? undefined
            : body instanceof FormData
              ? body
              : JSON.stringify(body),
      });
      if (response.headers.has("set-cookie"))
        cookie = response.headers.get("set-cookie").split(";")[0];
      const data = await response.json();
      assert.equal(
        response.status,
        status,
        `${method} ${path}: ${JSON.stringify(data)}`,
      );
      return data;
    },
    async download(id) {
      const response = await fetch(origin + "/api/handouts/" + id + "/file", {
        headers: { Cookie: cookie },
      });
      assert.equal(response.status, 200);
      return response.text();
    },
  };
}
const admin = agent(),
  teacher = agent(),
  student = agent();
await admin.request("/auth/login", "POST", {
  username: config.ADMIN_USERNAME || "admin",
  password: config.ADMIN_PASSWORD,
});
const teacherUser = await admin.request(
  "/admin/teachers",
  "POST",
  { username: "verify_t_" + suffix, name: "部署验收临时教师", password },
  201,
);
const studentUser = await student.request("/auth/register", "POST", {
  username: "verify_s_" + suffix,
  name: "部署验收临时学生",
  password,
});
await teacher.request("/auth/login", "POST", {
  username: teacherUser.username,
  password,
});
const classroom = await teacher.request(
  "/classes",
  "POST",
  { name: "部署持久化验收（临时）", description: "验收后自动清理" },
  201,
);
await student.request("/classes/join", "POST", { code: classroom.invite_code });
const contents = "%PDF-1.4\nDocker persistent storage verification\n%%EOF";
const form = new FormData();
form.set("file", new Blob([contents]), "deployment-check.pdf");
form.set("title", "部署验收临时讲义");
const handout = await teacher.request(
  `/classes/${classroom.id}/handouts`,
  "POST",
  form,
  201,
);
assert.equal(await student.download(handout.id), contents);
await student.request("/admin/teachers", "GET", undefined, 403);
console.log(
  "PASS: container login, roles, classroom, join, upload and download.",
);
const recreate = !process.argv.includes("--skip-recreate");
if (recreate) {
  await docker(["compose", "down"]);
  await docker(["compose", "up", "-d", "--wait", "--wait-timeout", "180"]);
}
const detail = await student.request("/classes/" + classroom.id);
assert.equal(detail.classroom.name, classroom.name);
assert.equal(detail.members.length, 1);
assert.equal(detail.handouts.length, 1);
assert.equal(await student.download(handout.id), contents);
await admin.request("/auth/login", "POST", {
  username: config.ADMIN_USERNAME || "admin",
  password: config.ADMIN_PASSWORD,
});
console.log(
  recreate
    ? "PASS: container recreation preserves accounts, sessions, classroom membership and uploaded bytes."
    : "PASS: deployed accounts, sessions, classroom membership and uploaded bytes.",
);
const browser = await chromium.launch({ channel: "chrome", headless: true });
try {
  const page = await browser.newPage({
    viewport: { width: 1440, height: 1000 },
  });
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto(origin);
  await page
    .getByLabel("账号", { exact: true })
    .fill(config.ADMIN_USERNAME || "admin");
  await page.getByLabel("密码", { exact: true }).fill(config.ADMIN_PASSWORD);
  await page.getByRole("button", { name: "登录平台", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "教师管理.", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "退出登录", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "登录平台", exact: true }),
  ).toBeVisible();
  await mkdir(resolve(root, ".local/screenshots"), { recursive: true });
  await page.screenshot({
    path: resolve(root, ".local/screenshots/docker-login.png"),
    fullPage: true,
  });
  await page.getByLabel("账号", { exact: true }).fill(teacherUser.username);
  await page.getByLabel("密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "登录平台", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "创建班级", exact: true }).first(),
  ).toBeVisible();
  await page.goto(origin + "/#class/" + classroom.id);
  for (const extension of ["txt", "md"]) {
    const text =
      extension === "txt"
        ? "中文 TXT 讲义\r\n内容示例"
        : "# 中文 Markdown 讲义\n\n- 学习目标";
    const title = extension.toUpperCase() + " 格式验收";
    await page.getByRole("button", { name: "上传讲义", exact: true }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog.getByLabel("选择讲义文件")).toHaveAttribute(
      "accept",
      /\.txt,\.md/,
    );
    await dialog
      .getByLabel("选择讲义文件")
      .setInputFiles({
        name: "验收." + extension,
        mimeType: "application/octet-stream",
        buffer: Buffer.from(text),
      });
    await dialog.getByLabel("讲义标题").fill(title);
    await dialog.getByRole("button", { name: "上传讲义", exact: true }).click();
    await expect(dialog).toBeHidden();
    await expect(page.getByText(title, { exact: true })).toBeVisible();
    const event = page.waitForEvent("download");
    await page
      .getByRole("link", { name: "下载 " + title, exact: true })
      .click();
    const download = await event;
    assert.deepEqual(await readFile(await download.path()), Buffer.from(text));
    const current = await student.request("/classes/" + classroom.id);
    const record = current.handouts.find((h) => h.title === title);
    assert.equal(await student.download(record.id), text);
  }
  console.log(
    "PASS: browser TXT/MD file selection, upload, download and student access.",
  );
  assert.deepEqual(errors, []);
  console.log(
    "PASS: production frontend assets, browser login/logout and API proxy.",
  );
} finally {
  await browser.close();
}
const cleanup = await teacher.request("/classes/" + classroom.id);
for (const record of cleanup.handouts)
  await teacher.request("/handouts/" + record.id, "DELETE");
for (const id of [teacherUser.id, studentUser.id])
  assert.match(id, /^[0-9a-f]{32}$/);
const sql = `DELETE FROM memberships WHERE user_id='${studentUser.id}'; DELETE FROM classrooms WHERE owner_id='${teacherUser.id}'; DELETE FROM users WHERE id IN ('${teacherUser.id}','${studentUser.id}');`;
await docker(
  [
    "compose",
    "exec",
    "-T",
    "-e",
    `MYSQL_PWD=${config.DB_PASSWORD}`,
    "db",
    "mysql",
    "-h",
    "127.0.0.1",
    "-u",
    "teaching",
    "teaching",
    "--default-character-set=utf8mb4",
  ],
  sql,
);
await admin.request("/auth/logout", "POST");
console.log(
  "PASS: temporary verification accounts, classrooms and files removed.",
);
console.log(await docker(["compose", "ps"]));
