package main

import (
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

// buildAnswerMessages 组装对话输入：只包含材料标题、切片序号、切片正文、用户最新一句与此前轮次。
func buildAnswerMessages(question string, rows []chunkRow, history []chatMessage) []chatMessage {
	var contextText strings.Builder
	for i, row := range rows {
		excerpt := row.Content
		if utf8.RuneCountInString(excerpt) > maxExcerptRunes {
			excerpt = string([]rune(excerpt)[:maxExcerptRunes])
		}
		fmt.Fprintf(&contextText, "[%d] 材料：%s（第 %d 片，第 %d–%d 字）\n%s\n\n", i+1, row.Title, row.Ordinal, row.CharStart+1, row.CharEnd, excerpt)
	}
	messages := []chatMessage{{
		Role: "system",
		Content: "你是课程资料助手。只能依据给出的本班材料片段用简短中文作答，并在句末用 [1]、[2] 这样的编号标注依据；" +
			"材料中没有答案时直接说明材料中未找到相关内容；不要引用编号之外的材料，也不要编造出处。",
	}}
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
	if utf8.RuneCountInString(input.Query) < 1 || utf8.RuneCountInString(input.Query) > 1000 {
		return fail(400, "提问须为 1–1000 字")
	}
	if !a.gateway.configured() || !a.gateway.chatConfigured() {
		return fail(503, "课程网关未配置，问答暂不可用")
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
			"status": "no_ready_documents", "mode": modeHybrid, "answer": "尚无可检索的讲义", "citations": []any{},
			"ready_documents": 0, "total_documents": total,
		})
		return nil
	}
	activeProfileID, _, threshold, err := a.activeProfile(r.Context())
	if err != nil {
		return fail(503, "检索模型配置不可用")
	}
	// 第一段：本班混合检索，最多取前 4 条。
	hits, _, err := a.searchCandidates(r.Context(), modeHybrid, classID, activeProfileID, input.Query, nil, answerTopK, threshold)
	if err != nil {
		return fail(503, "检索暂不可用，请稍后重试")
	}
	ids := make([]string, 0, len(hits))
	for _, hit := range hits {
		ids = append(ids, hit.ID)
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = knowledgeAccessTx(r.Context(), tx, classID, u); err != nil {
		return err
	}
	rows, err := a.publishedChunks(r.Context(), tx, classID, activeProfileID, ids, nil)
	if err != nil {
		return err
	}
	ordered := make([]chunkRow, 0, len(hits))
	for _, hit := range hits {
		if row, ok := rows[hit.ID]; ok {
			ordered = append(ordered, row)
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if len(ordered) == 0 {
		// 没有依据：直接给出固定文案，绝不访问对话网关。
		writeJSON(w, 200, map[string]any{
			"status": "no_matches", "mode": modeHybrid, "answer": noAnswerText, "citations": []any{},
			"ready_documents": ready, "total_documents": total, "partial": ready < total,
		})
		return nil
	}
	// 第二段：命中之后才把材料标题、切片序号与正文交给对话模块。
	messages := buildAnswerMessages(input.Query, ordered, normalizeHistory(input.History))
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
	citations := make([]map[string]any, 0, len(ordered))
	for i, row := range ordered {
		citations = append(citations, map[string]any{
			"index": i + 1, "chunk_id": row.ChunkID, "handout_id": row.HandoutID, "title": row.Title, "filename": row.Filename,
			"ordinal": row.Ordinal, "char_start": row.CharStart, "char_end": row.CharEnd, "content": row.Content,
			"locator": row.Locator, "download_url": "/api/handouts/" + row.HandoutID + "/file",
		})
	}
	writeJSON(w, 200, map[string]any{
		"status": "ok", "mode": modeHybrid, "answer": cleaned, "citations": citations,
		"ready_documents": ready, "total_documents": total, "partial": ready < total,
	})
	return nil
}
