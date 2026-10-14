package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// 「解题助手」的会话与流式回答。与单次问答共用同一条检索、提示组装与出处生成路径，
// 区别只在回答以 text/event-stream 逐块返回。
const (
	maxChatMessageRunes = 1000
	chatStreamDeadline  = 5 * time.Minute
	chatHeartbeat       = 15 * time.Second
	chatPageSize        = 50
	chatTitleRunes      = 30
)

type chatInput struct {
	Content string `json:"content"`
}

type sessionInput struct {
	Title string `json:"title"`
}

type chatSessionView struct {
	ID            string `json:"id"`
	ClassID       string `json:"class_id"`
	UserID        string `json:"user_id"`
	Title         string `json:"title"`
	MessageCount  int    `json:"message_count"`
	Mine          bool   `json:"mine"`
	CreatedAt     string `json:"created_at"`
	LastMessageAt string `json:"last_message_at,omitempty"`
}

type chatMessageView struct {
	ID        string           `json:"id"`
	Seq       int              `json:"seq"`
	Role      string           `json:"role"`
	Status    string           `json:"status"`
	Content   string           `json:"content"`
	Citations []map[string]any `json:"citations"`
	CreatedAt string           `json:"created_at"`
}

// sseWriter 串行化事件写入：增量事件与心跳可能来自不同 goroutine。
type sseWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
	mu      sync.Mutex
}

func (s *sseWriter) event(name string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err = fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", name, raw); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

func (s *sseWriter) comment(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Fprintf(s.w, ": %s\n\n", text)
	s.flusher.Flush()
}

// loadChatSession 读取班级内的会话；不属于该班或不存在都按 404 处理，跨班因此表现一致。
func (a *App) loadChatSession(ctx context.Context, classID, sessionID string) (chatSessionView, error) {
	view := chatSessionView{}
	var last sql.NullTime
	err := a.db.QueryRowContext(ctx, `SELECT id,class_id,user_id,title,created_at,last_message_at,
		(SELECT COUNT(*) FROM chat_messages m WHERE m.session_id=s.id) FROM chat_sessions s WHERE s.id=? AND s.class_id=?`,
		sessionID, classID).Scan(&view.ID, &view.ClassID, &view.UserID, &view.Title, &view.CreatedAt, &last, &view.MessageCount)
	if errors.Is(err, sql.ErrNoRows) {
		return view, fail(404, "会话不存在")
	}
	if err != nil {
		return view, err
	}
	if last.Valid {
		view.LastMessageAt = last.Time.UTC().Format(time.RFC3339)
	}
	return view, nil
}

// appendChatMessage 在一个短事务里锁定会话行后追加消息，保证 seq 连续且并发发送不会重号。
func (a *App) appendChatMessage(ctx context.Context, sessionID, classID, role, content, status string, citations []map[string]any, model string, promptChars, latencyMS int) (chatMessageView, error) {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return chatMessageView{}, err
	}
	defer tx.Rollback()
	var exists string
	if err = tx.QueryRowContext(ctx, `SELECT id FROM chat_sessions WHERE id=? AND class_id=? FOR UPDATE`, sessionID, classID).Scan(&exists); err != nil {
		return chatMessageView{}, err
	}
	var seq int
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0)+1 FROM chat_messages WHERE session_id=?`, sessionID).Scan(&seq); err != nil {
		return chatMessageView{}, err
	}
	var citationsJSON any
	if citations != nil {
		raw, marshalErr := json.Marshal(citations)
		if marshalErr != nil {
			return chatMessageView{}, marshalErr
		}
		citationsJSON = string(raw)
	}
	view := chatMessageView{ID: newID(), Seq: seq, Role: role, Content: content, Status: status, Citations: citations}
	if _, err = tx.ExecContext(ctx, `INSERT INTO chat_messages(id,session_id,class_id,seq,role,content,status,citations,model,prompt_chars,latency_ms)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`, view.ID, sessionID, classID, seq, role, content, status, citationsJSON, model, promptChars, latencyMS); err != nil {
		return chatMessageView{}, err
	}
	// 首条用户消息顺带定标题，方便会话列表辨认。
	if role == "user" {
		var title string
		if err = tx.QueryRowContext(ctx, `SELECT title FROM chat_sessions WHERE id=?`, sessionID).Scan(&title); err != nil {
			return chatMessageView{}, err
		}
		if strings.TrimSpace(title) == "" {
			runes := []rune(strings.TrimSpace(content))
			if len(runes) > chatTitleRunes {
				runes = runes[:chatTitleRunes]
			}
			title = string(runes)
		}
		if _, err = tx.ExecContext(ctx, `UPDATE chat_sessions SET title=?,updated_at=NOW(6),last_message_at=NOW(6) WHERE id=?`, title, sessionID); err != nil {
			return chatMessageView{}, err
		}
	} else if _, err = tx.ExecContext(ctx, `UPDATE chat_sessions SET updated_at=NOW(6),last_message_at=NOW(6) WHERE id=?`, sessionID); err != nil {
		return chatMessageView{}, err
	}
	if err = tx.Commit(); err != nil {
		return chatMessageView{}, err
	}
	if err = a.db.QueryRowContext(ctx, `SELECT created_at FROM chat_messages WHERE id=?`, view.ID).Scan(&view.CreatedAt); err != nil {
		return view, err
	}
	if view.Citations == nil {
		view.Citations = []map[string]any{}
	}
	return view, nil
}

// chatHistory 读取会话中该序号之前的最近若干轮，供组装对话输入使用。
func (a *App) chatHistory(ctx context.Context, sessionID string, beforeSeq int) ([]chatMessage, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT role,content FROM chat_messages WHERE session_id=? AND seq<? AND status<>'failed' ORDER BY seq DESC LIMIT ?`,
		sessionID, beforeSeq, maxHistoryMessages)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	reversed := []chatMessage{}
	for rows.Next() {
		var item chatMessage
		if err = rows.Scan(&item.Role, &item.Content); err != nil {
			return nil, err
		}
		reversed = append(reversed, item)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	out := make([]chatMessage, 0, len(reversed))
	for i := len(reversed) - 1; i >= 0; i-- {
		out = append(out, reversed[i])
	}
	return out, nil
}

