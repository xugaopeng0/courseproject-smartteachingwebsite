import { chromium, expect } from "@playwright/test";
import { readFile, writeFile, mkdir } from "node:fs/promises";
import { resolve } from "node:path";
import { randomBytes } from "node:crypto";
const root = resolve(import.meta.dirname, "..");
const shots = resolve(root, ".local/screenshots");
await mkdir(shots, { recursive: true });
const creds = JSON.parse(
  await readFile(resolve(root, ".local/dev-credentials.json"), "utf8"),
);
const suffix = Date.now().toString(36),
  password = randomBytes(12).toString("hex");
const teacher = "teacher_" + suffix,
  student = "student_" + suffix;
const browser = await chromium.launch({ channel: "chrome", headless: true });
const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
const errors = [];
page.on("pageerror", (e) => errors.push(e.message));
const base = "http://127.0.0.1:5173";
async function login(p, username, pw) {
  await p.getByLabel("账号", { exact: true }).fill(username);
  await p.getByLabel("密码", { exact: true }).fill(pw);
  await p.getByRole("button", { name: "登录平台", exact: true }).click();
}
async function createClass(name, description) {
  await page
    .getByRole("button", { name: "创建班级", exact: true })
    .first()
    .click();
  const d = page.getByRole("dialog");
  await d.getByLabel("班级名称").fill(name);
  await d.getByLabel("班级简介").fill(description);
  await d.getByRole("button", { name: "创建班级", exact: true }).click();
  await expect(page.getByRole("heading", { name, exact: true })).toBeVisible();
}
function pdf() {
  const objects = [
    "<< /Type /Catalog /Pages 2 0 R >>",
    "<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
    "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>",
    "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
  ];
  const stream = "BT /F1 24 Tf 72 700 Td (Welcome to Zhixu Classroom) Tj ET";
  objects.push(`<< /Length ${stream.length} >>\nstream\n${stream}\nendstream`);
  let out = "%PDF-1.4\n",
    offsets = [0];
  for (let i = 0; i < objects.length; i++) {
    offsets.push(Buffer.byteLength(out));
    out += `${i + 1} 0 obj\n${objects[i]}\nendobj\n`;
  }
  const xref = Buffer.byteLength(out);
  out += `xref\n0 6\n0000000000 65535 f \n${offsets
    .slice(1)
    .map((n) => String(n).padStart(10, "0") + " 00000 n \n")
    .join("")}trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF\n`;
  return Buffer.from(out);
}
try {
  await page.goto(base);
  await expect(
    page.getByRole("button", { name: "登录平台", exact: true }),
  ).toBeVisible();
  await page.screenshot({
    path: resolve(shots, "login-desktop.png"),
    fullPage: true,
  });
  await login(page, "admin", creds.admin);
  await page.getByRole("button", { name: "创建教师账号", exact: true }).click();
  let dialog = page.getByRole("dialog");
  await dialog.getByLabel("教师姓名").fill("林知远（演示教师）");
  await dialog.getByLabel("登录账号").fill(teacher);
  await dialog.getByLabel("初始密码").fill(password);
  await dialog.getByRole("button", { name: "创建账号", exact: true }).click();
  await expect(dialog).toBeHidden();
  await page.getByRole("button", { name: "退出登录", exact: true }).click();
  await login(page, teacher, password);
  await createClass(
    "互联网应用开发 · 示例班级",
    "从 Web 基础到全栈应用，在实践中探索互联网技术的无限可能。",
  );
  const classHash = new URL(page.url()).hash;
  const code = (await page.locator(".invite-bar strong").innerText()).trim();
  await page.getByRole("button", { name: "上传讲义", exact: true }).click();
  dialog = page.getByRole("dialog");
  await dialog
    .getByLabel("选择讲义文件")
    .setInputFiles({
      name: "第一讲-Web应用基础.pdf",
      mimeType: "application/pdf",
      buffer: pdf(),
    });
  await dialog.getByLabel("讲义标题").fill("第一讲：Web 应用与互联网基础");
  await dialog.getByRole("button", { name: "上传讲义", exact: true }).click();
  await expect(dialog).toBeHidden();
  await expect(
    page.getByText("第一讲：Web 应用与互联网基础", { exact: true }),
  ).toBeVisible();
  await page.screenshot({
    path: resolve(shots, "classroom-desktop.png"),
    fullPage: true,
  });
  await page.getByRole("button", { name: "返回我的班级" }).click();
  await createClass(
    "数据结构与算法 · 示例班级",
    "理解数据的组织方式，培养解决问题的思维。",
  );
  await page.getByRole("button", { name: "返回我的班级" }).click();
  await createClass(
    "软件工程实践 · 示例班级",
    "从需求到交付，体验团队协作与规范开发。",
  );
  await page.getByRole("button", { name: "返回我的班级" }).click();
  await expect(page.locator(".class-card")).toHaveCount(3);
  await page.screenshot({
    path: resolve(shots, "classrooms-desktop.png"),
    fullPage: true,
  });
  await page.getByLabel("搜索班级").fill("互联网");
  await expect(page.locator(".class-card")).toHaveCount(1);
  await page.getByLabel("搜索班级").fill("");
  const context = await browser.newContext({
    viewport: { width: 390, height: 844 },
    isMobile: true,
    deviceScaleFactor: 1,
  });
  const mobile = await context.newPage();
  mobile.on("pageerror", (e) => errors.push(e.message));
  await mobile.goto(base);
  await mobile.screenshot({
    path: resolve(shots, "login-mobile.png"),
    fullPage: true,
  });
  await mobile.getByRole("button", { name: "学生注册", exact: true }).click();
  await mobile.getByLabel("姓名", { exact: true }).fill("陈同学（演示学生）");
  await mobile.getByLabel("账号", { exact: true }).fill(student);
  await mobile.getByLabel("密码", { exact: true }).fill(password);
  await mobile
    .getByRole("button", { name: "注册并进入平台", exact: true })
    .click();
  await mobile
    .getByRole("button", { name: "加入班级", exact: true })
    .first()
    .click();
  dialog = mobile.getByRole("dialog");
  await dialog.getByLabel("班级邀请码").fill(code);
  await dialog.getByRole("button", { name: "加入班级", exact: true }).click();
  await expect(dialog).toBeHidden();
  await expect(
    mobile.getByText("第一讲：Web 应用与互联网基础", { exact: true }),
  ).toBeVisible();
  await expect(
    mobile.getByRole("button", { name: "上传讲义", exact: true }),
  ).toHaveCount(0);
  await mobile.screenshot({
    path: resolve(shots, "classroom-mobile.png"),
    fullPage: true,
  });
  expect(
    await mobile.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBeTruthy();
  const downloadPromise = mobile.waitForEvent("download");
  await mobile
    .getByRole("link", { name: "下载 第一讲：Web 应用与互联网基础" })
    .click();
  const download = await downloadPromise;
  expect(download.suggestedFilename()).toBe("第一讲-Web应用基础.pdf");
  await page.goto(base + "/" + classHash);
  await page.getByRole("button", { name: "班级设置", exact: true }).click();
  await page.getByRole("button", { name: "归档班级", exact: true }).click();
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "确认", exact: true })
    .click();
  await expect(
    page.getByText("此班级已归档，现有讲义仍可查看和下载。"),
  ).toBeVisible();
  await page.getByRole("button", { name: "恢复班级", exact: true }).click();
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "确认", exact: true })
    .click();
  await expect(
    page.getByText("此班级已归档，现有讲义仍可查看和下载。"),
  ).toHaveCount(0);
  await mobile.getByRole("button", { name: "班级设置", exact: true }).click();
  await mobile.getByRole("button", { name: "退出班级", exact: true }).click();
  await mobile
    .getByRole("dialog")
    .getByRole("button", { name: "确认", exact: true })
    .click();
  await expect(
    mobile.getByRole("heading", { name: "你的学习旅程，从加入班级开始" }),
  ).toBeVisible();
  await page.getByRole("button", { name: "班级讲义", exact: false }).click();
  await page.getByRole("button", { name: "编辑班级", exact: true }).click();
  await page
    .getByRole("dialog")
    .getByLabel("班级简介")
    .fill("从 Web 基础到全栈应用，在实践中探索互联网技术。");
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "保存修改", exact: true })
    .click();
  await expect(page.getByRole("dialog")).toBeHidden();
  await page.getByRole("button", { name: "返回我的班级" }).click();
  await page.getByRole("button", { name: "讲义资源", exact: false }).click();
  await expect(
    page.getByText("第一讲：Web 应用与互联网基础", { exact: true }),
  ).toBeVisible();
  expect(errors).toEqual([]);
  await writeFile(
    resolve(root, ".local/browser-demo.json"),
    JSON.stringify(
      {
        teacher,
        student,
        password,
        classHash,
        note: "Development-only demo accounts created by browser regression test",
      },
      null,
      2,
    ),
  );
  console.log(
    "PASS: browser login, teacher creation, classroom creation/search, upload/download, student registration/join/leave, archive/restore, edit, resources, mobile layout; no page errors.",
  );
} finally {
  await browser.close();
}
