package main

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type sourceUnit struct {
	Text    string
	Locator locator
}

// sourceChunk 是最终入库的切片：正文、定位、序号与在提取文本中的字符区间。
type sourceChunk struct {
	Text      string
	Locator   locator
	Ordinal   int
	CharStart int
	CharEnd   int
}

type workerJob struct {
	ID, HandoutID, ClassID, GenerationID, StoredName, Filename, Hash, Token string
	ProfileID, Model                                                        string
	Attempts                                                                int
	Policy                                                                  chunkingPolicy
	// 实际生效的切片策略与回退说明，随发布写入代次。
	ChunkStrategy, ChunkNote string
}
type permanentError struct {
	code string
	err  error
}

func (e *permanentError) Error() string { return e.err.Error() }

func (a *App) runKnowledgeWorker(ctx context.Context) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if err := a.reconcileKnowledgeProfile(ctx); err != nil {
			return err
		}
		job, err := a.claimKnowledgeJob(ctx)
		if err != nil {
			return err
		}
		if job != nil {
			a.processKnowledgeJob(ctx, *job)
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (a *App) claimKnowledgeJob(ctx context.Context) (*workerJob, error) {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// MySQL 不支持 FOR UPDATE OF：先在任务表上加行锁（跳过已被占用的行），再按主键取上下文。
	var jobID string
	err = tx.QueryRowContext(ctx, `SELECT j.id FROM knowledge_jobs j
		WHERE j.next_attempt_at<=NOW(6) AND (j.status='queued' OR (j.status='running' AND j.lease_until<NOW(6)))
		AND NOT EXISTS(SELECT 1 FROM classrooms c WHERE c.id=j.class_id AND c.archived=1)
		ORDER BY j.created_at LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&jobID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, tx.Commit()
	}
	if err != nil {
		return nil, err
	}
	var j workerJob
	err = tx.QueryRowContext(ctx, `SELECT j.id,j.handout_id,j.class_id,j.generation_id,h.stored_name,h.filename,g.content_hash,g.profile_id,p.model,j.attempts,
		g.chunk_strategy,g.chunk_size,g.chunk_overlap
		FROM knowledge_jobs j JOIN handouts h ON h.id=j.handout_id JOIN knowledge_generations g ON g.id=j.generation_id
		JOIN knowledge_profiles p ON p.id=g.profile_id WHERE j.id=?`, jobID).
		Scan(&j.ID, &j.HandoutID, &j.ClassID, &j.GenerationID, &j.StoredName, &j.Filename, &j.Hash, &j.ProfileID, &j.Model, &j.Attempts,
			&j.Policy.Strategy, &j.Policy.Size, &j.Policy.Overlap)
	if err != nil {
		return nil, err
	}
	j.Token = randomID(16)
	_, err = tx.ExecContext(ctx, `UPDATE knowledge_jobs SET status='running',attempts=attempts+1,lease_token=?,lease_until=NOW(6)+INTERVAL 10 MINUTE,updated_at=NOW(6) WHERE id=?`, j.Token, j.ID)
	if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE knowledge_documents SET status='extracting',error_code='',updated_at=NOW(6) WHERE handout_id=?`, j.HandoutID)
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	j.Attempts++
	return &j, nil
}

func (a *App) processKnowledgeJob(ctx context.Context, j workerJob) {
	path := filepath.Join(a.uploads, j.StoredName)
	units, err := extractDocument(ctx, path, j.Filename)
	if err != nil {
		a.failKnowledgeJob(ctx, j, err)
		return
	}
	chunks, outcome, err := chunkDocument(units, j.Policy, 10000)
	if err != nil {
		a.failKnowledgeJob(ctx, j, err)
		return
	}
	if len(chunks) == 0 {
		a.failKnowledgeJob(ctx, j, &permanentError{"no_text", errors.New("no extractable text")})
		return
	}
	j.ChunkStrategy, j.ChunkNote = outcome.Strategy, outcome.Note
	if _, err = a.db.ExecContext(ctx, `UPDATE knowledge_documents SET status='embedding',updated_at=NOW(6) WHERE handout_id=? AND EXISTS(SELECT 1 FROM knowledge_jobs WHERE id=? AND lease_token=? AND status='running')`, j.HandoutID, j.ID, j.Token); err != nil {
		a.failKnowledgeJob(ctx, j, err)
		return
	}
	vectors := make([][]float32, 0, len(chunks))
	for start := 0; start < len(chunks); start += 16 {
		tag, leaseErr := a.db.ExecContext(ctx, `UPDATE knowledge_jobs SET lease_until=NOW(6)+INTERVAL 10 MINUTE,updated_at=NOW(6) WHERE id=? AND lease_token=? AND status='running'`, j.ID, j.Token)
		affected, _ := tag.RowsAffected()
		if leaseErr != nil || affected != 1 {
			a.failKnowledgeJob(ctx, j, errors.New("worker lease expired"))
			return
		}
		end := start + 16
		if end > len(chunks) {
			end = len(chunks)
		}
		texts := make([]string, end-start)
		for i := start; i < end; i++ {
			texts[i-start] = chunks[i].Text
		}
		batch, embedErr := a.gateway.embed(ctx, texts)
		if embedErr != nil {
			a.failKnowledgeJob(ctx, j, embedErr)
			return
		}
		vectors = append(vectors, batch...)
	}
	if err = a.publishKnowledge(ctx, j, chunks, vectors); err != nil {
		a.failKnowledgeJob(ctx, j, err)
		return
	}
	log.Printf("knowledge job %s (handout %s)：%d 个切片已发布，策略 %s", j.ID, j.HandoutID, len(chunks), j.ChunkStrategy)
}

func (a *App) publishKnowledge(ctx context.Context, j workerJob, chunks []sourceChunk, vectors [][]float32) error {
	if len(chunks) != len(vectors) {
		return errors.New("chunk and vector count mismatch")
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var archived bool
	var stored string
	err = tx.QueryRowContext(ctx, `SELECT c.archived,h.stored_name FROM classrooms c JOIN handouts h ON h.class_id=c.id WHERE c.id=? AND h.id=? FOR UPDATE`, j.ClassID, j.HandoutID).Scan(&archived, &stored)
	if errors.Is(err, sql.ErrNoRows) {
		return &permanentError{"deleted", errors.New("handout deleted")}
	}
	if err != nil {
		return err
	}
	if archived {
		if _, err = tx.ExecContext(ctx, `UPDATE knowledge_jobs SET status='paused',lease_token=NULL,lease_until=NULL,updated_at=NOW(6) WHERE id=? AND lease_token=?`, j.ID, j.Token); err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE knowledge_documents SET status='paused',updated_at=NOW(6) WHERE handout_id=?`, j.HandoutID)
		}
		if err != nil {
			return err
		}
		return tx.Commit()
	}
	currentHash, err := fileHash(filepath.Join(a.uploads, stored))
	if err != nil {
		return err
	}
	if currentHash != j.Hash {
		return &permanentError{"source_changed", errors.New("source changed")}
	}
	var valid bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM knowledge_jobs WHERE id=? AND lease_token=? AND status='running' AND lease_until>NOW(6))`, j.ID, j.Token).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return errors.New("worker lease expired")
	}
	points := make([]vectorPoint, 0, len(chunks))
	for i, chunk := range chunks {
		raw, _ := json.Marshal(chunk.Locator)
		chunkID := randomUUID()
		_, err = tx.ExecContext(ctx, `INSERT INTO knowledge_chunks(id,generation_id,class_id,handout_id,ordinal,char_start,char_end,content,locator)
			VALUES(?,?,?,?,?,?,?,?,?)`, chunkID, j.GenerationID, j.ClassID, j.HandoutID, chunk.Ordinal, chunk.CharStart, chunk.CharEnd, chunk.Text, raw)
		if err != nil {
			return err
		}
		points = append(points, vectorPoint{
			ID:     chunkID,
			Vector: vectors[i],
			Payload: map[string]any{
				"class_id": j.ClassID, "handout_id": j.HandoutID, "generation_id": j.GenerationID, "ordinal": chunk.Ordinal,
			},
		})
	}
	// 先写向量再提交：事务回滚时遗留的向量点因为没有对应的有效代次而不会被检索到。
	if err = a.vectors.upsert(ctx, points); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE knowledge_generations SET status='ready',chunk_strategy=COALESCE(NULLIF(?,''),chunk_strategy),chunk_note=? WHERE id=?`, j.ChunkStrategy, j.ChunkNote, j.GenerationID); err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE knowledge_documents SET active_generation_id=CASE WHEN EXISTS(SELECT 1 FROM knowledge_profiles WHERE id=? AND active=1) THEN ? ELSE active_generation_id END,
			status='ready',error_code='',updated_at=NOW(6) WHERE handout_id=?`, j.ProfileID, j.GenerationID, j.HandoutID)
	}
	if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE knowledge_jobs SET status='done',lease_token=NULL,lease_until=NULL,updated_at=NOW(6) WHERE id=?`, j.ID)
	}
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	// 清理同一讲义旧代次的向量点；失败只记录，不影响已提交的有效索引。
	if err = a.vectors.deleteStaleGenerations(ctx, j.ClassID, j.HandoutID, j.GenerationID); err != nil {
		fmt.Fprintf(os.Stderr, "cleanup stale vectors: %v\n", err)
	}
	return nil
}

// reconcileKnowledgeProfile 在切换到新嵌入配置前先为全部知识文档建立完整代次，
// 只有全部成功才原子切换活动配置；命名锁保证多个 worker 不会重复建库。
func (a *App) reconcileKnowledgeProfile(ctx context.Context) error {
	conn, err := a.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var locked sql.NullInt64
	if err = conn.QueryRowContext(ctx, `SELECT GET_LOCK('knowledge-profile-rollout', 0)`).Scan(&locked); err != nil {
		return err
	}
	if !locked.Valid || locked.Int64 != 1 {
		return nil
	}
	defer conn.ExecContext(context.Background(), `SELECT RELEASE_LOCK('knowledge-profile-rollout')`)
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var active bool
	if err = tx.QueryRowContext(ctx, `SELECT active FROM knowledge_profiles WHERE id=? FOR UPDATE`, a.profileID).Scan(&active); err != nil {
		return err
	}
	if active {
		return tx.Commit()
	}
	type pendingDocument struct{ handoutID, classID, hash string }
	rows, err := tx.QueryContext(ctx, `SELECT d.handout_id,d.class_id,d.content_hash FROM knowledge_documents d
		WHERE NOT EXISTS(SELECT 1 FROM knowledge_generations g WHERE g.handout_id=d.handout_id AND g.profile_id=?)
		AND NOT EXISTS(SELECT 1 FROM knowledge_jobs j WHERE j.handout_id=d.handout_id AND j.status IN ('queued','running','paused'))
		ORDER BY d.handout_id FOR UPDATE`, a.profileID)
	if err != nil {
		return err
	}
	pending := []pendingDocument{}
	for rows.Next() {
		var item pendingDocument
		if err = rows.Scan(&item.handoutID, &item.classID, &item.hash); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, item := range pending {
		if err = queueKnowledge(ctx, tx, item.handoutID, item.classID, item.hash, a.profileID, defaultChunkingPolicy()); err != nil {
			return err
		}
	}
	if len(pending) > 0 {
		return tx.Commit()
	}
	var total, ready int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(CASE WHEN EXISTS(
		SELECT 1 FROM knowledge_generations g WHERE g.handout_id=d.handout_id AND g.profile_id=? AND g.status='ready') THEN 1 ELSE 0 END),0)
		FROM knowledge_documents d`, a.profileID).Scan(&total, &ready); err != nil {
		return err
	}
	if ready != total {
		return tx.Commit()
	}
	if _, err = tx.ExecContext(ctx, `UPDATE knowledge_documents d SET active_generation_id=(
		SELECT g.id FROM knowledge_generations g WHERE g.handout_id=d.handout_id AND g.profile_id=? AND g.status='ready'
		ORDER BY g.created_at DESC LIMIT 1),status='ready',error_code='',updated_at=NOW(6)`, a.profileID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE knowledge_profiles SET active=0 WHERE active=1`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE knowledge_profiles SET active=1 WHERE id=?`, a.profileID); err != nil {
		return err
	}
	return tx.Commit()
}

