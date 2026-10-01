-- 知识库表：切片正文与索引元数据保存在 MySQL，向量保存在 Qdrant。
-- 注意：MySQL 的 DDL 会隐式提交，因此本文件的语句全部写成可重复执行的形式；
-- 迁移器同时把「重复列/重复键/重复外键」视为已应用（见 main.go 的 isAlreadyApplied）。

CREATE TABLE IF NOT EXISTS knowledge_profiles (
  id varchar(100) NOT NULL,
  model varchar(190) NOT NULL,
  dimensions int NOT NULL,
  preprocessing_version varchar(64) NOT NULL,
  similarity_threshold double NOT NULL,
  active tinyint(1) NOT NULL DEFAULT 0,
  created_at datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  -- MySQL 没有部分索引：用生成列把「同时只能有一个活动配置」表达成唯一键。
  active_flag tinyint GENERATED ALWAYS AS (IF(active = 1, 1, NULL)) STORED,
  PRIMARY KEY (id),
  UNIQUE KEY knowledge_one_active_profile (active_flag),
  CONSTRAINT knowledge_profiles_dimensions_check CHECK (dimensions > 0),
  CONSTRAINT knowledge_profiles_threshold_check CHECK (similarity_threshold BETWEEN -1 AND 1)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS knowledge_documents (
  handout_id varchar(64) NOT NULL,
  class_id varchar(64) NOT NULL,
  content_hash varchar(64) NOT NULL,
  status varchar(16) NOT NULL,
  error_code varchar(64) NOT NULL DEFAULT '',
  active_generation_id varchar(64) NULL,
  updated_at datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  PRIMARY KEY (handout_id),
  -- MySQL 8.4 要求外键引用的列必须构成唯一键，因此这里用唯一索引而不是普通索引；
  -- knowledge_generations / knowledge_chunks / knowledge_jobs 的组合外键都引用它。
  UNIQUE KEY knowledge_documents_scope (handout_id, class_id),
  KEY knowledge_documents_class_status (class_id, status),
  CONSTRAINT knowledge_documents_handout_fk FOREIGN KEY (handout_id, class_id) REFERENCES handouts(id, class_id) ON DELETE CASCADE,
  CONSTRAINT knowledge_documents_status_check CHECK (status IN ('queued','extracting','embedding','ready','failed','unsupported','paused'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS knowledge_generations (
  id varchar(64) NOT NULL,
  handout_id varchar(64) NOT NULL,
  class_id varchar(64) NOT NULL,
  profile_id varchar(100) NOT NULL,
  content_hash varchar(64) NOT NULL,
  parser_version varchar(64) NOT NULL,
  -- 切片策略随代次记录，保证同内容同策略重建时切片边界可复现。
  chunker_version varchar(64) NOT NULL DEFAULT 'chunks-v1',
  chunk_strategy varchar(32) NOT NULL DEFAULT 'window',
  chunk_size int NOT NULL DEFAULT 800,
  chunk_overlap int NOT NULL DEFAULT 80,
  status varchar(16) NOT NULL,
  created_at datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  PRIMARY KEY (id),
  KEY knowledge_generations_lookup (handout_id, profile_id, content_hash),
  CONSTRAINT knowledge_generations_document_fk FOREIGN KEY (handout_id, class_id) REFERENCES knowledge_documents(handout_id, class_id) ON DELETE CASCADE,
  CONSTRAINT knowledge_generations_profile_fk FOREIGN KEY (profile_id) REFERENCES knowledge_profiles(id),
  CONSTRAINT knowledge_generations_status_check CHECK (status IN ('building','ready','failed','paused'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS knowledge_chunks (
  -- 切片标识同时是 Qdrant 点标识，必须是合法 UUID 文本。
  id char(36) NOT NULL,
  generation_id varchar(64) NOT NULL,
  class_id varchar(64) NOT NULL,
  handout_id varchar(64) NOT NULL,
  ordinal int NOT NULL,
  char_start int NOT NULL,
  char_end int NOT NULL,
  content mediumtext NOT NULL,
  locator json NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY knowledge_chunks_ordinal (generation_id, ordinal),
  KEY knowledge_chunks_scope (class_id, handout_id, generation_id),
  CONSTRAINT knowledge_chunks_generation_fk FOREIGN KEY (generation_id) REFERENCES knowledge_generations(id) ON DELETE CASCADE,
  CONSTRAINT knowledge_chunks_document_fk FOREIGN KEY (handout_id, class_id) REFERENCES knowledge_documents(handout_id, class_id) ON DELETE CASCADE,
  CONSTRAINT knowledge_chunks_ordinal_check CHECK (ordinal >= 0),
  -- 中文关键字检索依赖二字切分；ngram_token_size 必须在服务器启动参数中固定为 2。
  FULLTEXT KEY knowledge_chunks_content_ft (content) WITH PARSER ngram
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS knowledge_jobs (
  id varchar(64) NOT NULL,
  handout_id varchar(64) NOT NULL,
  class_id varchar(64) NOT NULL,
  generation_id varchar(64) NOT NULL,
  status varchar(16) NOT NULL,
  attempts int NOT NULL DEFAULT 0,
  lease_until datetime(6) NULL,
  lease_token varchar(64) NULL,
  next_attempt_at datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  error_code varchar(64) NOT NULL DEFAULT '',
  created_at datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  -- MySQL 没有部分唯一索引：用生成列把「同一讲义同时只有一个未完成任务」表达成唯一键。
  -- 必须是 VIRTUAL：MySQL 8.4 不允许在「存储生成列的基列」上使用 ON DELETE CASCADE 外键。
  live_handout_id varchar(64) GENERATED ALWAYS AS (IF(status IN ('queued','running','paused'), handout_id, NULL)) VIRTUAL,
  PRIMARY KEY (id),
  UNIQUE KEY knowledge_one_live_job (live_handout_id),
  KEY knowledge_jobs_claim (status, next_attempt_at, lease_until),
  CONSTRAINT knowledge_jobs_document_fk FOREIGN KEY (handout_id, class_id) REFERENCES knowledge_documents(handout_id, class_id) ON DELETE CASCADE,
  CONSTRAINT knowledge_jobs_generation_fk FOREIGN KEY (generation_id) REFERENCES knowledge_generations(id) ON DELETE CASCADE,
  CONSTRAINT knowledge_jobs_status_check CHECK (status IN ('queued','running','paused','failed','done'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 活动代次外键必须在两张表都建好之后添加；迁移器把重复外键视为已应用。
ALTER TABLE knowledge_documents
  ADD CONSTRAINT knowledge_active_generation_fk FOREIGN KEY (active_generation_id) REFERENCES knowledge_generations(id) ON DELETE SET NULL;
