package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 这些测试需要一个真实的 MySQL 与 Qdrant（方言、行锁、生成列与跨存储一致性无法用假实现覆盖）。
// 通过环境变量启用：
//
//	MYSQL_TEST_DSN         例如 root:pass@tcp(127.0.0.1:13306)/teaching?parseTime=true&loc=UTC&charset=utf8mb4
//	QDRANT_TEST_URL        例如 http://127.0.0.1:16333
//	MYSQL_TEST_DSN_NGRAM3  以 --ngram_token_size=3 启动的实例，用于验证启动守卫
func testApp(t *testing.T) (*App, context.Context) {
	t.Helper()
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("MYSQL_TEST_DSN 未设置，跳过需要真实 MySQL 的测试")
	}
	qdrant := os.Getenv("QDRANT_TEST_URL")
	if qdrant == "" {
		t.Skip("QDRANT_TEST_URL 未设置，跳过需要真实 Qdrant 的测试")
	}
	ctx := context.Background()
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("QDRANT_URL", qdrant)
	t.Setenv("QDRANT_COLLECTION", "knowledge_test_chunks")
	t.Setenv("EMBEDDING_DIMENSIONS", "4")
	t.Setenv("EMBEDDING_PROFILE_ID", "test-profile")
	t.Setenv("EMBEDDING_MODEL", "test-model")
	t.Setenv("GATEWAY_BASE_URL", "http://127.0.0.1:1")
	db, err := openDatabase(ctx)
	if err != nil {
		t.Fatalf("连接测试数据库失败: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err = db.ExecContext(ctx, `SET FOREIGN_KEY_CHECKS=0`); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"chat_messages", "chat_sessions", "tool_calls", "tool_server_tools", "tool_servers", "prompts",
		"knowledge_jobs", "knowledge_chunks", "knowledge_generations", "knowledge_documents", "knowledge_profiles",
		"schema_migrations", "handouts", "memberships", "classrooms", "sessions", "users"} {
		if _, err = db.ExecContext(ctx, "DROP TABLE IF EXISTS "+table); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.ExecContext(ctx, `SET FOREIGN_KEY_CHECKS=1`); err != nil {
		t.Fatal(err)
	}
	for _, statement := range splitStatements(schema) {
		if _, err = db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("建表失败: %v", err)
		}
	}
	if err = checkServerVariables(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err = applyMigrations(ctx, db); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	a := &App{db: db, uploads: t.TempDir(), origin: "http://localhost:8080", knowledge: true, attempts: map[string]attempt{}}
	// 与启动路径一致：测试环境同样写入平台默认提示词与内置技能。
	if err = a.ensurePlatformPrompts(ctx); err != nil {
		t.Fatalf("写入平台提示词失败: %v", err)
	}
	a.gateway = newGatewayClient(4)
	a.profileID = "test-profile"
	a.vectors = newVectorStore(4)
	if err = a.vectors.ensureCollection(ctx); err != nil {
		t.Fatalf("创建测试集合失败: %v", err)
	}
	if err = a.ensureKnowledgeProfile(ctx); err != nil {
		t.Fatalf("注册嵌入配置失败: %v", err)
	}
	return a, ctx
}

type fixture struct {
	userID, classID, handoutID string
	hash                       string
}

func seedFixture(t *testing.T, a *App, ctx context.Context) fixture {
	t.Helper()
	f := fixture{userID: randomID(16), classID: randomID(16), handoutID: randomID(16)}
	stored := "stored-" + f.handoutID
	path := filepath.Join(a.uploads, stored)
	if err := os.WriteFile(path, []byte("讲义正文内容"), 0600); err != nil {
		t.Fatal(err)
	}
	hash, err := fileHash(path)
	if err != nil {
		t.Fatal(err)
	}
	f.hash = hash
	if _, err = a.db.ExecContext(ctx, `INSERT INTO users(id,username,name,password_hash,role) VALUES(?,?,?,'x','teacher')`, f.userID, "t"+f.userID[:8], "测试教师"); err != nil {
		t.Fatal(err)
	}
	if _, err = a.db.ExecContext(ctx, `INSERT INTO classrooms(id,name,description,owner_id,invite_code) VALUES(?,?,?,?,?)`, f.classID, "测试班级", "", f.userID, strings.ToUpper(f.classID[:8])); err != nil {
		t.Fatal(err)
	}
	if _, err = a.db.ExecContext(ctx, `INSERT INTO handouts(id,class_id,title,filename,stored_name,size,mime) VALUES(?,?,?,?,?,?,?)`,
		f.handoutID, f.classID, "讲义", "lesson.txt", stored, 10, "text/plain"); err != nil {
		t.Fatal(err)
	}
	return f
}

func queueFixtureJob(t *testing.T, a *App, ctx context.Context, f fixture) {
	t.Helper()
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = queueKnowledge(ctx, tx, f.handoutID, f.classID, f.hash, a.profileID, defaultChunkingPolicy()); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func documentStatus(t *testing.T, a *App, ctx context.Context, handoutID string) (string, string) {
	t.Helper()
	var status, errorCode string
	if err := a.db.QueryRowContext(ctx, `SELECT status,error_code FROM knowledge_documents WHERE handout_id=?`, handoutID).Scan(&status, &errorCode); err != nil {
		t.Fatal(err)
	}
	return status, errorCode
}

// 任务 1.3：任务领取使用行锁与 SKIP LOCKED，租约过期后可以重新领取。
func TestClaimAndLeaseExpiry(t *testing.T) {
	a, ctx := testApp(t)
	f := seedFixture(t, a, ctx)
	queueFixtureJob(t, a, ctx, f)

	job, err := a.claimKnowledgeJob(ctx)
	if err != nil || job == nil {
		t.Fatalf("首次领取失败: %v %#v", err, job)
	}
	if job.HandoutID != f.handoutID || job.Token == "" || job.Attempts != 1 {
		t.Fatalf("领取到的任务不正确: %#v", job)
	}
	if status, _ := documentStatus(t, a, ctx, f.handoutID); status != "extracting" {
		t.Fatalf("领取后文档状态应为 extracting，实际 %s", status)
	}
	second, err := a.claimKnowledgeJob(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if second != nil {
		t.Fatalf("运行中的任务不应被重复领取: %#v", second)
	}
	if _, err = a.db.ExecContext(ctx, `UPDATE knowledge_jobs SET lease_until=NOW(6)-INTERVAL 1 MINUTE WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := a.claimKnowledgeJob(ctx)
	if err != nil || reclaimed == nil {
		t.Fatalf("租约过期后应能重新领取: %v %#v", err, reclaimed)
	}
	if reclaimed.ID != job.ID || reclaimed.Attempts != 2 {
		t.Fatalf("重新领取的任务不正确: %#v", reclaimed)
	}
}

// 任务 1.3：领取时跳过已归档班级的任务。
func TestClaimSkipsArchivedClass(t *testing.T) {
	a, ctx := testApp(t)
	f := seedFixture(t, a, ctx)
	queueFixtureJob(t, a, ctx, f)
	if _, err := a.db.ExecContext(ctx, `UPDATE classrooms SET archived=1 WHERE id=?`, f.classID); err != nil {
		t.Fatal(err)
	}
	job, err := a.claimKnowledgeJob(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if job != nil {
		t.Fatalf("已归档班级的任务不应被领取: %#v", job)
	}
}

// 任务 1.3：临时失败退避重试，永久错误立即终止并给出安全错误码。
func TestFailKnowledgeJobRetryAndTerminal(t *testing.T) {
	a, ctx := testApp(t)
	f := seedFixture(t, a, ctx)
	queueFixtureJob(t, a, ctx, f)
	job, err := a.claimKnowledgeJob(ctx)
	if err != nil || job == nil {
		t.Fatalf("领取失败: %v", err)
	}
	a.failKnowledgeJob(ctx, *job, errTemporary)
	var status, code string
	var next time.Time
	if err = a.db.QueryRowContext(ctx, `SELECT status,error_code,next_attempt_at FROM knowledge_jobs WHERE id=?`, job.ID).Scan(&status, &code, &next); err != nil {
		t.Fatal(err)
	}
	if status != "queued" || code != "temporary_failure" || !next.After(time.Now().UTC()) {
		t.Fatalf("临时失败应退避重试: status=%s code=%s next=%s", status, code, next)
	}
	if docStatus, _ := documentStatus(t, a, ctx, f.handoutID); docStatus != "queued" {
		t.Fatalf("退避后文档状态应为 queued，实际 %s", docStatus)
	}

	// 永久错误：直接终止并标记为不可索引。
	if _, err = a.db.ExecContext(ctx, `UPDATE knowledge_jobs SET next_attempt_at=NOW(6) WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	job2, err := a.claimKnowledgeJob(ctx)
	if err != nil || job2 == nil {
		t.Fatalf("重新领取失败: %v", err)
	}
	a.failKnowledgeJob(ctx, *job2, &permanentError{"unsupported", errTemporary})
	if err = a.db.QueryRowContext(ctx, `SELECT status,error_code FROM knowledge_jobs WHERE id=?`, job.ID).Scan(&status, &code); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || code != "unsupported" {
		t.Fatalf("永久错误应终止任务: status=%s code=%s", status, code)
	}
	if docStatus, docCode := documentStatus(t, a, ctx, f.handoutID); docStatus != "unsupported" || docCode != "unsupported" {
		t.Fatalf("文档状态应为 unsupported: %s %s", docStatus, docCode)
	}
	var generationStatus string
	if err = a.db.QueryRowContext(ctx, `SELECT status FROM knowledge_generations WHERE id=?`, job.GenerationID).Scan(&generationStatus); err != nil {
		t.Fatal(err)
	}
	if generationStatus != "failed" {
		t.Fatalf("失败代次不应保持 building: %s", generationStatus)
	}
}

func TestFailKnowledgeJobStopsAfterAttemptLimit(t *testing.T) {
	a, ctx := testApp(t)
	f := seedFixture(t, a, ctx)
	queueFixtureJob(t, a, ctx, f)
	var job *workerJob
	for i := 0; i < 3; i++ {
		if i > 0 {
			if _, err := a.db.ExecContext(ctx, `UPDATE knowledge_jobs SET next_attempt_at=NOW(6) WHERE handout_id=?`, f.handoutID); err != nil {
				t.Fatal(err)
			}
		}
		claimed, err := a.claimKnowledgeJob(ctx)
		if err != nil || claimed == nil {
			t.Fatalf("第 %d 次领取失败: %v", i+1, err)
		}
		job = claimed
		if i < 2 {
			a.failKnowledgeJob(ctx, *claimed, errTemporary)
		}
	}
	if job.Attempts != 3 {
		t.Fatalf("尝试次数应为 3，实际 %d", job.Attempts)
	}
	a.failKnowledgeJob(ctx, *job, errTemporary)
	status, _ := documentStatus(t, a, ctx, f.handoutID)
	if status != "failed" {
		t.Fatalf("达到尝试上限后应失败，实际 %s", status)
	}
}

// 任务 1.3 与 2.3：发布代次后切片落库、向量可检索、覆盖统计正确，残留向量不可见。
func TestPublishKnowledgeAndCoverage(t *testing.T) {
	a, ctx := testApp(t)
	f := seedFixture(t, a, ctx)
	queueFixtureJob(t, a, ctx, f)
	job, err := a.claimKnowledgeJob(ctx)
	if err != nil || job == nil {
		t.Fatalf("领取失败: %v", err)
	}
	chunks := []sourceChunk{
		{Text: "第一段内容", Locator: locator{Type: "line", Start: 1, End: 1, Label: "第 1 行"}, Ordinal: 1, CharStart: 0, CharEnd: 5},
		{Text: "第二段内容", Locator: locator{Type: "line", Start: 2, End: 2, Label: "第 2 行"}, Ordinal: 2, CharStart: 5, CharEnd: 10},
	}
	vectors := [][]float32{{1, 0, 0, 0}, {0, 1, 0, 0}}
	if err = a.publishKnowledge(ctx, *job, chunks, vectors); err != nil {
		t.Fatalf("发布失败: %v", err)
	}
	var count int
	if err = a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_chunks WHERE handout_id=?`, f.handoutID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("应写入 2 条切片，实际 %d", count)
	}
	var ordinal, charStart, charEnd int
	var chunkID string
	if err = a.db.QueryRowContext(ctx, `SELECT id,ordinal,char_start,char_end FROM knowledge_chunks WHERE handout_id=? ORDER BY ordinal LIMIT 1`, f.handoutID).Scan(&chunkID, &ordinal, &charStart, &charEnd); err != nil {
		t.Fatal(err)
	}
	if ordinal != 1 || charStart != 0 || charEnd != 5 {
		t.Fatalf("切片序号或字符区间不正确: %d %d %d", ordinal, charStart, charEnd)
	}
	var activeID string
	var status string
	if err = a.db.QueryRowContext(ctx, `SELECT active_generation_id,status FROM knowledge_documents WHERE handout_id=?`, f.handoutID).Scan(&activeID, &status); err != nil {
		t.Fatal(err)
	}
	if activeID != job.GenerationID || status != "ready" {
		t.Fatalf("文档未指向新代次: %s %s", activeID, status)
	}
	var jobStatus string
	if err = a.db.QueryRowContext(ctx, `SELECT status FROM knowledge_jobs WHERE id=?`, job.ID).Scan(&jobStatus); err != nil {
		t.Fatal(err)
	}
	if jobStatus != "done" {
		t.Fatalf("任务状态应为 done，实际 %s", jobStatus)
	}

	// 向量写入后应能按班级过滤检索到。
	hits, err := a.vectors.search(ctx, []float32{1, 0, 0, 0}, f.classID, nil, 5, 0.35)
	if err != nil || len(hits) == 0 {
		t.Fatalf("向量检索未命中: %v %#v", err, hits)
	}
	if hits[0].ID != chunkID {
		t.Fatalf("向量主键与切片标识不一致: %s vs %s", hits[0].ID, chunkID)
	}

	// 覆盖统计：一份已就绪、一份未就绪。
	second := fixture{handoutID: randomID(16)}
	if _, err = a.db.ExecContext(ctx, `INSERT INTO handouts(id,class_id,title,filename,stored_name,size,mime) VALUES(?,?,?,?,?,?,?)`,
		second.handoutID, f.classID, "第二份", "b.txt", "stored-"+second.handoutID, 5, "text/plain"); err != nil {
		t.Fatal(err)
	}
	var total, ready int
	if err = a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM handouts WHERE class_id=?`, f.classID).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if err = a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM handouts h JOIN knowledge_documents d ON d.handout_id=h.id
		JOIN knowledge_generations g ON g.id=d.active_generation_id JOIN knowledge_profiles p ON p.id=g.profile_id AND p.active=1
		WHERE h.class_id=?`, f.classID).Scan(&ready); err != nil {
		t.Fatal(err)
	}
	if total != 2 || ready != 1 {
		t.Fatalf("覆盖统计不正确: ready=%d total=%d", ready, total)
	}

	// 残留向量：向量库里有、关系库里没有的点必须被丢弃。
	if err = a.vectors.upsert(ctx, []vectorPoint{{ID: randomUUID(), Vector: []float32{1, 0, 0, 0}, Payload: map[string]any{"class_id": f.classID, "handout_id": f.handoutID, "generation_id": "stale", "ordinal": 9}}}); err != nil {
		t.Fatal(err)
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	stale, err := a.vectors.search(ctx, []float32{1, 0, 0, 0}, f.classID, nil, 10, 0.35)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(stale))
	for _, hit := range stale {
		ids = append(ids, hit.ID)
	}
	rows, err := a.publishedChunks(ctx, tx, f.classID, "test-profile", ids, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("残留向量不应回表成功: %#v", rows)
	}
	if _, ok := rows[chunkID]; !ok {
		t.Fatalf("有效切片应能回表: %#v", rows)
	}
}

// 任务 1.3：发布时班级已归档，任务与文档进入暂停而不是标记完成。
func TestPublishPausesWhenArchived(t *testing.T) {
	a, ctx := testApp(t)
	f := seedFixture(t, a, ctx)
	queueFixtureJob(t, a, ctx, f)
	job, err := a.claimKnowledgeJob(ctx)
	if err != nil || job == nil {
		t.Fatalf("领取失败: %v", err)
	}
	if _, err = a.db.ExecContext(ctx, `UPDATE classrooms SET archived=1 WHERE id=?`, f.classID); err != nil {
		t.Fatal(err)
	}
	if err = a.publishKnowledge(ctx, *job, []sourceChunk{{Text: "内容", Ordinal: 1}}, [][]float32{{1, 0, 0, 0}}); err != nil {
		t.Fatalf("归档时发布应暂停而不是报错: %v", err)
	}
	var jobStatus, docStatus string
	if err = a.db.QueryRowContext(ctx, `SELECT status FROM knowledge_jobs WHERE id=?`, job.ID).Scan(&jobStatus); err != nil {
		t.Fatal(err)
	}
	if err = a.db.QueryRowContext(ctx, `SELECT status FROM knowledge_documents WHERE handout_id=?`, f.handoutID).Scan(&docStatus); err != nil {
		t.Fatal(err)
	}
	if jobStatus != "paused" || docStatus != "paused" {
		t.Fatalf("归档时应暂停: job=%s doc=%s", jobStatus, docStatus)
	}
}

// 任务 1.3：源文件哈希变化后拒绝发布，避免把过期内容标记为可检索。
func TestPublishRejectsChangedSource(t *testing.T) {
	a, ctx := testApp(t)
	f := seedFixture(t, a, ctx)
	queueFixtureJob(t, a, ctx, f)
	job, err := a.claimKnowledgeJob(ctx)
	if err != nil || job == nil {
		t.Fatalf("领取失败: %v", err)
	}
	if _, err = a.db.ExecContext(ctx, `UPDATE handouts SET stored_name=? WHERE id=?`, "missing-file", f.handoutID); err != nil {
		t.Fatal(err)
	}
	err = a.publishKnowledge(ctx, *job, []sourceChunk{{Text: "内容", Ordinal: 1}}, [][]float32{{1, 0, 0, 0}})
	if err == nil {
		t.Fatal("源文件不可读时不应发布")
	}
	status, _ := documentStatus(t, a, ctx, f.handoutID)
	if status == "ready" {
		t.Fatal("发布失败后文档不应标记为 ready")
	}
}

// 任务 2.1：集合缺失时创建、已存在时幂等、维度不一致时报错且不覆盖。
func TestVectorCollectionBranches(t *testing.T) {
	a, ctx := testApp(t)
	if err := a.vectors.ensureCollection(ctx); err != nil {
		t.Fatalf("集合已存在时应幂等返回: %v", err)
	}
	mismatched := newVectorStore(8)
	err := mismatched.ensureCollection(ctx)
	if err == nil {
		t.Fatal("维度不一致时应报错")
	}
	if !strings.Contains(err.Error(), "维度") {
		t.Fatalf("错误信息应说明维度不一致: %v", err)
	}
	// 原有集合必须保持原样。
	info := newVectorStore(4)
	if infoErr := info.ensureCollection(ctx); infoErr != nil {
		t.Fatalf("维度不匹配不应破坏已有集合: %v", infoErr)
	}
}

// 任务 2.2：嵌入失败时任务回到重试队列，且不向向量库写入任何点。
func TestEmbeddingFailureWritesNoVectors(t *testing.T) {
	a, ctx := testApp(t)
	f := seedFixture(t, a, ctx)
	queueFixtureJob(t, a, ctx, f)
	job, err := a.claimKnowledgeJob(ctx)
	if err != nil || job == nil {
		t.Fatalf("领取失败: %v", err)
	}
	a.gateway = &gatewayClient{baseURL: "http://127.0.0.1:1", embedModel: "test-model", dimensions: 4, timeout: 200 * time.Millisecond}
	a.processKnowledgeJob(ctx, *job)
	var status, code string
	if err = a.db.QueryRowContext(ctx, `SELECT status,error_code FROM knowledge_jobs WHERE id=?`, job.ID).Scan(&status, &code); err != nil {
		t.Fatal(err)
	}
	if status != "queued" || code == "" {
		t.Fatalf("嵌入失败应回到队列并记录错误码: status=%s code=%s", status, code)
	}
	if docStatus, _ := documentStatus(t, a, ctx, f.handoutID); docStatus == "ready" {
		t.Fatal("嵌入失败不应把文档标记为可检索")
	}
	hits, err := a.vectors.search(ctx, []float32{1, 0, 0, 0}, f.classID, nil, 10, 0.0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("嵌入失败不应写入向量: %#v", hits)
	}
	var chunks int
	if err = a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_chunks WHERE handout_id=?`, f.handoutID).Scan(&chunks); err != nil {
		t.Fatal(err)
	}
	if chunks != 0 {
		t.Fatalf("嵌入失败不应写入切片: %d", chunks)
	}
}

// 任务 2.3：低于 0.35 的候选必须被丢弃，跨班过滤在向量库与回表两处都生效。
func TestVectorThresholdAndClassFilter(t *testing.T) {
	a, ctx := testApp(t)
	f := seedFixture(t, a, ctx)
	queueFixtureJob(t, a, ctx, f)
	job, err := a.claimKnowledgeJob(ctx)
	if err != nil || job == nil {
		t.Fatalf("领取失败: %v", err)
	}
	// 与查询的余弦相似度约 0.17：高于 0 但低于 0.35 的阈值。
	if err = a.publishKnowledge(ctx, *job, []sourceChunk{{Text: "弱相关内容", Ordinal: 1, CharStart: 0, CharEnd: 5}}, [][]float32{{0.17, 0.985, 0, 0}}); err != nil {
		t.Fatalf("发布失败: %v", err)
	}
	below, err := a.vectors.search(ctx, []float32{1, 0, 0, 0}, f.classID, nil, 5, similarityThreshold)
	if err != nil {
		t.Fatal(err)
	}
	if len(below) != 0 {
		t.Fatalf("低于阈值的候选不应保留: %#v", below)
	}
	// 同一份向量在不设阈值时可以检索到，说明过滤来自阈值而不是数据缺失。
	all, err := a.vectors.search(ctx, []float32{1, 0, 0, 0}, f.classID, nil, 5, 0)
	if err != nil || len(all) != 1 {
		t.Fatalf("不设阈值时应能取到候选: %v %#v", err, all)
	}
	// 其他班级检索不到本班向量。
	other, err := a.vectors.search(ctx, []float32{1, 0, 0, 0}, "other-class", nil, 5, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 0 {
		t.Fatalf("跨班过滤未生效: %#v", other)
	}
}

// 任务 1.4 与变更 2 的 1.1：中文按二字 token 切分后必须能被关键字检索命中，且限定班级。
func TestFulltextChineseKeyword(t *testing.T) {
	a, ctx := testApp(t)
	f := seedFixture(t, a, ctx)
	queueFixtureJob(t, a, ctx, f)
	job, err := a.claimKnowledgeJob(ctx)
	if err != nil || job == nil {
		t.Fatalf("领取失败: %v", err)
	}
	chunks := []sourceChunk{
		{Text: "课程项目数据库采用 MySQL。", Locator: locator{Type: "line", Start: 1, End: 1, Label: "第 1 行"}, Ordinal: 1, CharStart: 0, CharEnd: 13},
		{Text: "本节讨论向量检索与切片窗口。", Locator: locator{Type: "line", Start: 2, End: 2, Label: "第 2 行"}, Ordinal: 2, CharStart: 13, CharEnd: 27},
	}
	if err = a.publishKnowledge(ctx, *job, chunks, [][]float32{{1, 0, 0, 0}, {0, 1, 0, 0}}); err != nil {
		t.Fatalf("发布失败: %v", err)
	}
	for _, keyword := range []string{"数据库", "向量检索", "切片"} {
		hits, searchErr := a.keywordSearch(ctx, a.db, f.classID, a.profileID, keyword, nil, 5)
		if searchErr != nil {
			t.Fatalf("关键字 %s 查询失败: %v", keyword, searchErr)
		}
		if len(hits) == 0 {
			t.Fatalf("中文关键字 %s 未命中任何切片", keyword)
		}
	}
	misses, err := a.keywordSearch(ctx, a.db, f.classID, a.profileID, "操作系统", nil, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(misses) != 0 {
		t.Fatalf("不存在的关键字不应命中: %#v", misses)
	}
	// 跨班级检索同样的关键字应当没有命中。
	otherClass, err := a.keywordSearch(ctx, a.db, randomID(16), a.profileID, "数据库", nil, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(otherClass) != 0 {
		t.Fatalf("关键字检索未限定班级: %#v", otherClass)
	}
}

// 变更 2 的任务 1.3：两路均命中的切片在 RRF 融合后必须排在仅单路命中之前。
func TestHybridFusionRanksDualPathFirst(t *testing.T) {
	dual := []searchHit{{ID: "dual", Score: 5}, {ID: "vector-only", Score: 4}}
	keyword := []searchHit{{ID: "dual", Score: 3}, {ID: "keyword-only", Score: 2.5}}
	fused := fuseRRF(keyword, dual, 10)
	if len(fused) != 3 {
		t.Fatalf("融合结果数量不正确: %#v", fused)
	}
	if fused[0].ID != "dual" {
		t.Fatalf("两路均命中的切片应排在最前: %#v", fused)
	}
	// 仅单路命中的两条名次相同，分数相等；顺序由融合的遍历次序决定（关键字先、向量后）。
	if fused[1].Score != fused[2].Score {
		t.Fatalf("仅单路命中的两条应当同分: %#v", fused)
	}
	if fused[1].ID != "keyword-only" || fused[2].ID != "vector-only" {
		t.Fatalf("同分时的顺序不符合融合次序: %#v", fused)
	}
	// 名次分只用于排序，不作为相关性阈值：数值本身不代表相关度百分比。
	if fused[0].Score <= fused[1].Score {
		t.Fatalf("融合分应递减: %#v", fused)
	}
	limited := fuseRRF(keyword, dual, 1)
	if len(limited) != 1 || limited[0].ID != "dual" {
		t.Fatalf("融合结果应受 top_k 限制: %#v", limited)
	}
}

// 变更 2 的任务 1.1 与 2.2：关键字模式不调用嵌入服务；网关故障时 hybrid 明确报错而关键字仍可用。
func TestKeywordModeDoesNotCallEmbedding(t *testing.T) {
	a, ctx := testApp(t)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	a.gateway = &gatewayClient{baseURL: server.URL, embedModel: "test-model", dimensions: 4, timeout: time.Second, http: server.Client()}

	f := seedFixture(t, a, ctx)
	queueFixtureJob(t, a, ctx, f)
	job, err := a.claimKnowledgeJob(ctx)
	if err != nil || job == nil {
		t.Fatalf("领取失败: %v", err)
	}
	chunks := []sourceChunk{{Text: "课程项目数据库采用 MySQL。", Locator: locator{Type: "line", Start: 1, End: 1, Label: "第 1 行"}, Ordinal: 1, CharStart: 0, CharEnd: 13}}
	if err = a.publishKnowledge(ctx, *job, chunks, [][]float32{{1, 0, 0, 0}}); err != nil {
		t.Fatalf("发布失败: %v", err)
	}

	hits, embedCalled, err := a.searchCandidates(ctx, modeKeyword, f.classID, a.profileID, "数据库", nil, 5, similarityThreshold)
	if err != nil {
		t.Fatalf("关键字模式不应因网关故障失败: %v", err)
	}
	if embedCalled || requests != 0 {
		t.Fatalf("关键字模式不得调用嵌入服务: embedCalled=%v requests=%d", embedCalled, requests)
	}
	if len(hits) == 0 {
		t.Fatal("关键字模式应命中中文切片")
	}

	for _, mode := range []string{modeVector, modeHybrid} {
		if _, _, modeErr := a.searchCandidates(ctx, mode, f.classID, a.profileID, "数据库", nil, 5, similarityThreshold); modeErr == nil {
			t.Fatalf("%s 模式在网关故障时必须明确报错", mode)
		}
	}
	if requests == 0 {
		t.Fatal("vector/hybrid 模式应当尝试调用网关")
	}
}

// 变更 2 的任务 1.5：在标注语料上测量关键字模式的召回，作为绝对过滤下限的依据。
// 切片直接入库（绕过嵌入服务），因此这项测量不依赖真实课程网关。
func TestKeywordRecallOnLabelledSet(t *testing.T) {
	a, ctx := testApp(t)
	f := seedFixture(t, a, ctx)
	facts := [][2]string{
		{"课程考核由什么组成", "考核方式由平时作业百分之四十和期末项目百分之六十组成。"},
		{"缺勤多少次不能参加期末考核", "累计缺勤达到三次的学生不能参加期末项目答辩。"},
		{"作业最晚什么时候提交", "每周作业应在下周一晚上二十二点前提交。"},
		{"课程使用哪种前端框架", "课程前端项目统一使用 React 与 TypeScript。"},
		{"课程后端使用什么语言", "课程后端示例主要使用 Go 语言。"},
		{"数据库采用什么产品", "课程项目数据库采用 MySQL。"},
		{"课堂提问加分上限是多少", "课堂提问加分在总评中最多计入五分。"},
		{"小组最多允许几个人", "课程项目小组最多由四名学生组成。"},
		{"期末答辩持续多久", "每个小组的期末答辩时间为十五分钟。"},
		{"代码仓库多久提交一次", "项目代码仓库至少每周提交两次有效更新。"},
		{"接口文档采用什么格式", "接口文档统一采用 OpenAPI 三点一格式。"},
		{"设计稿需要包含什么", "设计稿必须包含桌面端和移动端两个尺寸。"},
		{"测试覆盖率最低要求", "核心业务模块的测试覆盖率不得低于百分之七十。"},
		{"迟交作业如何扣分", "作业每迟交一天扣除该次成绩的百分之十。"},
		{"课堂资料在哪里获取", "课堂资料统一在知序平台的班级讲义区域获取。"},
		{"项目演示使用什么环境", "项目演示必须使用本机 Docker Compose 环境。"},
		{"需求变更如何记录", "所有功能需求变更必须先写入 OpenSpec。"},
		{"安全检查包含哪些内容", "安全检查包含身份认证、权限隔离和输入校验。"},
		{"最终报告提交什么格式", "最终课程报告需要提交 PDF 格式文件。"},
		{"教师答疑安排在何时", "教师线上答疑安排在每周三晚上七点。"},
	}
	chunks := make([]sourceChunk, 0, len(facts))
	for i, fact := range facts {
		chunks = append(chunks, sourceChunk{
			Text: fact[1], Locator: locator{Type: "line", Start: i + 1, End: i + 1, Label: "第 1 行"},
			Ordinal: i + 1, CharStart: i * 40, CharEnd: i*40 + len([]rune(fact[1])),
		})
	}
	vectors := make([][]float32, len(chunks))
	for i := range vectors {
		vectors[i] = []float32{1, 0, 0, 0}
	}
	queueFixtureJob(t, a, ctx, f)
	job, err := a.claimKnowledgeJob(ctx)
	if err != nil || job == nil {
		t.Fatalf("领取失败: %v", err)
	}
	if err = a.publishKnowledge(ctx, *job, chunks, vectors); err != nil {
		t.Fatalf("发布失败: %v", err)
	}

	hits := 0
	for i, fact := range facts {
		found, searchErr := a.keywordSearch(ctx, a.db, f.classID, a.profileID, fact[0], nil, 5)
		if searchErr != nil {
			t.Fatalf("关键字查询失败: %v", searchErr)
		}
		for _, hit := range found {
			if hit.ID == chunkIDAt(t, a, ctx, f.handoutID, i+1) {
				hits++
				break
			}
		}
	}
	unrelated := []string{"量子色动力学夸克禁闭", "南极帝企鹅迁徙路线", "巴洛克时期羽管键琴", "火星土壤高氯酸盐", "古埃及象形文字语法"}
	falsePositives := 0
	for _, query := range unrelated {
		found, searchErr := a.keywordSearch(ctx, a.db, f.classID, a.profileID, query, nil, 5)
		if searchErr != nil {
			t.Fatalf("无关查询失败: %v", searchErr)
		}
		falsePositives += len(found)
	}
	t.Logf("关键字模式标注集召回：%d/%d 在前 5 条命中来源切片；%d 条无关查询共产生 %d 条命中", hits, len(facts), len(unrelated), falsePositives)
	if hits < len(facts)*6/10 {
		t.Fatalf("关键字模式召回过低（%d/%d），需要重新评估分词与下限策略", hits, len(facts))
	}
	if falsePositives > 3 {
		t.Fatalf("无关查询产生了过多命中（%d 条），需要提高关键字下限", falsePositives)
	}
}

func chunkIDAt(t *testing.T, a *App, ctx context.Context, handoutID string, ordinal int) string {
	t.Helper()
	var id string
	if err := a.db.QueryRowContext(ctx, `SELECT id FROM knowledge_chunks WHERE handout_id=? AND ordinal=?`, handoutID, ordinal).Scan(&id); err != nil {
		t.Fatalf("读取切片标识失败: %v", err)
	}
	return id
}

// 回归：网关未配置时上传不得失败，也不得留下会破坏外键的索引任务。
func TestUploadQueueingRequiresGateway(t *testing.T) {
	a, ctx := testApp(t)
	f := seedFixture(t, a, ctx)

	// 未配置网关：排队被跳过，上传路径本身不报错。
	a.gateway = &gatewayClient{dimensions: 4}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.queueKnowledgeForUpload(ctx, tx, f.handoutID, f.classID, f.hash); err != nil {
		t.Fatalf("未配置网关时排队应被跳过而不是报错: %v", err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var documents, jobs int
	if err = a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_documents WHERE handout_id=?`, f.handoutID).Scan(&documents); err != nil {
		t.Fatal(err)
	}
	if err = a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_jobs WHERE handout_id=?`, f.handoutID).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if documents != 0 || jobs != 0 {
		t.Fatalf("未配置网关时不应产生索引文档或任务: documents=%d jobs=%d", documents, jobs)
	}
	if status, _ := documentStatus2(t, a, ctx, f.handoutID); status != "" {
		t.Fatalf("未配置网关时讲义应显示为未建立索引，实际状态 %q", status)
	}

	// 配置网关后（嵌入配置已在 testApp 中注册）恢复排队。
	a.gateway = newGatewayClient(4)
	tx, err = a.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.queueKnowledgeForUpload(ctx, tx, f.handoutID, f.classID, f.hash); err != nil {
		t.Fatalf("配置网关后应能排队: %v", err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_jobs WHERE handout_id=?`, f.handoutID).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 1 {
		t.Fatalf("配置网关后应产生一个索引任务，实际 %d", jobs)
	}
}

func documentStatus2(t *testing.T, a *App, ctx context.Context, handoutID string) (string, string) {
	t.Helper()
	var status, errorCode string
	err := a.db.QueryRowContext(ctx, `SELECT status,error_code FROM knowledge_documents WHERE handout_id=?`, handoutID).Scan(&status, &errorCode)
	if err != nil {
		return "", ""
	}
	return status, errorCode
}

// 任务 1.4：ngram_token_size 不为 2 的实例必须拒绝写入索引。
// 需要一个以 --ngram_token_size=3 启动的实例，通过 MYSQL_TEST_DSN_NGRAM3 提供。
func TestServerVariableGuard(t *testing.T) {
	dsn := os.Getenv("MYSQL_TEST_DSN_NGRAM3")
	if dsn == "" {
		t.Skip("MYSQL_TEST_DSN_NGRAM3 未设置，跳过 ngram 守卫测试")
	}
	ctx := context.Background()
	t.Setenv("DATABASE_URL", dsn)
	db, err := openDatabase(ctx)
	if err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	defer db.Close()
	var ngram int
	if err = db.QueryRowContext(ctx, `SELECT @@ngram_token_size`).Scan(&ngram); err != nil {
		t.Fatal(err)
	}
	if ngram == 2 {
		t.Fatalf("该实例的 ngram_token_size 应为非 2，实际 %d", ngram)
	}
	if err = checkServerVariables(ctx, db); err == nil {
		t.Fatal("ngram_token_size 不为 2 时应拒绝启动索引写入")
	}
}

var errTemporary = errors.New("temporary failure")

func (e *permanentError) Unwrap() error { return e.err }