// POST /api/classes/{id}/sessions：新建会话（归档班级禁止）。
func (a *App) createChatSession(w http.ResponseWriter, r *http.Request, u *User) error {
	c, err := a.access(r, r.PathValue("id"), u)
	if err != nil {
		return err
	}
	if c.Archived {
		return fail(403, "班级已归档，不能新建会话")
	}
	var input sessionInput
	if err = decodeLenient(w, r, &input); err != nil {
		return err
	}
	title := strings.TrimSpace(input.Title)
	if utf8.RuneCountInString(title) > 60 {
		return fail(400, "标题不能超过 60 个字符")
	}
	id := newID()
	if _, err = a.db.ExecContext(r.Context(), `INSERT INTO chat_sessions(id,class_id,user_id,title) VALUES(?,?,?,?)`, id, c.ID, u.ID, title); err != nil {
		return err
	}
	view, err := a.loadChatSession(r.Context(), c.ID, id)
	if err != nil {
		return err
	}
	view.Mine = view.UserID == u.ID
	writeJSON(w, 201, view)
	return nil
}

// GET /api/classes/{id}/sessions：本班会话列表（教师与学生都能看到本班会话）。
func (a *App) listChatSessions(w http.ResponseWriter, r *http.Request, u *User) error {
	c, err := a.access(r, r.PathValue("id"), u)
	if err != nil {
		return err
	}
	rows, err := a.db.QueryContext(r.Context(), `SELECT s.id,s.class_id,s.user_id,s.title,s.created_at,s.last_message_at,
		(SELECT COUNT(*) FROM chat_messages m WHERE m.session_id=s.id)
		FROM chat_sessions s WHERE s.class_id=? ORDER BY COALESCE(s.last_message_at,s.created_at) DESC LIMIT ?`, c.ID, chatPageSize)
	if err != nil {
		return err
	}
	defer rows.Close()
	list := []chatSessionView{}
	for rows.Next() {
		view := chatSessionView{}
		var last sql.NullTime
		if err = rows.Scan(&view.ID, &view.ClassID, &view.UserID, &view.Title, &view.CreatedAt, &last, &view.MessageCount); err != nil {
			return err
		}
		if last.Valid {
			view.LastMessageAt = last.Time.UTC().Format(time.RFC3339)
		}
		view.Mine = view.UserID == u.ID
		list = append(list, view)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"sessions": list})
	return nil
}

// GET /api/classes/{id}/sessions/{sid}：读取会话消息（按 seq 升序，可向前分页）。
func (a *App) chatSessionMessages(w http.ResponseWriter, r *http.Request, u *User) error {
	c, err := a.access(r, r.PathValue("id"), u)
	if err != nil {
		return err
	}
	session, err := a.loadChatSession(r.Context(), c.ID, r.PathValue("sid"))
	if err != nil {
		return err
	}
	session.Mine = session.UserID == u.ID
	rows, err := a.db.QueryContext(r.Context(), `SELECT id,seq,role,status,content,citations,created_at FROM chat_messages
		WHERE session_id=? ORDER BY seq LIMIT ?`, session.ID, chatPageSize)
	if err != nil {
		return err
	}
	defer rows.Close()
	messages := []chatMessageView{}
	for rows.Next() {
		view := chatMessageView{}
		var citations sql.NullString
		if err = rows.Scan(&view.ID, &view.Seq, &view.Role, &view.Status, &view.Content, &citations, &view.CreatedAt); err != nil {
			return err
		}
		view.Citations = decodeCitations(citations)
		messages = append(messages, view)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"session": session, "messages": messages})
	return nil
}

