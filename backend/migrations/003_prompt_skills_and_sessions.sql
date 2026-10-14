-- 提示词／技能、会话与消息。语句写成可重复执行的形式（迁移器把重复列/键/表视为已应用）。

CREATE TABLE IF NOT EXISTS prompts (
  id varchar(64) NOT NULL,
  scope varchar(16) NOT NULL,
  class_id varchar(64) NULL,
  kind varchar(16) NOT NULL,
  name varchar(190) NOT NULL,
  body text NOT NULL,
  enabled tinyint(1) NOT NULL DEFAULT 1,
  created_by varchar(64) NULL,
  created_at datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  -- MySQL 唯一键不约束 NULL：平台级行用空串占位，才能与班级级同名行共存并各自去重。
  -- 必须是 VIRTUAL：MySQL 8.4 不允许在「存储生成列的基列」上使用 ON DELETE CASCADE 外键。
  scope_key varchar(64) GENERATED ALWAYS AS (IFNULL(class_id, '')) VIRTUAL,
  PRIMARY KEY (id),
  UNIQUE KEY prompts_scope_name (scope_key, kind, name),
  KEY prompts_class_lookup (class_id, kind, enabled),
  CONSTRAINT prompts_scope_check CHECK (scope IN ('platform','class')),
  CONSTRAINT prompts_kind_check CHECK (kind IN ('prompt','skill')),
  CONSTRAINT prompts_scope_class_check CHECK ((scope = 'platform' AND class_id IS NULL) OR (scope = 'class' AND class_id IS NOT NULL)),
  CONSTRAINT prompts_class_fk FOREIGN KEY (class_id) REFERENCES classrooms(id) ON DELETE CASCADE,
  CONSTRAINT prompts_created_by_fk FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS chat_sessions (
  id varchar(64) NOT NULL,
  class_id varchar(64) NOT NULL,
  user_id varchar(64) NOT NULL,
  title varchar(120) NOT NULL DEFAULT '',
  created_at datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  last_message_at datetime(6) NULL,
  PRIMARY KEY (id),
  KEY chat_sessions_class (class_id, last_message_at),
  KEY chat_sessions_user (user_id),
  CONSTRAINT chat_sessions_class_fk FOREIGN KEY (class_id) REFERENCES classrooms(id) ON DELETE CASCADE,
  CONSTRAINT chat_sessions_user_fk FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS chat_messages (
  id varchar(64) NOT NULL,
  session_id varchar(64) NOT NULL,
  -- 班级冗余存一份：跨班隔离与按班统计都只查这一列，避免每次回表会话表。
  class_id varchar(64) NOT NULL,
  seq int NOT NULL,
  role varchar(16) NOT NULL,
  content mediumtext NOT NULL,
  status varchar(16) NOT NULL DEFAULT 'complete',
  citations json NULL,
  model varchar(190) NOT NULL DEFAULT '',
  prompt_chars int NOT NULL DEFAULT 0,
  latency_ms int NOT NULL DEFAULT 0,
  created_at datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  PRIMARY KEY (id),
  UNIQUE KEY chat_messages_seq (session_id, seq),
  KEY chat_messages_class (class_id),
  CONSTRAINT chat_messages_session_fk FOREIGN KEY (session_id) REFERENCES chat_sessions(id) ON DELETE CASCADE,
  CONSTRAINT chat_messages_class_fk FOREIGN KEY (class_id) REFERENCES classrooms(id) ON DELETE CASCADE,
  CONSTRAINT chat_messages_role_check CHECK (role IN ('user','assistant')),
  CONSTRAINT chat_messages_status_check CHECK (status IN ('complete','interrupted','failed')),
  CONSTRAINT chat_messages_seq_check CHECK (seq >= 1)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
