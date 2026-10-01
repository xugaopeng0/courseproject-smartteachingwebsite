// Portable MySQL 8.4 runtime for local development and integration tests.
// No Docker and no npm driver: binaries are extracted into .tools/mysql and every
// probe is a plain mysql.exe / mysqladmin.exe call.
//
// Windows MySQL cannot bootstrap from non-ASCII paths under a legacy codepage.
// Use an ASCII runtime directory for local testing only; Docker does not need this.
import { spawn } from "node:child_process";
import { once } from "node:events";
import {
  access,
  cp,
  mkdir,
  open,
  readdir,
  readFile,
  rename,
  rm,
  writeFile,
} from "node:fs/promises";
import { resolve } from "node:path";
import { tmpdir } from "node:os";
import { createHash } from "node:crypto";

const project = resolve(import.meta.dirname, "..");
// The path hash keeps the runtime directory ASCII-only and stable across runs.
export const runtimeDirectory = resolve(
  tmpdir(),
  "zhixu-" + createHash("sha256").update(project).digest("hex").slice(0, 12),
);
export const toolDirectory = resolve(project, ".tools", "mysql");
// .tools lives inside the project path, which contains non-ASCII characters on many
// Chinese Windows installations. mysqld 8.4.6 refuses to start with such a basedir
// (it dies before writing its error log), so bin/ and share/ are staged into the
// ASCII runtime directory and basedir points there instead.
export const asciiRuntimeDirectory = resolve(runtimeDirectory, "mysql");
// Official CDN link. The USTC path recorded earlier returns 404 for every
// Downloads/MySQL-8.4 path, so override with MYSQL_ZIP_URL when a mirror works.
export const defaultZipUrl =
  "https://dev.mysql.com/get/Downloads/MySQL-8.4/mysql-8.4.6-winx64.zip";
const bootstrapDatabase = "teaching";

function formatBytes(bytes) {
  return bytes >= 1024 * 1024 * 1024
    ? (bytes / 1024 / 1024 / 1024).toFixed(2) + " GB"
    : (bytes / 1024 / 1024).toFixed(1) + " MB";
}

// stderr carries mysqld's diagnostics; keep both streams readable for error reports.
function logOf(stdout, stderr) {
  const text = Buffer.concat([stdout, stderr]).toString("utf8").trim();
  return text || "(no output)";
}

// True when the path exists, file or directory.
async function containsFile(directory, name) {
  try {
    await access(resolve(directory, name));
    return true;
  } catch {
    return false;
  }
}

async function moveInto(source, destination) {
  try {
    await rename(source, destination);
  } catch {
    // rename fails across volumes; fall back to copying the tree.
    await cp(source, destination, { recursive: true });
    await rm(source, { recursive: true, force: true });
  }
}

// Spawn a helper process. Streams go to files instead of pipes so the output stays
// readable even when the caller cannot open pipes.
async function runProcess(command, args, options = {}) {
  const outHandle = options.stdout ? await open(options.stdout, "w") : "ignore";
  const errHandle = options.stderr ? await open(options.stderr, "w") : "ignore";
  const child = spawn(command, args, {
    cwd: options.cwd,
    windowsHide: true,
    stdio: [options.stdin ?? "ignore", outHandle, errHandle],
  });
  let timedOut = false;
  let timer;
  const exited = once(child, "exit");
  try {
    const result = await Promise.race([
      exited,
      new Promise((r) => {
        timer = setTimeout(() => {
          timedOut = true;
          child.kill();
          r(null);
        }, options.timeout ?? 120_000);
      }),
    ]);
    if (timedOut) {
      // Windows may need a moment before the killed process settles.
      await Promise.race([exited, new Promise((r) => setTimeout(r, 5_000))]);
      throw new Error(
        `Process timed out: ${command} ${args.join(" ")} (${options.timeout ?? 120_000} ms)`,
      );
    }
    return {
      code: result[0],
      stdout: options.stdout ? await readFile(options.stdout) : Buffer.alloc(0),
      stderr: options.stderr ? await readFile(options.stderr) : Buffer.alloc(0),
    };
  } finally {
    clearTimeout(timer);
    if (outHandle !== "ignore") await outHandle.close();
    if (errHandle !== "ignore") await errHandle.close();
  }
}