func (a *App) failKnowledgeJob(ctx context.Context, j workerJob, cause error) {
	code := "temporary_failure"
	permanent := false
	var p *permanentError
	if errors.As(cause, &p) {
		code = p.code
		permanent = true
	}
	if j.Attempts >= 3 {
		permanent = true
	}
	// 记下可诊断的原因：错误文本来自提取器或模型通道，不含密钥与讲义正文。
	log.Printf("knowledge job %s (handout %s) 第 %d 次尝试失败：code=%s permanent=%t cause=%v", j.ID, j.HandoutID, j.Attempts, code, permanent, cause)
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	var handoutID string
	if err = tx.QueryRowContext(ctx, `SELECT handout_id FROM knowledge_jobs WHERE id=? AND lease_token=? FOR UPDATE`, j.ID, j.Token).Scan(&handoutID); err != nil {
		return
	}
	if permanent {
		status := "failed"
		if code == "no_text" || code == "unsupported" || code == "encrypted" {
			status = "unsupported"
		}
		if _, err = tx.ExecContext(ctx, `UPDATE knowledge_jobs SET status='failed',error_code=?,lease_token=NULL,lease_until=NULL,updated_at=NOW(6) WHERE id=?`, code, j.ID); err != nil {
			return
		}
		if _, err = tx.ExecContext(ctx, `UPDATE knowledge_documents SET status=?,error_code=?,updated_at=NOW(6) WHERE handout_id=?`, status, code, handoutID); err != nil {
			return
		}
		if _, err = tx.ExecContext(ctx, `UPDATE knowledge_generations SET status='failed' WHERE id=?`, j.GenerationID); err != nil {
			return
		}
	} else {
		delay := time.Duration(1<<uint(j.Attempts-1)) * 5 * time.Second
		if _, err = tx.ExecContext(ctx, `UPDATE knowledge_jobs SET status='queued',error_code=?,lease_token=NULL,lease_until=NULL,next_attempt_at=?,updated_at=NOW(6) WHERE id=?`, code, time.Now().UTC().Add(delay), j.ID); err != nil {
			return
		}
		if _, err = tx.ExecContext(ctx, `UPDATE knowledge_documents SET status='queued',error_code=?,updated_at=NOW(6) WHERE handout_id=?`, code, handoutID); err != nil {
			return
		}
	}
	_ = tx.Commit()
}

