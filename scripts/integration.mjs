import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { mkdtemp, mkdir } from "node:fs/promises";
import { resolve } from "node:path";
import LocalMySQL, { runtimeDirectory } from "./mysql-runtime.mjs";
const root = resolve(import.meta.dirname, "..");
await mkdir(resolve(root, ".local"), { recursive: true });
await mkdir(runtimeDirectory, { recursive: true });
const dir = await mkdtemp(resolve(runtimeDirectory, "integration-"));
const mysql = new LocalMySQL({
  databaseDir: resolve(dir, "db"),
  port: 55433,
  database: "teaching",
});
let server;
let checks = 0;
const base = "http://127.0.0.1:8082/api";
function agent() {
  let cookie = "";
  return {
    get cookie() {
      return cookie;
    },
    async req(path, method = "GET", body, expected = 200, headers = {}) {
      const resp = await fetch(base + path, {
        method,
        headers: {
          ...(cookie ? { Cookie: cookie } : {}),
          ...(body instanceof FormData
            ? {}
            : { "Content-Type": "application/json" }),
          ...headers,
        },
        body:
          body === undefined
            ? undefined
            : body instanceof FormData
              ? body
              : JSON.stringify(body),
      });
      if (resp.headers.has("set-cookie"))
        cookie = resp.headers.get("set-cookie").split(";")[0];
      const data = await resp.json();
      assert.equal(
        resp.status,
        expected,
        `${method} ${path}: ${JSON.stringify(data)}`,
      );
      checks++;
      return data;
    },
    async file(path, status = 200) {
      const resp = await fetch(base + path, { headers: { Cookie: cookie } });
      assert.equal(resp.status, status, path);
      checks++;
      return resp;
    },
  };
}
function file(name = "lesson.pdf", content = "%PDF-1.7\nLecture content") {
  const f = new FormData();
  f.set("file", new Blob([content]), name);
  f.set("title", "第一讲：互联网基础");
  return f;
}
async function startServer() {
  server = spawn(
    resolve(
      root,
      ".local",
      process.env.TEST_SERVER_BIN ||
        (process.platform === "win32" ? "server.exe" : "server"),
    ),
    [],
    {
      windowsHide: true,
      env: {
        ...process.env,
        DATABASE_URL: mysql.databaseUrl,
        ADMIN_PASSWORD: "integration-admin-pass",
        UPLOAD_DIR: resolve(dir, "uploads"),
        APP_ORIGIN: "http://127.0.0.1:8082",
        LISTEN_ADDR: "127.0.0.1:8082",
      },
      stdio: ["ignore", "pipe", "pipe"],
    },
  );
  let logs = "";
  server.stderr.on("data", (b) => (logs += b));
  for (let i = 0; i < 100; i++) {
    if (server.exitCode !== null) throw new Error("API exited: " + logs);
    try {
      const r = await fetch(base + "/health");
      if (r.ok) return;
    } catch {}
    await new Promise((r) => setTimeout(r, 100));
  }
  throw new Error("API startup timed out: " + logs);
}
async function stopServer() {
  if (server && server.exitCode === null) {
    const exit = once(server, "exit");
    server.kill();
    await exit;
  }
}
try {
  await mysql.initialise();
  await mysql.start();
  await startServer();
  const guest = agent(),
    admin = agent(),
    teacher = agent(),
    otherTeacher = agent(),
    student = agent(),
    outsider = agent();
  await guest.req("/classes", "GET", undefined, 401);
  await admin.req(
    "/auth/login",
    "POST",
    { username: "admin", password: "wrong" },
    401,
  );
  await admin.req("/auth/login", "POST", {
    username: "admin",
    password: "integration-admin-pass",
  });
  const teacherDetails = {
    username: "teacher_a",
    name: "林老师",
    password: "test-password",
  };
  await admin.req("/admin/teachers", "POST", teacherDetails, 201);
  await admin.req(
    "/admin/teachers",
    "POST",
    { ...teacherDetails, username: "teacher_b" },
    201,
  );
  await admin.req("/admin/teachers", "POST", teacherDetails, 409);
  assert.equal((await admin.req("/admin/teachers")).length, 2);
  await teacher.req("/auth/login", "POST", {
    username: "teacher_a",
    password: "test-password",
  });
  await otherTeacher.req("/auth/login", "POST", {
    username: "teacher_b",
    password: "test-password",
  });
  await student.req(
    "/auth/register",
    "POST",
    {
      username: "student_a",
      name: "同学甲",
      password: "test-password",
      role: "admin",
    },
    400,
  );
  const me = await student.req("/auth/register", "POST", {
    username: "student_a",
    name: "同学甲",
    password: "test-password",
  });
  assert.equal(me.role, "student");
  await outsider.req("/auth/register", "POST", {
    username: "student_b",
    name: "同学乙",
    password: "test-password",
  });
  await guest.req(
    "/auth/register",
    "POST",
    { username: "student_a", name: "重复", password: "test-password" },
    409,
  );
  await student.req("/admin/teachers", "POST", teacherDetails, 403);
  await teacher.req("/admin/teachers", "GET", undefined, 403);
  await student.req("/classes", "POST", { name: "越权", description: "" }, 403);
  await teacher.req(
    "/classes",
    "POST",
    { name: "CSRF", description: "" },
    403,
    { Origin: "https://evil.invalid" },
  );
  const c = await teacher.req(
    "/classes",
    "POST",
    { name: "互联网应用开发", description: "从 Web 基础到完整应用" },
    201,
  );
  assert.equal(c.invite_code.length, 10);
  await otherTeacher.req("/classes/" + c.id, "GET", undefined, 403);
  await outsider.req("/classes/" + c.id, "GET", undefined, 403);
  await student.req("/classes/join", "POST", { code: "INVALID" }, 404);
  await student.req("/classes/join", "POST", { code: c.invite_code });
  await student.req("/classes/join", "POST", { code: c.invite_code }, 409);
  assert.equal((await student.req("/classes")).length, 1);
  const detail = await student.req("/classes/" + c.id);
  assert.equal(detail.classroom.invite_code, undefined);
  assert.equal(detail.members.length, 1);
  await student.req(
    "/classes/" + c.id,
    "PATCH",
    { name: "越权", description: "" },
    403,
  );
  await teacher.req("/classes/" + c.id, "PATCH", {
    name: "互联网开发课程",
    description: "更新简介",
  });
  await student.req(`/classes/${c.id}/handouts`, "POST", file(), 403);
  await teacher.req(`/classes/${c.id}/handouts`, "POST", file("bad.exe"), 400);
  await teacher.req(
    `/classes/${c.id}/handouts`,
    "POST",
    file("bad.pdf", "<script>bad</script>"),
    400,
  );
  await teacher.req(
    `/classes/${c.id}/handouts`,
    "POST",
    file("big.pdf", new Uint8Array(50 * 1024 * 1024 + 1)),
    413,
  );
  for (const [name, content, mime] of [
    ["课堂笔记.TXT", "中文讲义\r\n第一课\t😀", "text/plain"],
    [
      "课程说明.md",
      "\uFEFF# 教学目标\n<script>仅为文本</script>",
      "text/markdown",
    ],
  ]) {
    const textHandout = await teacher.req(
      `/classes/${c.id}/handouts`,
      "POST",
      file(name, content),
      201,
    );
    const response = await student.file(
      `/handouts/${textHandout.id}/file?preview=1`,
    );
    assert.deepEqual(
      Buffer.from(await response.arrayBuffer()),
      Buffer.from(content),
    );
    assert.equal(
      response.headers.get("content-type"),
      mime + "; charset=utf-8",
    );
    assert.match(response.headers.get("content-disposition"), /attachment/);
    assert.equal(response.headers.get("x-content-type-options"), "nosniff");
    await outsider.file(`/handouts/${textHandout.id}/file`, 403);
    await teacher.req(`/handouts/${textHandout.id}`, "DELETE");
  }
  for (const content of [
    "",
    "\uFEFF",
    "text\x00binary",
    new Uint8Array([0xff, 0xfe, 0x41, 0]),
  ]) {
    const error = await teacher.req(
      `/classes/${c.id}/handouts`,
      "POST",
      file("invalid.txt", content),
      400,
    );
    assert.match(error.error, /UTF-8/);
  }
  await student.req(
    `/classes/${c.id}/handouts`,
    "POST",
    file("student.md", "# 不能上传"),
    403,
  );
  const h = await teacher.req(`/classes/${c.id}/handouts`, "POST", file(), 201);
  const download = await student.file(`/handouts/${h.id}/file`);
  assert.equal(await download.text(), "%PDF-1.7\nLecture content");
  assert.match(download.headers.get("content-disposition"), /attachment/);
  const preview = await student.file(`/handouts/${h.id}/file?preview=1`);
  assert.match(preview.headers.get("content-disposition"), /inline/);
  await preview.arrayBuffer();
  await outsider.file(`/handouts/${h.id}/file`, 403);
  await otherTeacher.file(`/handouts/${h.id}/file`, 403);
  await guest.file(`/handouts/${h.id}/file`, 401);
  await student.req(`/handouts/${h.id}`, "DELETE", undefined, 403);
  await teacher.req(`/classes/${c.id}/archive`, "POST", { archived: true });
  await outsider.req("/classes/join", "POST", { code: c.invite_code }, 409);
  await teacher.req(`/classes/${c.id}/handouts`, "POST", file(), 409);
  await teacher.req(
    `/classes/${c.id}/handouts`,
    "POST",
    file("archived.txt", "归档后不能上传"),
    409,
  );
  await teacher.req(
    `/classes/${c.id}`,
    "PATCH",
    { name: "locked", description: "" },
    403,
  );
  await teacher.req(`/handouts/${h.id}`, "DELETE", undefined, 409);
  await student.file(`/handouts/${h.id}/file`);
  await teacher.req(`/classes/${c.id}/archive`, "POST", { archived: false });
  await teacher.req(`/classes/${c.id}/members/${me.id}`, "DELETE");
  await student.file(`/handouts/${h.id}/file`, 403);
  await student.req("/classes/join", "POST", { code: c.invite_code });
  await student.req(`/classes/${c.id}/members/${me.id}`, "DELETE");
  await student.file(`/handouts/${h.id}/file`, 403);
  await student.req("/classes/join", "POST", { code: c.invite_code });
  // API process restart must preserve sessions, classroom and stored file.
  await stopServer();
  await startServer();
  assert.equal(
    (await teacher.req("/classes/" + c.id)).classroom.name,
    "互联网开发课程",
  );
  await student.file(`/handouts/${h.id}/file`);
  await teacher.req(`/handouts/${h.id}`, "DELETE");
  await student.file(`/handouts/${h.id}/file`, 404);
  const oldCookie = student.cookie;
  await student.req("/auth/logout", "POST");
  await guest.req("/auth/me", "GET", undefined, 401, { Cookie: oldCookie });
  await student.req("/auth/login", "POST", {
    username: "student_a",
    password: "test-password",
  });
  // Sessions live in MySQL; expire this user's rows through mysql.exe.
  await mysql.query(
    `UPDATE sessions s JOIN users u ON u.id = s.user_id
      SET s.expires_at = UTC_TIMESTAMP() - INTERVAL 1 HOUR
      WHERE u.username = 'student_a'`,
  );
  await student.req("/auth/me", "GET", undefined, 401);
  const hashes = await mysql.query("SELECT password_hash FROM users");
  assert.ok(hashes.length > 0, "users table should hold rows");
  assert.ok(
    hashes.split(/\r?\n/).every((hash) => hash.startsWith("$2")),
    "all password hashes should be bcrypt: " + hashes,
  );
  console.log(
    `PASS: ${checks} real-MySQL HTTP checks; roles, CSRF, sessions, classrooms, uploads, authorization and restart persistence.`,
  );
} finally {
  await stopServer();
  await mysql.stop();
}
