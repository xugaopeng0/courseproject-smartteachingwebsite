package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const similarityThreshold = 0.35

func validateEmbeddings(vectors [][]float32, expectedCount, dimensions int) error {
	if len(vectors) != expectedCount {
		return errors.New("embedding count mismatch")
	}
	for _, vector := range vectors {
		if len(vector) != dimensions {
			return fmt.Errorf("embedding dimension %d, expected %d", len(vector), dimensions)
		}
		var norm float64
		for _, value := range vector {
			if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
				return errors.New("embedding contains non-finite value")
			}
			norm += float64(value * value)
		}
		if norm == 0 {
			return errors.New("embedding is zero vector")
		}
	}
	return nil
}

// ensureKnowledgeProfile 注册当前嵌入配置；模型、维度或预处理方式变化时必须换用新的 profile id。
func (a *App) ensureKnowledgeProfile(ctx context.Context) error {
	var activeExists bool
	if err := a.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM knowledge_profiles WHERE active=1)`).Scan(&activeExists); err != nil {
		return err
	}
	_, err := a.db.ExecContext(ctx, `INSERT INTO knowledge_profiles(id,model,dimensions,preprocessing_version,similarity_threshold,active)
		VALUES(?,?,?,?,?,?) ON DUPLICATE KEY UPDATE id=id`, a.profileID, a.gateway.embedModel, a.gateway.dimensions, chunkerVersion, similarityThreshold, !activeExists)
	if err != nil {
		return err
	}
	// 自愈：网关未配置时注册的空模型行会在配置补齐后挡住启动，这里直接改用当前配置。
	if _, err = a.db.ExecContext(ctx, `UPDATE knowledge_profiles SET model=?,dimensions=?,preprocessing_version=? WHERE id=? AND model=''`,
		a.gateway.embedModel, a.gateway.dimensions, chunkerVersion, a.profileID); err != nil {
		return err
	}
	var model, preprocessing string
	var dimensions int
	if err = a.db.QueryRowContext(ctx, `SELECT model,dimensions,preprocessing_version FROM knowledge_profiles WHERE id=?`, a.profileID).Scan(&model, &dimensions, &preprocessing); err != nil {
		return err
	}
	if model != a.gateway.embedModel || dimensions != a.gateway.dimensions || preprocessing != chunkerVersion {
		return fmt.Errorf("embedding profile %s configuration does not match its stored version", a.profileID)
	}
	return nil
}

// activeProfile 返回当前可用的嵌入配置与相似度阈值。
func (a *App) activeProfile(ctx context.Context) (id, model string, threshold float64, err error) {
	err = a.db.QueryRowContext(ctx, `SELECT id,model,similarity_threshold FROM knowledge_profiles WHERE active=1`).Scan(&id, &model, &threshold)
	return id, model, threshold, err
}

func fileHash(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	h := sha256.New()
	if _, err = io.Copy(h, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func queueKnowledge(ctx context.Context, tx *sql.Tx, handoutID, classID, hash, profileID string, policy chunkingPolicy) error {
	generationID, jobID := randomID(16), randomID(16)
	_, err := tx.ExecContext(ctx, `INSERT INTO knowledge_documents(handout_id,class_id,content_hash,status,error_code)
		VALUES(?,?,?,'queued','') ON DUPLICATE KEY UPDATE content_hash=VALUES(content_hash),status='queued',error_code='',updated_at=NOW(6)`, handoutID, classID, hash)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO knowledge_generations(id,handout_id,class_id,profile_id,content_hash,parser_version,chunker_version,chunk_strategy,requested_strategy,chunk_size,chunk_overlap,status)
		VALUES(?,?,?,?,?,'extractors-v1',?,?,?,?,?,'building')`, generationID, handoutID, classID, profileID, hash, chunkerVersion, policy.Strategy, policy.Strategy, policy.Size, policy.Overlap)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO knowledge_jobs(id,handout_id,class_id,generation_id,status) VALUES(?,?,?,?,'queued')`, jobID, handoutID, classID, generationID)
	return err
}

// queueKnowledgeForUpload 只在嵌入通道可用时排队。
// 网关未配置时上传必须照常成功，讲义显示为“未建立索引”，等配置完成后再由所有者建立索引。
func (a *App) queueKnowledgeForUpload(ctx context.Context, tx *sql.Tx, handoutID, classID, hash string) error {
	if !a.gateway.configured() {
		return nil
	}
	return queueKnowledge(ctx, tx, handoutID, classID, hash, a.profileID, defaultChunkingPolicy())
}

type knowledgeDocument struct {
	HandoutID     string     `json:"handout_id"`
	Title         string     `json:"title"`
	Filename      string     `json:"filename"`
	Status        string     `json:"status"`
	ErrorCode     string     `json:"error_code"`
	HasReadyIndex bool       `json:"has_ready_index"`
	Strategy      string     `json:"strategy,omitempty"`
	Requested     string     `json:"requested_strategy,omitempty"`
	ChunkNote     string     `json:"chunk_note,omitempty"`
	UpdatedAt     *time.Time `json:"updated_at,omitempty"`
}

func (a *App) requireKnowledge() error {
	if !a.knowledge || a.gateway == nil || a.vectors == nil {
		return fail(503, "知识库服务尚未启用")
	}
	return nil
}

func knowledgeAccessTx(ctx context.Context, tx *sql.Tx, classID string, u *User) error {
	var owner string
	err := tx.QueryRowContext(ctx, `SELECT owner_id FROM classrooms WHERE id=? FOR SHARE`, classID).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return fail(404, "班级不存在")
	}
	if err != nil {
		return err
	}
	if owner == u.ID {
		return nil
	}
	var member bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM memberships WHERE class_id=? AND user_id=?)`, classID, u.ID).Scan(&member); err != nil {
		return err
	}
	if !member {
		return fail(403, "你不是该班级的成员")
	}
	return nil
}

