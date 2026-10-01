-- 基础业务表：每次启动以幂等方式执行，可安全重复运行。
-- 字符集统一 utf8mb4；时间统一 UTC，由连接参数 loc=UTC 与服务器 default-time-zone=+00:00 保证。

CREATE TABLE IF NOT EXISTS users (
  id varchar(64) NOT NULL,
  username varchar(32) NOT NULL,
  name varchar(255) NOT NULL,
  password_hash varchar(255) NOT NULL,
  role varchar(16) NOT NULL,
  created_at datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  PRIMARY KEY (id),
  UNIQUE KEY users_username (username),
  CONSTRAINT users_role_check CHECK (role IN ('admin','teacher','student'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS sessions (
  token_hash varchar(64) NOT NULL,
  user_id varchar(64) NOT NULL,
  expires_at datetime(6) NOT NULL,
  PRIMARY KEY (token_hash),
  KEY sessions_expiry (expires_at),
  KEY sessions_user (user_id),
  CONSTRAINT sessions_user_fk FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS classrooms (
  id varchar(64) NOT NULL,
  name varchar(255) NOT NULL,
  description varchar(1000) NOT NULL DEFAULT '',
  owner_id varchar(64) NOT NULL,
  invite_code varchar(32) NOT NULL,
  archived tinyint(1) NOT NULL DEFAULT 0,
  created_at datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  PRIMARY KEY (id),
  UNIQUE KEY classrooms_invite_code (invite_code),
  KEY classrooms_owner (owner_id),
  CONSTRAINT classrooms_owner_fk FOREIGN KEY (owner_id) REFERENCES users(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS memberships (
  class_id varchar(64) NOT NULL,
  user_id varchar(64) NOT NULL,
  joined_at datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  PRIMARY KEY (class_id, user_id),
  KEY memberships_user (user_id),
  CONSTRAINT memberships_class_fk FOREIGN KEY (class_id) REFERENCES classrooms(id) ON DELETE CASCADE,
  CONSTRAINT memberships_user_fk FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS handouts (
  id varchar(64) NOT NULL,
  class_id varchar(64) NOT NULL,
  title varchar(255) NOT NULL,
  filename varchar(255) NOT NULL,
  stored_name varchar(255) NOT NULL,
  size bigint NOT NULL,
  mime varchar(128) NOT NULL,
  uploaded_at datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  PRIMARY KEY (id),
  UNIQUE KEY handouts_stored_name (stored_name),
  -- 组合唯一键供知识库表按 (handout_id,class_id) 建立外键，防止跨班级挂载。
  UNIQUE KEY handouts_id_class (id, class_id),
  KEY handouts_class (class_id),
  CONSTRAINT handouts_class_fk FOREIGN KEY (class_id) REFERENCES classrooms(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