func extractDocument(parent context.Context, path, filename string) ([]sourceUnit, error) {
	ctx, cancel := context.WithTimeout(parent, 120*time.Second)
	defer cancel()
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".txt", ".md":
		return extractText(path)
	case ".pdf":
		return extractPDF(ctx, path)
	case ".docx":
		return extractDOCX(path, false)
	case ".pptx":
		return extractPPTX(path, false)
	case ".doc", ".ppt":
		return convertLegacy(ctx, path, ext)
	default:
		return nil, &permanentError{"unsupported", errors.New("unsupported format")}
	}
}

func extractText(path string) ([]sourceUnit, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(data) {
		return nil, &permanentError{"unsupported", errors.New("invalid UTF-8")}
	}
	lines := strings.Split(strings.TrimPrefix(string(data), "\ufeff"), "\n")
	out := []sourceUnit{}
	for i, line := range lines {
		line = strings.TrimSuffix(line, "\r")
		if strings.TrimSpace(line) != "" {
			out = append(out, sourceUnit{line, locator{"line", i + 1, i + 1, fmt.Sprintf("第 %d 行", i+1), false}})
		}
	}
	return out, nil
}

func extractPDF(ctx context.Context, path string) ([]sourceUnit, error) {
	info, err := exec.CommandContext(ctx, "pdfinfo", path).CombinedOutput()
	if err != nil {
		return nil, &permanentError{"unsupported", fmt.Errorf("pdfinfo: %w", err)}
	}
	re := regexp.MustCompile(`(?m)^Pages:\s+(\d+)`)
	m := re.FindSubmatch(info)
	if len(m) != 2 {
		return nil, &permanentError{"unsupported", errors.New("PDF page count unavailable")}
	}
	pages, _ := strconv.Atoi(string(m[1]))
	out := []sourceUnit{}
	for page := 1; page <= pages; page++ {
		cmd := exec.CommandContext(ctx, "pdftotext", "-f", strconv.Itoa(page), "-l", strconv.Itoa(page), "-layout", path, "-")
		data, e := cmd.Output()
		if e != nil {
			return nil, &permanentError{"unsupported", e}
		}
		text := strings.TrimSpace(string(data))
		if text != "" {
			out = append(out, sourceUnit{text, locator{"page", page, page, fmt.Sprintf("第 %d 页", page), false}})
		}
	}
	if len(out) == 0 {
		return nil, &permanentError{"no_text", errors.New("scanned PDF")}
	}
	return out, nil
}