func (a *App) knowledgeDocuments(w http.ResponseWriter, r *http.Request, u *User) error {
	if err := a.requireKnowledge(); err != nil {
		return err
	}
	if _, err := a.access(r, r.PathValue("id"), u); err != nil {
		return err
	}
	rows, err := a.db.QueryContext(r.Context(), `SELECT h.id,h.title,h.filename,COALESCE(d.status,'not_indexed'),COALESCE(d.error_code,''),
		d.active_generation_id IS NOT NULL,d.updated_at,
		COALESCE((SELECT g.chunk_strategy FROM knowledge_generations g WHERE g.handout_id=h.id ORDER BY g.created_at DESC LIMIT 1),''),
		COALESCE((SELECT g.requested_strategy FROM knowledge_generations g WHERE g.handout_id=h.id ORDER BY g.created_at DESC LIMIT 1),''),
		COALESCE((SELECT g.chunk_note FROM knowledge_generations g WHERE g.handout_id=h.id ORDER BY g.created_at DESC LIMIT 1),'')
		FROM handouts h LEFT JOIN knowledge_documents d ON d.handout_id=h.id
		WHERE h.class_id=? ORDER BY h.uploaded_at DESC`, r.PathValue("id"))
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []knowledgeDocument{}
	for rows.Next() {
		var d knowledgeDocument
		if err = rows.Scan(&d.HandoutID, &d.Title, &d.Filename, &d.Status, &d.ErrorCode, &d.HasReadyIndex, &d.UpdatedAt, &d.Strategy, &d.Requested, &d.ChunkNote); err != nil {
			return err
		}
		items = append(items, d)
	}
	writeJSON(w, 200, map[string]any{"documents": items})
	return rows.Err()
}

