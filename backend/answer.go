package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// answerTopK 是交给对话模块的切片上限：既控制提示长度，也让编号范围有限。
	answerTopK = 4
	// 历史轮次与单条消息的长度上限，避免客户端塞入超长上下文。
	maxHistoryMessages = 6
	maxHistoryRunes    = 2000
	// 单条摘录进入提示的上限。
	maxExcerptRunes = 1200
	// 无命中时的固定文案。
	noAnswerText = "资料中未找到相关内容"
)

type answerInput struct {
	Query   string        `json:"query"`
	App     string        `json:"app"`
	History []chatMessage `json:"history"`
}

// normalizeHistory 丢弃客户端构造的 system 消息与其他角色，并限制轮次与长度。
func normalizeHistory(history []chatMessage) []chatMessage {
	out := []chatMessage{}
	total := 0
	for _, message := range history {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		if role != "user" && role != "assistant" {
			// 客户端注入的指令类消息一律不进对话模块。
			continue
		}
		content := strings.TrimSpace(message.Content)
		if content == "" {
			continue
		}
		if utf8.RuneCountInString(content) > maxHistoryRunes {
			content = string([]rune(content)[:maxHistoryRunes])
		}
		total += utf8.RuneCountInString(content)
		if total > maxHistoryRunes*2 {
			break
		}
		out = append(out, chatMessage{Role: role, Content: content})
	}
	if len(out) > maxHistoryMessages {
		out = out[len(out)-maxHistoryMessages:]
	}
	return out
}

// buildAnswerMessages 组装对话输入：系统提示（固定约束 + 提示词 + 技能）、此前轮次，
// 以及包含材料标题、切片序号与正文的最新一句。MUST NOT 出现向量、其他班级切片或内部字段。
func buildAnswerMessages(systemPrompt, question string, rows []chunkRow, history []chatMessage) []chatMessage {
	var contextText strings.Builder
	for i, row := range rows {
		excerpt := row.Content
		if utf8.RuneCountInString(excerpt) > maxExcerptRunes {
			excerpt = string([]rune(excerpt)[:maxExcerptRunes])
		}
		fmt.Fprintf(&contextText, "[%d] 材料：%s（第 %d 片，第 %d–%d 字）\n%s\n\n", i+1, row.Title, row.Ordinal, row.CharStart+1, row.CharEnd, excerpt)
	}
	messages := []chatMessage{{Role: "system", Content: systemPrompt}}
	messages = append(messages, history...)
	messages = append(messages, chatMessage{Role: "user", Content: "材料片段：\n" + contextText.String() + "问题：" + question})
	return messages
}

var citationPattern = regexp.MustCompile(`\[(\d{1,3})\]`)

// sanitizeAnswer 校正回答里的引用编号：只有落在命中范围内的编号才作为引用呈现，
// 越界编号去掉方括号后保留数字文本，使其不再看起来像出处。
func sanitizeAnswer(answer string, count int) (string, []int) {
	used := []int{}
	seen := map[int]bool{}
	cleaned := citationPattern.ReplaceAllStringFunc(answer, func(match string) string {
		index := 0
		if _, err := fmt.Sscanf(match, "[%d]", &index); err != nil {
			return match
		}
		if index >= 1 && index <= count {
			if !seen[index] {
				seen[index] = true
				used = append(used, index)
			}
			return match
		}
		return fmt.Sprintf("%d", index)
	})
	return cleaned, used
}