async function download(url, destination, onProgress) {
  // 260 MB from the official CDN; allow for a slow link.
  const response = await fetch(url, {
    redirect: "follow",
    signal: AbortSignal.timeout(45 * 60_000),
  });
  if (!response.ok)
    throw new Error(`MySQL download failed: HTTP ${response.status} for ${url}`);
  const total = Number(response.headers.get("content-length")) || 0;
  await mkdir(resolve(destination, ".."), { recursive: true });
  const handle = await open(destination, "w");
  let received = 0;
  let reported = -1;
  try {
    for await (const chunk of response.body) {
      await handle.write(chunk);
      received += chunk.length;
      const step = total ? Math.floor((received / total) * 10) : -1;
      if (step !== reported) {
        reported = step;
        onProgress(
          total
            ? `MySQL download ${step * 10}% (${formatBytes(received)} / ${formatBytes(total)})`
            : `MySQL download ${formatBytes(received)}`,
        );
      }
    }
  } finally {
    await handle.close();
  }
  if (!received) throw new Error("MySQL download produced no data: " + url);
  return received;
}

// tar (bsdtar on Windows) unpacks the ZIP archive built into the runtime.
async function extractZip(archive, destination) {
  await mkdir(destination, { recursive: true });
  const { code, stdout, stderr } = await runProcess(
    "tar",
    ["-xf", archive, "-C", destination],
    {
      cwd: runtimeDirectory,
      timeout: 15 * 60_000,
      stdout: resolve(runtimeDirectory, "mysql-tar.out"),
      stderr: resolve(runtimeDirectory, "mysql-tar.err"),
    },
  );
  if (code !== 0)
    throw new Error("MySQL extraction failed: " + logOf(stdout, stderr));
}

async function findBinaryDirectory(extracted) {
  if (await containsFile(resolve(extracted, "bin"), "mysqld.exe"))
    return extracted;
  for (const name of await readdir(extracted)) {
    if (await containsFile(resolve(extracted, name, "bin"), "mysqld.exe"))
      return resolve(extracted, name);
  }
  throw new Error("mysqld.exe not found in the extracted MySQL archive");
}

export default class LocalMySQL {
  /**
   * @param {{ databaseDir: string, port: number, user?: string, password?: string, database?: string }} options
   *   databaseDir must live under the ASCII runtimeDirectory. The default account is
   *   root with an empty password, as created by mysqld --initialize-insecure.
   */
  constructor(options) {
    this.options = options;
    this.databaseDir = options.databaseDir;
    this.port = options.port;
    this.user = options.user ?? "root";
    this.password = options.password ?? "";
    this.database = options.database ?? bootstrapDatabase;
    this.zipUrl = process.env.MYSQL_ZIP_URL || defaultZipUrl;
    this.tools = toolDirectory;
    this.logFile = resolve(runtimeDirectory, `mysqld-${this.port}.log`);
    this.bin = null;
    this.child = null;
    this.exit = null;
    this.ready = false;
  }

  /** DATABASE_URL for the Go service. */
  get databaseUrl() {
    return (
      `${this.user}@tcp(127.0.0.1:${this.port})/${this.database}` +
      "?parseTime=true&loc=UTC&charset=utf8mb4"
    );
  }

  /** Extract the ZIP bundle into .tools/mysql, downloading it once when missing. */
  async prepare() {
    if (this.bin) return;
    await mkdir(runtimeDirectory, { recursive: true });
    const extension = process.platform === "win32" ? ".exe" : "";
    if (!(await containsFile(resolve(this.tools, "bin"), "mysqld" + extension)))
      await this.install();
    await this.stage();
  }

  /** Copy the binaries needed at runtime into the ASCII runtime directory. */
  async stage() {
    const from = resolve(this.tools, "bin");
    const to = resolve(asciiRuntimeDirectory, "bin");
    if (
      !(await containsFile(to, "mysqld" + (process.platform === "win32" ? ".exe" : "")))
    ) {
      await rm(asciiRuntimeDirectory, { recursive: true, force: true });
      await mkdir(asciiRuntimeDirectory, { recursive: true });
      await cp(from, to, { recursive: true });
      // errmsg.sys and the charset tables live in share/; mysqld aborts without them.
      const share = resolve(this.tools, "share");
      if (await containsFile(share, "english")) {
        await cp(share, resolve(asciiRuntimeDirectory, "share"), { recursive: true });
      }
    }
    const extension = process.platform === "win32" ? ".exe" : "";
    this.mysqld = resolve(to, "mysqld" + extension);
    this.mysql = resolve(to, "mysql" + extension);
    this.mysqladmin = resolve(to, "mysqladmin" + extension);
    this.bin = asciiRuntimeDirectory;
  }

