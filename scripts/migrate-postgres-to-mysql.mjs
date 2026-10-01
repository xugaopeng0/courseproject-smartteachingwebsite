// PostgreSQL → MySQL 一次性数据迁移。
//
// 设计要点：
//  - 默认只操作数据卷的**副本**（<volume>-drill），原卷始终只读，便于先演练再上线。
//  - 只迁移基础业务表（用户、会话、班级、成员、讲义元数据）；知识库切片在迁移后由 worker 重建。
//  - 写入使用 INSERT ... ON DUPLICATE KEY UPDATE，重复执行不产生重复行。
//  - 迁移前后对上传卷中每份讲义计算 SHA-256，确认文件未被改动。
//
// 用法：
//   node scripts/migrate-postgres-to-mysql.mjs            # 演练：只导出并报告差异，不写 MySQL
//   node scripts/migrate-postgres-to-mysql.mjs --apply    # 导出并写入 MySQL
//   node scripts/migrate-postgres-to-mysql.mjs --apply --keep-drill
import { spawn } from "node:child_process";
import { mkdir, readFile, writeFile, rm } from "node:fs/promises";
import { createHash } from "node:crypto";
import { resolve, delimiter, join } from "node:path";

const root = resolve(import.meta.dirname, "..");
const dockerBinDir = process.platform === "win32" ? resolve(process.env.LOCALAPPDATA, "Programs/DockerDesktop/resources/bin") : "";
const dockerPath = process.platform === "win32" ? resolve(dockerBinDir, "docker.exe") : "docker";
const args = process.argv.slice(2);
const apply = args.includes("--apply");
const keepDrill = args.includes("--keep-drill");
const volumeIndex = args.indexOf("--volume");
const sourceVolume = volumeIndex >= 0 ? args[volumeIndex + 1] : "zhixu-teaching_database";
const drillVolume = `${sourceVolume}-drill`;
const drillContainer = "zhixu-pg-drill";
const pgImage = process.env.PG_DRILL_IMAGE || "pgvector/pgvector:0.8.6-pg18-trixie";
const pgPort = 15432;
const workDir = resolve(root, ".local/migration");

function docker(commandArgs, input) {
  return new Promise((resolvePromise, reject) => {
    const child = spawn(dockerPath, commandArgs, {
      cwd: root,
      windowsHide: true,
      env: { ...process.env, PATH: dockerBinDir + delimiter + process.env.PATH },
      stdio: ["pipe", "pipe", "pipe"],
    });
    let output = "";
    child.stdout.on("data", (part) => (output += part));
    child.stderr.on("data", (part) => (output += part));
    child.on("error", reject);
    child.on("exit", (code) => (code === 0 ? resolvePromise(output) : reject(new Error(`docker ${commandArgs[0]} 失败: ${output.trim()}`))));
    child.stdin.end(input);
  });
}

const config = Object.fromEntries(
  (await readFile(resolve(root, ".env"), "utf8"))
    .split(/\r?\n/)
    .filter((line) => line && !line.startsWith("#") && line.includes("="))
    .map((line) => [line.slice(0, line.indexOf("=")), line.slice(line.indexOf("=") + 1)]),
);

const tables = [
  { name: "users", columns: ["id", "username", "name", "password_hash", "role"], time: { created_at: "timestamptz" } },
  { name: "classrooms", columns: ["id", "name", "description", "owner_id", "invite_code", "archived"], time: { created_at: "timestamptz" } },
  { name: "memberships", columns: ["class_id", "user_id"], time: { joined_at: "timestamptz" } },
  { name: "handouts", columns: ["id", "class_id", "title", "filename", "stored_name", "size", "mime"], time: { uploaded_at: "timestamptz" } },
  { name: "sessions", columns: ["token_hash", "user_id"], time: { expires_at: "timestamptz" } },
];

// 时间统一导出为 UTC 的 datetime 文本，避免时区偏移写进 MySQL。
function exportExpression(table) {
  const parts = [];
  for (const column of table.columns) {
    parts.push(column);
  }
  for (const [column, type] of Object.entries(table.time)) {
    parts.push(type === "timestamptz" ? `to_char(${column} AT TIME ZONE 'UTC','YYYY-MM-DD HH24:MI:SS.US') AS ${column}` : column);
  }
  return `SELECT ${parts.join(",")} FROM ${table.name} ORDER BY 1`;
}