// askKnowledge 先检索、后生成：只有命中本班切片时才调用对话模块。
func (a *App) askKnowledge(w http.ResponseWriter, r *http.Request, u *User) error {
	if err := a.requireKnowledge(); err != nil {
		return err
	}
	classID := r.PathValue("id")
	if _, err := a.access(r, classID, u); err != nil {
		return err
	}
	var input answerInput
	if err := decodeLenient(w, r, &input); err != nil {
		return err
	}
	input.Query = strings.TrimSpace(input.Query)
	input.App = strings.TrimSpace(input.App)
	if input.App == "" {
		input.App = appQA
	}
	if _, ok := findApp(input.App); !ok {
		return fail(400, "未知应用：可用应用为 qa（知识库问答）与 quiz（出题练习）")
	}
	if utf8.RuneCountInString(input.Query) < 1 || utf8.RuneCountInString(input.Query) > 1000 {
		return fail(400, "提问须为 1–1000 字")
	}
	if !a.gateway.configured() || !a.gateway.chatConfigured() {
		return fail(503, "课程网关未配置，问答暂不可用")
	}
	evidence, err := a.retrieveAnswerContext(r.Context(), classID, u, input.Query)
	if err != nil {
		return err
	}
	if evidence.NoReady {
		writeJSON(w, 200, map[string]any{
			"status": "no_ready_documents", "mode": modeHybrid, "answer": "尚无可检索的讲义", "citations": []any{},
			"ready_documents": 0, "total_documents": evidence.Total,
		})
		return nil
	}
	ordered := evidence.Rows
	if len(ordered) == 0 {
		// 没有依据：直接给出固定文案，绝不访问对话网关。
		writeJSON(w, 200, map[string]any{
			"status": "no_matches", "mode": modeHybrid, "answer": noAnswerText, "citations": []any{},
			"ready_documents": evidence.Ready, "total_documents": evidence.Total, "partial": evidence.Ready < evidence.Total,
		})
		return nil
	}
	// 第二段：命中之后才把材料标题、切片序号与正文交给对话模块。
	promptBody, _, err := a.effectivePrompt(r.Context(), classID)
	if err != nil {
		return err
	}
	skills, err := a.effectiveSkills(r.Context(), classID)
	if err != nil {
		return err
	}
	// 提示词模板支持变量（{{question}} 等），第三个应用（出题练习）再追加任务段。
	promptBody = renderTemplate(promptBody, a.promptVariables(classID, input.Query, skills))
	taskPrompt, err := a.buildTaskPrompt(input.App, classID, input.Query, skills)
	if err != nil {
		return err
	}
	systemPrompt := buildSystemPrompt(promptBody, skills)
	if taskPrompt != "" {
		systemPrompt += "\n\n" + taskPrompt
	}
	messages := buildAnswerMessages(systemPrompt, input.Query, ordered, normalizeHistory(input.History))
	promptLength := 0
	for _, message := range messages {
		promptLength += len([]rune(message.Content))
	}
	started := time.Now()
	answer, err := a.gateway.chat(r.Context(), messages)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fail(503, "问答暂不可用，请稍后重试")
		}
		return fail(503, "课程网关暂不可用，请稍后重试")
	}
	// 记录上下文规模与耗时，便于验收记录模型版本、提示长度与问答耗时。
	log.Printf("knowledge ask: 材料 %d 条，历史 %d 条，提示 %d 字，耗时 %d ms，模型 %s",
		len(ordered), len(messages)-1, promptLength, time.Since(started).Milliseconds(), a.gateway.chatModel)
	cleaned, used := sanitizeAnswer(answer, len(ordered))
	if len(used) == 0 {
		log.Printf("knowledge ask: 回答未引用任何命中切片（本次命中 %d 条）", len(ordered))
	}
	writeJSON(w, 200, map[string]any{
		"status": "ok", "mode": modeHybrid, "answer": cleaned, "citations": citationPayload(ordered),
		"ready_documents": evidence.Ready, "total_documents": evidence.Total, "partial": evidence.Ready < evidence.Total,
	})
	return nil
}

// answerEvidence 是一次回答所需的检索结果与覆盖统计。
type answerEvidence struct {
	Rows    []chunkRow
	Total   int
	Ready   int
	NoReady bool
}

// retrieveAnswerContext 执行「覆盖统计 → 本班混合检索（前 4 条）→ 回表复核权限与已发布代次」。
// 单次问答与流式会话共用这一条路径，保证两者的检索与隔离语义一致。
func (a *App) retrieveAnswerContext(ctx context.Context, classID string, u *User, query string) (answerEvidence, error) {
	var out answerEvidence
	if err := a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM handouts WHERE class_id=?`, classID).Scan(&out.Total); err != nil {
		return out, err
	}
	if err := a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM handouts h JOIN knowledge_documents d ON d.handout_id=h.id
		JOIN knowledge_generations g ON g.id=d.active_generation_id JOIN knowledge_profiles p ON p.id=g.profile_id AND p.active=1
		WHERE h.class_id=?`, classID).Scan(&out.Ready); err != nil {
		return out, err
	}
	if out.Ready == 0 {
		out.NoReady = true
		return out, nil
	}
	activeProfileID, _, threshold, err := a.activeProfile(ctx)
	if err != nil {
		return out, fail(503, "检索模型配置不可用")
	}
	hits, _, err := a.searchCandidates(ctx, modeHybrid, classID, activeProfileID, query, nil, answerTopK, threshold)
	if err != nil {
		log.Printf("knowledge answer retrieve: %v", err)
		return out, fail(503, "检索暂不可用，请稍后重试")
	}
	ids := make([]string, 0, len(hits))
	for _, hit := range hits {
		ids = append(ids, hit.ID)
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = knowledgeAccessTx(ctx, tx, classID, u); err != nil {
		return out, err
	}
	rows, err := a.publishedChunks(ctx, tx, classID, activeProfileID, ids, nil)
	if err != nil {
		return out, err
	}
	ordered := make([]chunkRow, 0, len(hits))
	for _, hit := range hits {
		if row, ok := rows[hit.ID]; ok {
			ordered = append(ordered, row)
		}
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	out.Rows = ordered
	return out, nil
}

// citationPayload 由服务端从命中切片生成出处：编号即展示顺序，位置与摘录都可核对。
func citationPayload(rows []chunkRow) []map[string]any {
	citations := make([]map[string]any, 0, len(rows))
	for i, row := range rows {
		citations = append(citations, map[string]any{
			"index": i + 1, "chunk_id": row.ChunkID, "handout_id": row.HandoutID, "title": row.Title, "filename": row.Filename,
			"ordinal": row.Ordinal, "char_start": row.CharStart, "char_end": row.CharEnd, "content": row.Content,
			"locator": row.Locator, "download_url": "/api/handouts/" + row.HandoutID + "/file",
		})
	}
	return citations
}