func zipXML(path, name string) ([]byte, error) {
	z, err := zip.OpenReader(path)
	if err != nil {
		return nil, &permanentError{"unsupported", err}
	}
	defer z.Close()
	var expanded uint64
	for _, f := range z.File {
		expanded += f.UncompressedSize64
		if expanded > 200<<20 {
			return nil, &permanentError{"resource_limit", errors.New("expanded document too large")}
		}
	}
	for _, f := range z.File {
		if f.Name == name {
			if f.UncompressedSize64 > 200<<20 {
				return nil, &permanentError{"resource_limit", errors.New("expanded document too large")}
			}
			r, e := f.Open()
			if e != nil {
				return nil, e
			}
			defer r.Close()
			return io.ReadAll(io.LimitReader(r, (200<<20)+1))
		}
	}
	return nil, &permanentError{"unsupported", errors.New("document XML missing")}
}
func xmlTexts(data []byte) ([]string, error) {
	d := xml.NewDecoder(bytes.NewReader(data))
	out := []string{}
	var b strings.Builder
	depth := 0
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, &permanentError{"unsupported", err}
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "p" {
				depth++
				b.Reset()
			}
			if t.Name.Local == "tab" && depth > 0 {
				b.WriteByte('\t')
			}
		case xml.CharData:
			if depth > 0 {
				b.Write([]byte(t))
			}
		case xml.EndElement:
			if t.Name.Local == "p" && depth > 0 {
				text := strings.TrimSpace(b.String())
				if text != "" {
					out = append(out, text)
				}
				depth--
			}
		}
	}
	return out, nil
}
func extractDOCX(path string, converted bool) ([]sourceUnit, error) {
	data, err := zipXML(path, "word/document.xml")
	if err != nil {
		return nil, err
	}
	texts, err := xmlTexts(data)
	if err != nil {
		return nil, err
	}
	out := make([]sourceUnit, len(texts))
	for i, text := range texts {
		out[i] = sourceUnit{text, locator{"paragraph", i + 1, i + 1, fmt.Sprintf("提取正文第 %d 段", i+1), converted}}
	}
	return out, nil
}
func extractPPTX(path string, converted bool) ([]sourceUnit, error) {
	z, err := zip.OpenReader(path)
	if err != nil {
		return nil, &permanentError{"unsupported", err}
	}
	defer z.Close()
	var expanded uint64
	for _, f := range z.File {
		expanded += f.UncompressedSize64
		if expanded > 200<<20 {
			return nil, &permanentError{"resource_limit", errors.New("expanded document too large")}
		}
	}
	re := regexp.MustCompile(`^ppt/slides/slide(\d+)\.xml$`)
	type slide struct {
		n int
		f *zip.File
	}
	slides := []slide{}
	for _, f := range z.File {
		m := re.FindStringSubmatch(f.Name)
		if len(m) == 2 {
			n, _ := strconv.Atoi(m[1])
			slides = append(slides, slide{n, f})
		}
	}
	sort.Slice(slides, func(i, j int) bool { return slides[i].n < slides[j].n })
	out := []sourceUnit{}
	for _, s := range slides {
		if s.f.UncompressedSize64 > 200<<20 {
			return nil, &permanentError{"resource_limit", errors.New("expanded document too large")}
		}
		r, e := s.f.Open()
		if e != nil {
			return nil, e
		}
		data, e := io.ReadAll(io.LimitReader(r, (200<<20)+1))
		r.Close()
		if e != nil {
			return nil, e
		}
		texts, e := xmlTexts(data)
		if e != nil {
			return nil, e
		}
		if len(texts) > 0 {
			out = append(out, sourceUnit{strings.Join(texts, "\n"), locator{"slide", s.n, s.n, fmt.Sprintf("第 %d 张幻灯片", s.n), converted}})
		}
	}
	return out, nil
}
func convertLegacy(ctx context.Context, path, ext string) ([]sourceUnit, error) {
	dir, err := os.MkdirTemp("", "knowledge-convert-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	input := filepath.Join(dir, "source"+ext)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err = os.WriteFile(input, data, 0600); err != nil {
		return nil, err
	}
	format := "docx"
	if ext == ".ppt" {
		format = "pptx"
	}
	cmd := exec.CommandContext(ctx, "libreoffice", "--headless", "--nologo", "--nodefault", "--nofirststartwizard", "--convert-to", format, "--outdir", dir, input)
	cmd.Env = []string{"HOME=" + dir, "PATH=" + os.Getenv("PATH")}
	if output, e := cmd.CombinedOutput(); e != nil {
		return nil, &permanentError{"unsupported", fmt.Errorf("office conversion: %s", strings.TrimSpace(string(output)))}
	}
	converted := filepath.Join(dir, "source."+format)
	if ext == ".ppt" {
		return extractPPTX(converted, true)
	}
	return extractDOCX(converted, true)
}