function parseCsv(text) {
  const rows = [];
  let row = [];
  let field = "";
  let quoted = false;
  for (let i = 0; i < text.length; i++) {
    const char = text[i];
    if (quoted) {
      if (char === '"') {
        if (text[i + 1] === '"') {
          field += '"';
          i++;
        } else {
          quoted = false;
        }
      } else {
        field += char;
      }
      continue;
    }
    if (char === '"') {
      quoted = true;
    } else if (char === ",") {
      row.push(field);
      field = "";
    } else if (char === "\n") {
      row.push(field);
      rows.push(row);
      row = [];
      field = "";
    } else if (char !== "\r") {
      field += char;
    }
  }
  if (field.length > 0 || row.length > 0) {
    row.push(field);
    rows.push(row);
  }
  return rows.filter((entry) => entry.length > 1 || entry[0] !== "");
}

function sqlLiteral(value) {
  if (value === "\\N" || value === "") return "NULL";
  return "'" + value.replace(/\\/g, "\\\\").replace(/'/g, "\\'") + "'";
}

function insertStatements(table, rows) {
  if (rows.length === 0) return [];
  const columns = [...table.columns, ...Object.keys(table.time)];
  const statements = [];
  const batchSize = 100;
  for (let start = 0; start < rows.length; start += batchSize) {
    const batch = rows.slice(start, start + batchSize);
    const values = batch
      .map((row) => {
        const mapped = row.map((value, index) => {
          const column = columns[index];
          if (column === "archived") return value === "t" || value === "true" ? "1" : "0";
          if (column === "size") return value === "\\N" ? "NULL" : value;
          if (table.time[column]) {
            // 空时间与 NULL 时间按 NULL 处理。
            return value === "" || value === "\\N" ? "NULL" : `'${value}'`;
          }
          return sqlLiteral(value);
        });
        return `(${mapped.join(",")})`;
      })
      .join(",\n");
    statements.push(
      `INSERT INTO ${table.name} (${columns.join(",")}) VALUES\n${values}\n` +
        `ON DUPLICATE KEY UPDATE ${columns.filter((column) => !["id", "token_hash", "class_id", "user_id"].includes(column)).map((column) => `${column}=VALUES(${column})`).join(", ") || "id=id"};`,
    );
  }
  return statements;
}

async function mysqlQuery(sql) {
  // 用 MYSQL_PWD 传密码，避免客户端把 "Using a password" 告警混进查询结果。
  return docker(["compose", "exec", "-T", "-e", `MYSQL_PWD=${config.DB_PASSWORD}`, "db", "mysql", "-h", "127.0.0.1", "-u", "teaching", "teaching", "-N", "-B"], sql);
}

async function fileManifest() {
  const listing = await docker(["run", "--rm", "-v", "zhixu-teaching_uploads:/data:ro", "alpine:3.23", "sh", "-c", "find /data -type f -printf '%P\\n' | sort"]);
  const entries = listing.split("\n").map((line) => line.trim()).filter(Boolean);
  const manifest = {};
  for (const entry of entries) {
    const hash = await docker(["run", "--rm", "-v", "zhixu-teaching_uploads:/data:ro", "alpine:3.23", "sh", "-c", `sha256sum "/data/${entry}" | cut -d' ' -f1`]);
    manifest[entry] = hash.trim();
  }
  return manifest;
}

async function main() {
  await mkdir(workDir, { recursive: true });
  console.log(`源数据卷：${sourceVolume}；演练副本：${drillVolume}${apply ? "；模式：写入 MySQL" : "；模式：演练（不写 MySQL）"}`);

  const before = await fileManifest();
  console.log(`上传卷文件数：${Object.keys(before).length}`);

  // 1. 复制数据卷（原卷只读）。
  await docker(["volume", "rm", "-f", drillVolume]).catch(() => {});
  await docker(["volume", "create", drillVolume]);
  await docker(["run", "--rm", "-v", `${sourceVolume}:/from:ro`, "-v", `${drillVolume}:/to`, "alpine:3.23", "sh", "-c", "cp -a /from/. /to/"]);
  console.log("数据卷副本已创建");

  // 2. 启动临时 PostgreSQL（只读用途，端口仅绑定本机）。
  await docker(["rm", "-f", drillContainer]).catch(() => {});
  await docker([
    "run", "-d", "--name", drillContainer,
    "-e", `POSTGRES_PASSWORD=${config.DB_PASSWORD}`,
    "-e", "POSTGRES_USER=teaching",
    "-e", "POSTGRES_DB=teaching",
    "-v", `${drillVolume}:/var/lib/postgresql`,
    "-p", `127.0.0.1:${pgPort}:5432`,
    pgImage,
  ]);
  let ready = false;
  for (let i = 0; i < 40; i++) {
    try {
      await docker(["exec", drillContainer, "pg_isready", "-U", "teaching", "-d", "teaching"]);
      ready = true;
      break;
    } catch {
      await new Promise((r) => setTimeout(r, 1500));
    }
  }
  if (!ready) throw new Error("临时 PostgreSQL 未就绪");

  // 3. 导出全部表。
  const exported = {};
  for (const table of tables) {
    const csv = await docker(["exec", drillContainer, "psql", "-U", "teaching", "-d", "teaching", "-A", "-t", "-c", `COPY (${exportExpression(table)}) TO STDOUT WITH (FORMAT csv)`]);
    exported[table.name] = parseCsv(csv);
    await writeFile(join(workDir, `${table.name}.csv`), csv, "utf8");
  }

  // 4. 写入 MySQL。同名字段（例如 admin 账号）在目标库里可能已有不同的主键，
  //    因此先写 users，再按 username 建立 ID 映射，用它改写其余表的外键。
  const userIds = new Map();
  if (apply) {
    const userTable = tables.find((table) => table.name === "users");
    const statements = insertStatements(userTable, exported.users);
    if (statements.length > 0) {
      await mysqlQuery(`SET NAMES utf8mb4;\n${statements.join("\n")}`);
    }
    const listed = await mysqlQuery(`SELECT username,id FROM users;`);
    const byUsername = new Map(
      listed
        .split("\n")
        .map((line) => line.trim())
        .filter(Boolean)
        .map((line) => {
          const [username, id] = line.split("\t");
          return [username, id];
        }),
    );
    const remapped = [];
    for (const row of exported.users) {
      const target = byUsername.get(row[1]);
      if (target && target !== row[0]) {
        userIds.set(row[0], target);
        remapped.push(`${row[1]}: ${row[0]} → ${target}`);
      }
    }
    if (remapped.length > 0) {
      console.log("同名账号主键映射：\n  " + remapped.join("\n  "));
    }
  }

  function mapForeignKeys(table, row) {
    const copy = [...row];
    const remap = (index) => {
      const mapped = userIds.get(copy[index]);
      if (mapped) copy[index] = mapped;
    };
    if (table.name === "classrooms") remap(3);
    if (table.name === "memberships" || table.name === "sessions") remap(1);
    return copy;
  }

  const summary = [];
  for (const table of tables) {
    const rows = exported[table.name];
    if (apply && table.name !== "users") {
      const mapped = rows.map((row) => mapForeignKeys(table, row)).filter((row) => row.every((value) => value !== undefined));
      const statements = insertStatements(table, mapped);
      if (statements.length > 0) {
        await mysqlQuery(`SET NAMES utf8mb4;\n${statements.join("\n")}`);
      }
    }
    summary.push({ table: table.name, source: rows.length });
  }

  // 5. 核对行数与文件哈希。
  console.log("\n表\tPostgreSQL\tMySQL");
  for (const entry of summary) {
    let target = "-";
    if (apply) {
      const output = await mysqlQuery(`SELECT COUNT(*) FROM ${entry.table};`);
      target = output.trim();
    }
    console.log(`${entry.table}\t${entry.source}\t${target}`);
    if (apply && target !== "-" && Number(target) !== entry.source) {
      throw new Error(`${entry.table} 行数不一致：源 ${entry.source}，目标 ${target}`);
    }
  }

  const after = await fileManifest();
  const changed = Object.keys(before).filter((name) => after[name] !== before[name]);
  if (changed.length > 0 || Object.keys(after).length !== Object.keys(before).length) {
    throw new Error(`上传文件发生变化：${changed.join(", ") || "文件数量不一致"}`);
  }
  console.log(`上传文件哈希一致：${Object.keys(after).length} 份，未发生改动`);

  if (!keepDrill) {
    await docker(["rm", "-f", drillContainer]).catch(() => {});
    await docker(["volume", "rm", "-f", drillVolume]).catch(() => {});
    console.log("临时容器与演练副本已清理（原数据卷未改动）");
  } else {
    console.log(`保留演练环境：容器 ${drillContainer}，副本 ${drillVolume}`);
  }
  console.log(apply ? "迁移完成：基础业务表已写入 MySQL，讲义索引需在应用内重新建立。" : "演练完成：未写入 MySQL。加 --apply 执行实际迁移。");
}

try {
  await main();
} catch (error) {
  console.error("迁移失败：" + error.message);
  await docker(["rm", "-f", drillContainer]).catch(() => {});
  process.exit(1);
}