  async install() {
    const archive = resolve(runtimeDirectory, "mysql.zip");
    const unpacked = resolve(runtimeDirectory, "mysql-unpacked");
    let downloaded = false;
    try {
      await access(archive);
      console.error(`MySQL archive already downloaded: ${archive}`);
    } catch {
      downloaded = true;
      const started = Date.now();
      const bytes = await download(this.zipUrl, archive, (message) =>
        console.error(message),
      );
      console.error(
        `MySQL ${formatBytes(bytes)} downloaded in ${((Date.now() - started) / 1000).toFixed(1)} s`,
      );
    }
    await rm(unpacked, { recursive: true, force: true });
    await extractZip(archive, unpacked);
    const source = await findBinaryDirectory(unpacked);
    await rm(this.tools, { recursive: true, force: true });
    await mkdir(resolve(this.tools, ".."), { recursive: true });
    await moveInto(source, this.tools);
    await rm(unpacked, { recursive: true, force: true });
    if (downloaded) await rm(archive, { force: true });
  }

  /** Create the data directory with mysqld --initialize-insecure. */
  async initialise() {
    await this.prepare();
    await rm(this.databaseDir, { recursive: true, force: true });
    await mkdir(this.databaseDir, { recursive: true });
    const previous = await readFile(this.logFile, "utf8").catch(() => null);
    await rm(this.logFile, { force: true }).catch(() => {});
    if (previous !== null) await writeFile(this.logFile, previous).catch(() => {});
    const optionFile = resolve(runtimeDirectory, `mysql-init-${this.port}.ini`);
    await writeFile(
      optionFile,
      [
        "[mysqld]",
        "character_set_server=utf8mb4",
        "collation_server=utf8mb4_0900_ai_ci",
        "default_time_zone=+00:00",
        "ngram_token_size=2",
        `basedir="${asciiRuntimeDirectory}"`,
        `datadir="${this.databaseDir}"`,
        `log-error="${this.logFile}"`,
        "",
      ].join("\r\n"),
    );
    const started = Date.now();
    const { code, stdout, stderr } = await runProcess(
      this.mysqld,
      [
        `--defaults-file=${optionFile}`,
        "--initialize-insecure",
        // MySQL 8.4 sizes its redo files in 10 MiB steps; the local runtime stays
        // small on purpose, so pin the smallest capacity.
        "--innodb-redo-log-capacity=10485760",
      ],
      {
        cwd: runtimeDirectory,
        timeout: 15 * 60_000,
        stdout: resolve(runtimeDirectory, "mysql-init.out"),
        stderr: resolve(runtimeDirectory, "mysql-init.err"),
      },
    );
    if (code !== 0)
      throw new Error(
        "MySQL initialization failed: " +
          logOf(stdout, stderr) +
          "\n" +
          (await readFile(this.logFile, "utf8").catch(() => "(no error log)")),
      );
    console.error(
      `MySQL data directory initialized in ${((Date.now() - started) / 1000).toFixed(1)} s`,
    );
  }

  /** Start mysqld on the fixed port and wait until SELECT 1 answers. */
  async start() {
    await this.prepare();
    if (!(await containsFile(this.databaseDir, "mysql")))
      throw new Error(
        "MySQL data directory is not initialized (run initialise() first): " +
          this.databaseDir,
      );
    const started = Date.now();
    // Start from an empty error log so a failed start reports only this attempt.
    await rm(this.logFile, { force: true }).catch(() => {});
    this.child = spawn(
      this.mysqld,
      [
        `--basedir=${asciiRuntimeDirectory}`,
        `--datadir=${this.databaseDir}`,
        `--port=${this.port}`,
        "--bind-address=127.0.0.1",
        "--mysqlx=0",
        "--character-set-server=utf8mb4",
        "--collation-server=utf8mb4_0900_ai_ci",
        "--default-time-zone=+00:00",
        "--ngram-token-size=2",
        `--log-error=${this.logFile}`,
      ],
      {
        cwd: runtimeDirectory,
        windowsHide: true,
        stdio: ["ignore", "ignore", "ignore"],
      },
    );
    this.exit = once(this.child, "exit");
    this.exit.catch(() => {});
    this.child.on("error", (error) => {
      this.ready = false;
      console.error("mysqld spawn error: " + error.message);
    });
    try {
      await this.waitUntilReady(started);
    } catch (error) {
      // A failed start must not leave mysqld holding the port.
      await this.stop().catch(() => {});
      throw error;
    }
  }

