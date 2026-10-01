-- 记录请求的切片策略与实际生效策略、以及策略回退原因，供索引状态接口说明。
ALTER TABLE knowledge_generations ADD COLUMN requested_strategy varchar(32) NOT NULL DEFAULT 'window' AFTER chunk_strategy;
ALTER TABLE knowledge_generations ADD COLUMN chunk_note varchar(160) NOT NULL DEFAULT '' AFTER chunk_overlap;