func (a *App) indexKnowledgeDocument(w http.ResponseWriter, r *http.Request, u *User) error {
	if err := a.requireKnowledge(); err != nil {
		return err
	}
	if !a.gateway.configured() {
		// 没有可用的嵌入通道时排队只会得到必然失败的任务，直接说明原因。
		return fail(503, "课程网关未配置，暂不能建立索引")
	}
	var input struct {
		Strategy  string `json:"strategy"`
		Separator string `json:"separator"`
		Size      int    `json:"size"`
		Overlap   int    `json:"overlap"`
	}
	if err := decodeOptional(w, r, &input); err != nil {
		return err
	}
	if input.Size != 0 && (input.Size < 100 || input.Size > maxChunkSize) {
		return fail(400, "切片窗口须为 100–4000 字")
	}
	policy, err := chunkingPolicy{Strategy: input.Strategy, Separator: input.Separator, Size: input.Size, Overlap: input.Overlap}.normalize()
	if err != nil {
		return err
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var owner, stored string
	var archived bool
	err = tx.QueryRowContext(r.Context(), `SELECT c.owner_id,c.archived,h.stored_name FROM classrooms c JOIN handouts h ON h.class_id=c.id WHERE c.id=? AND h.id=? FOR UPDATE`, r.PathValue("id"), r.PathValue("handoutID")).Scan(&owner, &archived, &stored)
	if errors.Is(err, sql.ErrNoRows) {
		return fail(404, "讲义不存在")
	}
	if err != nil {
		return err
	}
	if owner != u.ID {
		return fail(403, "仅任课教师可以建立索引")
	}
	if archived {
		return fail(409, "班级已归档，请先恢复")
	}
	var live bool
	if err = tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM knowledge_jobs WHERE handout_id=? AND status IN ('queued','running','paused'))`, r.PathValue("handoutID")).Scan(&live); err != nil {
		return err
	}
	if !live {
		hash, hashErr := fileHash(filepath.Join(a.uploads, stored))
		if hashErr != nil {
			return fail(503, "讲义文件暂不可用")
		}
		if err = queueKnowledge(r.Context(), tx, r.PathValue("handoutID"), r.PathValue("id"), hash, a.profileID, policy); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"queued": true})
	return nil
}

type searchInput struct {
	Query      string   `json:"query"`
	Mode       string   `json:"mode"`
	TopK       int      `json:"top_k"`
	HandoutIDs []string `json:"handout_ids"`
}
type locator struct {
	Type      string `json:"type"`
	Start     int    `json:"start"`
	End       int    `json:"end"`
	Label     string `json:"label"`
	Converted bool   `json:"converted,omitempty"`
}

// searchHit 是检索阶段的候选：只带标识与分数，正文一律回关系库取。
type searchHit struct {
	ID    string
	Score float64
}

const (
	modeKeyword = "keyword"
	modeVector  = "vector"
	modeHybrid  = "hybrid"

	// rrfK 是倒数排名融合的常数；名次分只用于排序，不作为相关性阈值。
	rrfK = 60
	// 每路进入融合前保留的候选数量，避免单路以数量淹没另一路。
	fusionPoolSize = 50
	// keywordMinScore 是关键字路径的绝对过滤下限：默认 0 表示至少命中一个二字 token。
	keywordMinScore = 0.0
)

func validMode(mode string) bool {
	return mode == modeKeyword || mode == modeVector || mode == modeHybrid
}

// rowsQueryer 让关键字检索既能直接用连接池，也能在事务里执行。
type rowsQueryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// keywordSearch 只查询 MySQL 全文索引，不调用嵌入服务。
func (a *App) keywordSearch(ctx context.Context, q rowsQueryer, classID, profileID, query string, handoutIDs []string, limit int) ([]searchHit, error) {
	statement := `SELECT k.id, MATCH(k.content) AGAINST(? IN NATURAL LANGUAGE MODE) AS score
		FROM knowledge_chunks k
		JOIN knowledge_documents d ON d.handout_id=k.handout_id AND d.active_generation_id=k.generation_id
		JOIN knowledge_generations g ON g.id=k.generation_id
		JOIN handouts h ON h.id=k.handout_id
		WHERE k.class_id=? AND h.class_id=?
		AND MATCH(k.content) AGAINST(? IN NATURAL LANGUAGE MODE) > ?`
	args := []any{query, classID, classID, query, keywordMinScore}
	if profileID != "" {
		// 关键字模式不依赖嵌入配置；只有存在活动配置时才用它进一步限定代次。
		statement += ` AND g.profile_id=?`
		args = append(args, profileID)
	}
	if len(handoutIDs) > 0 {
		statement += ` AND h.id IN (` + placeholders(len(handoutIDs)) + `)`
		args = append(args, stringsToAny(handoutIDs)...)
	}
	statement += ` ORDER BY score DESC LIMIT ?`
	args = append(args, limit)
	rows, err := q.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	hits := []searchHit{}
	for rows.Next() {
		var hit searchHit
		if err = rows.Scan(&hit.ID, &hit.Score); err != nil {
			return nil, err
		}
		hits = append(hits, hit)
	}
	return hits, rows.Err()
}

// vectorSearch 通过 Qdrant 取得候选标识与余弦分数；低于阈值的结果由向量库直接过滤。
func (a *App) vectorSearch(ctx context.Context, vector []float32, classID string, handoutIDs []string, limit int, threshold float64) ([]searchHit, error) {
	found, err := a.vectors.search(ctx, vector, classID, handoutIDs, limit, threshold)
	if err != nil {
		return nil, err
	}
	hits := make([]searchHit, 0, len(found))
	for _, hit := range found {
		hits = append(hits, searchHit{ID: hit.ID, Score: hit.Score})
	}
	return hits, nil
}

// fuseRRF 按倒数排名融合两路结果：两路均命中的切片一定排在仅单路命中之前。
func fuseRRF(keyword, vector []searchHit, limit int) []searchHit {
	scores := map[string]float64{}
	order := []string{}
	accumulate := func(list []searchHit) {
		for index, hit := range list {
			if _, seen := scores[hit.ID]; !seen {
				order = append(order, hit.ID)
			}
			scores[hit.ID] += 1.0 / float64(rrfK+index+1)
		}
	}
	accumulate(keyword)
	accumulate(vector)
	fused := make([]searchHit, 0, len(order))
	for _, id := range order {
		fused = append(fused, searchHit{ID: id, Score: scores[id]})
	}
	sort.SliceStable(fused, func(i, j int) bool { return fused[i].Score > fused[j].Score })
	if len(fused) > limit {
		fused = fused[:limit]
	}
	return fused
}

type chunkRow struct {
	ChunkID, HandoutID, Title, Filename, Content, Version string
	Ordinal, CharStart, CharEnd                           int
	Locator                                               locator
}

// publishedChunks 按主键回关系库取正文，并校验班级、讲义与已发布代次。
func (a *App) publishedChunks(ctx context.Context, tx *sql.Tx, classID, profileID string, ids, handoutIDs []string) (map[string]chunkRow, error) {
	out := map[string]chunkRow{}
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(ids)+4)
	placeholders := make([]string, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args = append(args, id)
	}
	args = append(args, classID, classID, profileID)
	query := `SELECT k.id,k.ordinal,k.char_start,k.char_end,h.id,h.title,h.filename,k.content,g.content_hash,k.locator
		FROM knowledge_chunks k
		JOIN knowledge_documents d ON d.handout_id=k.handout_id AND d.active_generation_id=k.generation_id
		JOIN knowledge_generations g ON g.id=k.generation_id
		JOIN handouts h ON h.id=k.handout_id
		WHERE k.id IN (` + strings.Join(placeholders, ",") + `) AND k.class_id=? AND h.class_id=? AND g.profile_id=?`
	if len(handoutIDs) > 0 {
		filter := make([]string, len(handoutIDs))
		for i, id := range handoutIDs {
			filter[i] = "?"
			args = append(args, id)
		}
		query += ` AND h.id IN (` + strings.Join(filter, ",") + `)`
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var row chunkRow
		var raw []byte
		if err = rows.Scan(&row.ChunkID, &row.Ordinal, &row.CharStart, &row.CharEnd, &row.HandoutID, &row.Title, &row.Filename, &row.Content, &row.Version, &raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &row.Locator); err != nil {
			return nil, err
		}
		out[row.ChunkID] = row
	}
	return out, rows.Err()
}

// searchCandidates 执行所选模式的检索，并报告本次是否调用了嵌入服务。
// 关键字模式完全不调用网关，因此网关故障时它仍然可用。
func (a *App) searchCandidates(ctx context.Context, mode, classID, profileID, query string, handoutIDs []string, topK int, threshold float64) ([]searchHit, bool, error) {
	switch mode {
	case modeKeyword:
		hits, err := a.keywordSearch(ctx, a.db, classID, profileID, query, handoutIDs, topK)
		return hits, false, err
	case modeVector:
		vectors, err := a.gateway.embed(ctx, []string{query})
		if err != nil {
			return nil, true, err
		}
		hits, err := a.vectorSearch(ctx, vectors[0], classID, handoutIDs, topK, threshold)
		return hits, true, err
	default:
		keywordHits, err := a.keywordSearch(ctx, a.db, classID, profileID, query, handoutIDs, fusionPoolSize)
		if err != nil {
			return nil, false, err
		}
		vectors, err := a.gateway.embed(ctx, []string{query})
		if err != nil {
			return nil, true, err
		}
		vectorHits, err := a.vectorSearch(ctx, vectors[0], classID, handoutIDs, fusionPoolSize, threshold)
		if err != nil {
			return nil, true, err
		}
		return fuseRRF(keywordHits, vectorHits, topK), true, nil
	}
}

func (a *App) searchKnowledge(w http.ResponseWriter, r *http.Request, u *User) error {
	if err := a.requireKnowledge(); err != nil {
		return err
	}
	classID := r.PathValue("id")
	if _, err := a.access(r, classID, u); err != nil {
		return err
	}
	var input searchInput
	// 检索请求忽略未识别字段：调用方另行携带的班级编号在解析后被丢弃，而不是报错。
	if err := decodeLenient(w, r, &input); err != nil {
		return err
	}
	input.Query = strings.TrimSpace(input.Query)
	if input.Mode == "" {
		input.Mode = modeHybrid
	}
	if input.TopK == 0 {
		input.TopK = 5
	}
	if utf8.RuneCountInString(input.Query) < 1 || utf8.RuneCountInString(input.Query) > 1000 || input.TopK < 1 || input.TopK > 20 {
		return fail(400, "查询须为 1–1000 字，结果数量须为 1–20")
	}
	if !validMode(input.Mode) {
		return fail(400, "检索模式只支持 keyword、vector 或 hybrid")
	}
	if len(input.HandoutIDs) > 100 {
		return fail(400, "讲义筛选数量过多")
	}
	if len(input.HandoutIDs) > 0 {
		var count int
		if err := a.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM handouts WHERE class_id=? AND id IN (`+placeholders(len(input.HandoutIDs))+`)`, append([]any{classID}, stringsToAny(input.HandoutIDs)...)...).Scan(&count); err != nil {
			return err
		}
		if count != len(input.HandoutIDs) {
			return fail(404, "讲义不存在")
		}
	}
	var total, ready int
	if err := a.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM handouts WHERE class_id=?`, classID).Scan(&total); err != nil {
		return err
	}
	if err := a.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM handouts h JOIN knowledge_documents d ON d.handout_id=h.id
		JOIN knowledge_generations g ON g.id=d.active_generation_id JOIN knowledge_profiles p ON p.id=g.profile_id AND p.active=1
		WHERE h.class_id=?`, classID).Scan(&ready); err != nil {
		return err
	}
	if ready == 0 {
		writeJSON(w, 200, map[string]any{
			"status": "no_ready_documents", "mode": input.Mode, "ready_documents": 0, "total_documents": total,
			"hits": []any{}, "message": "尚无可检索的讲义",
		})
		return nil
	}
	// 关键字模式不调用网关；vector 与 hybrid 需要已配置的网关与活动嵌入配置。
	activeProfile := ""
	threshold := similarityThreshold
	if input.Mode == modeKeyword {
		if id, _, _, profileErr := a.activeProfile(r.Context()); profileErr == nil {
			activeProfile = id
		}
	} else {
		if !a.gateway.configured() {
			return fail(503, "课程网关未配置，语义检索暂不可用")
		}
		profile, _, profileThreshold, profileErr := a.activeProfile(r.Context())
		if profileErr != nil {
			return fail(503, "检索模型配置不可用")
		}
		activeProfile, threshold = profile, profileThreshold
	}
	// 模型调用与数据库读取分开：关键字路径完全不调用嵌入服务。
	hits, embedCalled, err := a.searchCandidates(r.Context(), input.Mode, classID, activeProfile, input.Query, input.HandoutIDs, input.TopK, threshold)
	if err != nil {
		// 记录可诊断的原因：错误文本来自模型通道或向量库，不含密钥与讲义正文。
		log.Printf("knowledge search mode=%s embed_called=%t failed: %v", input.Mode, embedCalled, err)
		if embedCalled {
			// 关键字模式不依赖网关，仍可返回结果；vector 与 hybrid 在这里明确报错。
			return fail(503, "课程网关暂不可用，请稍后重试")
		}
		return fail(503, "检索暂不可用，请稍后重试")
	}
	ids := make([]string, 0, len(hits))
	for _, hit := range hits {
		ids = append(ids, hit.ID)
	}
	// 模型调用之后再开一个短事务复核权限与来源，最终决定权在这里。
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = knowledgeAccessTx(r.Context(), tx, classID, u); err != nil {
		return err
	}
	rows, err := a.publishedChunks(r.Context(), tx, classID, activeProfile, ids, input.HandoutIDs)
	if err != nil {
		return err
	}
	results := []map[string]any{}
	for _, hit := range hits {
		row, ok := rows[hit.ID]
		if !ok {
			// 向量库残留点：代次已失效、讲义已删除或不属于本班，直接丢弃。
			continue
		}
		item := map[string]any{
			"chunk_id": row.ChunkID, "handout_id": row.HandoutID, "title": row.Title, "filename": row.Filename,
			"content": row.Content, "content_version": row.Version, "locator": row.Locator,
			"ordinal": row.Ordinal, "char_start": row.CharStart, "char_end": row.CharEnd,
		}
		if input.Mode == modeVector {
			// 只有向量模式给出余弦相似度；它表示相似程度，不是事实正确概率。
			item["similarity"] = hit.Score
		}
		results = append(results, item)
	}
	status, message := "ok", ""
	if len(results) == 0 {
		status, message = "no_matches", "资料中未找到相关内容"
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{
		"status": status, "mode": input.Mode, "message": message,
		"ready_documents": ready, "total_documents": total, "partial": ready < total, "hits": results,
	})
	return nil
}