func decodeCitations(raw sql.NullString) []map[string]any {
	out := []map[string]any{}
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return out
	}
	if err := json.Unmarshal([]byte(raw.String), &out); err != nil {
		return []map[string]any{}
	}
	if out == nil {
		return []map[string]any{}
	}
	return out
}

// POST /api/classes/{id}/sessions/{sid}/messages：发送一条消息并以 SSE 流式返回回答。
//
// 顺序是固定的：先落库用户消息 → 再检索（失败时仍用普通 JSON 返回错误，避免流已开始还改状态码）
// → 无命中直接返回固定文案且不调用对话模型 → 有命中才把材料交给模型并逐块转发。
func (a *App) sendChatMessage(w http.ResponseWriter, r *http.Request, u *User) error {
	c, err := a.access(r, r.PathValue("id"), u)
	if err != nil {
		return err
	}
	if c.Archived {
		return fail(403, "班级已归档，不能继续提问")
	}
	var input chatInput
	if err = decodeLenient(w, r, &input); err != nil {
		return err
	}
	content := strings.TrimSpace(input.Content)
	if utf8.RuneCountInString(content) < 1 || utf8.RuneCountInString(content) > maxChatMessageRunes {
		return fail(400, "消息须为 1–1000 字")
	}
	if !a.gateway.configured() || !a.gateway.chatConfigured() {
		return fail(503, "课程网关未配置，问答暂不可用")
	}
	session, err := a.loadChatSession(r.Context(), c.ID, r.PathValue("sid"))
	if err != nil {
		return err
	}
	// 用户消息先持久化：即使随后流式失败或客户端断开，会话里也保留这条消息。
	userMessage, err := a.appendChatMessage(r.Context(), session.ID, c.ID, "user", content, "complete", nil, "", 0, 0)
	if err != nil {
		return err
	}
	history, err := a.chatHistory(r.Context(), session.ID, userMessage.Seq)
	if err != nil {
		return err
	}
	evidence, err := a.retrieveAnswerContext(r.Context(), c.ID, u, content)
	if err != nil {
		return err
	}
	promptBody, _, err := a.effectivePrompt(r.Context(), c.ID)
	if err != nil {
		return err
	}
	skills, err := a.effectiveSkills(r.Context(), c.ID)
	if err != nil {
		return err
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		return fail(500, "当前服务器不支持流式返回")
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	stream := &sseWriter{w: w, flusher: flusher}

	// 无依据：固定文案 + 空出处，绝不调用对话模型。
	if evidence.NoReady || len(evidence.Rows) == 0 {
		status, text := "no_matches", noAnswerText
		if evidence.NoReady {
			status, text = "no_ready_documents", "尚无可检索的讲义"
		}
		assistant, err := a.appendChatMessage(r.Context(), session.ID, c.ID, "assistant", text, "complete", []map[string]any{}, "", 0, 0)
		if err != nil {
			return err
		}
		_ = stream.event("delta", map[string]any{"text": text})
		_ = stream.event("citations", map[string]any{"message_id": assistant.ID, "citations": []any{}})
		_ = stream.event("done", map[string]any{"status": status, "message_id": assistant.ID})
		return nil
	}

	messages := buildAnswerMessages(buildSystemPrompt(promptBody, skills), content, evidence.Rows, normalizeHistory(history))
	promptChars := 0
	for _, message := range messages {
		promptChars += len([]rune(message.Content))
	}
	citations := citationPayload(evidence.Rows)
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(chatHeartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				stream.comment("heartbeat")
			}
		}
	}()

	var builder strings.Builder
	started := time.Now()
	emit := func(text string) error {
		builder.WriteString(text)
		return stream.event("delta", map[string]any{"text": text})
	}
	streamCtx, cancel := context.WithTimeout(r.Context(), chatStreamDeadline)
	streamErr := a.gateway.chatStream(streamCtx, messages, emit)
	cancel()
	close(stop)

	// 断开或失败后的落库不能再用已取消的请求上下文。
	persistCtx, persistCancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Second)
	defer persistCancel()
	answer, _ := sanitizeAnswer(builder.String(), len(evidence.Rows))
	status := "complete"
	if streamErr != nil {
		status = "interrupted"
		log.Printf("knowledge chat: 会话 %s 回答中断: %v（已收 %d 字）", session.ID, streamErr, utf8.RuneCountInString(answer))
	}
	assistant, persistErr := a.appendChatMessage(persistCtx, session.ID, c.ID, "assistant", answer, status, citations, a.gateway.chatModel, promptChars, int(time.Since(started).Milliseconds()))
	if persistErr != nil {
		log.Printf("knowledge chat: 保存助手消息失败: %v", persistErr)
	}
	if streamErr != nil {
		_ = stream.event("error", map[string]any{"error": "回答中断，请重试"})
	}
	messageID := ""
	if persistErr == nil {
		messageID = assistant.ID
	}
	_ = stream.event("citations", map[string]any{"message_id": messageID, "citations": citations})
	_ = stream.event("done", map[string]any{"status": status, "message_id": messageID})
	return nil
}
