-- 第 6 课：工具服务登记、工具清单与调用记录。
-- 幂等性由 schema_migrations 账本保证（每个文件只执行一次）。

CREATE TABLE IF NOT EXISTS tool_servers (
  id VARCHAR(32) NOT NULL,
  owner_id VARCHAR(32) NOT NULL,
  name VARCHAR(64) NOT NULL,
  base_url VARCHAR(500) NOT NULL,
  -- 服务器级令牌以 AES-256-GCM 密文保存；令牌永不回传接口。
  token_cipher VARCHAR(1024) NULL,
  connected TINYINT(1) NOT NULL DEFAULT 0,
  connected_at DATETIME(6) NULL,
  last_error VARCHAR(500) NOT NULL DEFAULT '',
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  PRIMARY KEY (id),
  UNIQUE KEY tool_servers_owner_name (owner_id, name),
  KEY tool_servers_owner (owner_id),
  CONSTRAINT tool_servers_owner_fk FOREIGN KEY (owner_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 连接成功时在同一事务里整体替换，因此连续连接两次不会让清单翻倍。
CREATE TABLE IF NOT EXISTS tool_server_tools (
  server_id VARCHAR(32) NOT NULL,
  name VARCHAR(64) NOT NULL,
  description VARCHAR(2000) NOT NULL DEFAULT '',
  input_schema JSON NULL,
  discovered_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  PRIMARY KEY (server_id, name),
  CONSTRAINT tool_server_tools_fk FOREIGN KEY (server_id) REFERENCES tool_servers(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 调用记录只增不改：没有更新与删除入口。服务器删除后记录保留，server_id 可空。
CREATE TABLE IF NOT EXISTS tool_calls (
  id VARCHAR(32) NOT NULL,
  owner_id VARCHAR(32) NOT NULL,
  class_id VARCHAR(32) NULL,
  session_id VARCHAR(32) NULL,
  turn INT NOT NULL DEFAULT 1,
  trigger_user_id VARCHAR(32) NULL,
  server_id VARCHAR(32) NULL,
  server_name VARCHAR(64) NOT NULL DEFAULT '',
  tool_name VARCHAR(64) NOT NULL,
  outcome ENUM('success','rejected','error') NOT NULL,
  detail VARCHAR(500) NOT NULL DEFAULT '',
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  PRIMARY KEY (id),
  KEY tool_calls_owner (owner_id, created_at),
  KEY tool_calls_session (session_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 技能的「工具」项：内置知识库检索与 `工具名@服务器名` 引用。
ALTER TABLE prompts ADD COLUMN tools JSON NULL;