func (a *App) knowledgeSource(w http.ResponseWriter, r *http.Request, u *User) error {
	if err := a.requireKnowledge(); err != nil {
		return err
	}
	classID := r.PathValue("id")
	if _, err := a.access(r, classID, u); err != nil {
		return err
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = knowledgeAccessTx(r.Context(), tx, classID, u); err != nil {
		return err
	}
	var handoutID, title, filename, content, version string
	var ordinal, charStart, charEnd int
	var raw []byte
	var active bool
	err = tx.QueryRowContext(r.Context(), `SELECT h.id,h.title,h.filename,k.ordinal,k.char_start,k.char_end,k.content,g.content_hash,k.locator,d.active_generation_id=k.generation_id
		FROM knowledge_chunks k JOIN knowledge_documents d ON d.handout_id=k.handout_id JOIN knowledge_generations g ON g.id=k.generation_id
		JOIN handouts h ON h.id=k.handout_id WHERE k.id=? AND k.class_id=? AND h.class_id=?`, r.PathValue("chunkID"), classID, classID).Scan(&handoutID, &title, &filename, &ordinal, &charStart, &charEnd, &content, &version, &raw, &active)
	if errors.Is(err, sql.ErrNoRows) {
		return fail(404, "来源不存在")
	}
	if err != nil {
		return err
	}
	if !active {
		return fail(409, "来源已失效，请重新检索")
	}
	var loc locator
	if err = json.Unmarshal(raw, &loc); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"chunk_id": r.PathValue("chunkID"), "handout_id": handoutID, "title": title, "filename": filename,
		"content": content, "content_version": version, "locator": loc, "ordinal": ordinal, "char_start": charStart, "char_end": charEnd,
		"download_url": "/api/handouts/" + handoutID + "/file"})
	return nil
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func stringsToAny(values []string) []any {
	out := make([]any, len(values))
	for i, value := range values {
		out[i] = value
	}
	return out
}

// decodeLenient 解析请求体但忽略未识别字段：调用方另行携带的班级编号等字段在解析后被丢弃，而不是报错。
func decodeLenient(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return fail(400, "请求内容不正确")
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	if err = d.Decode(v); err != nil {
		return fail(400, "请求内容不正确")
	}
	if err = d.Decode(&struct{}{}); err != io.EOF {
		return fail(400, "请求只能包含一个 JSON 对象")
	}
	return nil
}

// decodeOptional 允许空请求体：不带正文时使用默认策略。
func decodeOptional(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return fail(400, "请求内容不正确")
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err = d.Decode(v); err != nil {
		return fail(400, "请求内容不正确")
	}
	if err = d.Decode(&struct{}{}); err != io.EOF {
		return fail(400, "请求只能包含一个 JSON 对象")
	}
	return nil
}