  async waitUntilReady(started) {
    let probeError = "";
    for (let i = 0; i < 120; i++) {
      if (this.child.exitCode !== null || this.child.signalCode) {
        const logs = await readFile(this.logFile, "utf8").catch(() => "");
        throw new Error("MySQL exited during startup: " + (logs || "(no log)"));
      }
      try {
        await this.query("SELECT 1", { database: "" });
        if (!this.ready) {
          this.ready = true;
          console.error(
            `MySQL 8.4 listening on 127.0.0.1:${this.port} after ` +
              `${((Date.now() - started) / 1000).toFixed(1)} s`,
          );
        }
        await this.ensureDatabase();
        return;
      } catch (error) {
        // Keep the last probe failure: a broken client invocation must not look
        // like a slow server.
        probeError = error.message;
        await new Promise((r) => setTimeout(r, 250));
      }
    }
    const logs = await readFile(this.logFile, "utf8").catch(() => "");
    throw new Error(
      `MySQL startup timed out after ${((Date.now() - started) / 1000).toFixed(0)} s: ` +
        (logs || "(no error log)") +
        "\nlast probe error: " +
        probeError,
    );
  }

  async ensureDatabase() {
    await this.query(
      `CREATE DATABASE IF NOT EXISTS \`${this.database}\` ` +
        "CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci",
      { database: "" },
    );
  }

  /**
   * Run SQL through mysql.exe. Statements arrive on stdin instead of the command line
   * and stdout is decoded as UTF-8, so Chinese literals survive in both directions.
   * @param {string} sql
   * @param {{ database?: string, timeout?: number }} [options] database: "" means none.
   */
  async query(sql, options = {}) {
    await this.prepare();
    const database =
      options.database === undefined ? this.database : options.database;
    const stamp = `${this.port}-${Date.now()}-${Math.floor(Math.random() * 1e6)}`;
    const script = resolve(runtimeDirectory, `mysql-query-${stamp}.sql`);
    const stdoutPath = resolve(runtimeDirectory, `mysql-out-${stamp}.log`);
    const stderrPath = resolve(runtimeDirectory, `mysql-err-${stamp}.log`);
    await writeFile(script, sql.endsWith(";") ? sql : sql + ";", "utf8");
    const stdin = await open(script, "r");
    try {
      const args = [
        "--protocol=TCP",
        "--host=127.0.0.1",
        `--port=${this.port}`,
        `--user=${this.user}`,
        "--default-character-set=utf8mb4",
        "--batch",
        "--raw",
        "--skip-column-names",
      ];
      if (this.password) args.push(`--password=${this.password}`);
      if (database) args.push(`--database=${database}`);
      const { code, stdout, stderr } = await runProcess(this.mysql, args, {
        cwd: runtimeDirectory,
        timeout: options.timeout ?? 60_000,
        stdin,
        stdout: stdoutPath,
        stderr: stderrPath,
      });
      if (code !== 0)
        throw new Error(
          `mysql client failed (exit ${code}): ` + logOf(stdout, stderr),
        );
      return stdout.toString("utf8").trim();
    } finally {
      await stdin.close();
      await rm(script, { force: true }).catch(() => {});
      await rm(stdoutPath, { force: true }).catch(() => {});
      await rm(stderrPath, { force: true }).catch(() => {});
    }
  }

  /** Graceful mysqladmin shutdown, falling back to killing the process. */
  async stop() {
    if (!this.child || this.child.exitCode !== null || this.child.signalCode) {
      this.ready = false;
      return;
    }
    const child = this.child;
    const args = [
      "--protocol=TCP",
      "--host=127.0.0.1",
      `--port=${this.port}`,
      `--user=${this.user}`,
      "--connect-timeout=5",
      "shutdown",
    ];
    if (this.password) args.push(`--password=${this.password}`);
    try {
      const { code, stdout, stderr } = await runProcess(this.mysqladmin, args, {
        cwd: runtimeDirectory,
        timeout: 60_000,
        stdout: resolve(runtimeDirectory, `mysqladmin-out-${this.port}.log`),
        stderr: resolve(runtimeDirectory, `mysqladmin-err-${this.port}.log`),
      });
      if (code !== 0)
        console.error(
          "mysqladmin shutdown returned " + code + ": " + logOf(stdout, stderr),
        );
    } catch (error) {
      console.error("mysqladmin shutdown failed: " + error.message);
    }
    const exited = await Promise.race([
      this.exit.then(() => true),
      new Promise((r) => setTimeout(() => r(false), 30_000)),
    ]);
    if (!exited) {
      console.error("mysqld ignored shutdown; killing process " + child.pid);
      if (process.platform === "win32")
        await runProcess("taskkill", ["/PID", String(child.pid), "/T", "/F"], {
          cwd: runtimeDirectory,
          timeout: 30_000,
          stdout: resolve(runtimeDirectory, `taskkill-out-${this.port}.log`),
          stderr: resolve(runtimeDirectory, `taskkill-err-${this.port}.log`),
        }).catch(() => {});
      else child.kill("SIGKILL");
      await Promise.race([
        this.exit,
        new Promise((r) => setTimeout(r, 15_000)),
      ]);
    }
    this.child = null;
    this.ready = false;
  }
}
