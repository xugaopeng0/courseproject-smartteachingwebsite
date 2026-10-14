package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

// chatStub 是同时提供嵌入与流式对话的替身：嵌入用于检索，对话用于验证 SSE。
type chatStub struct {
	server     *httptest.Server
	client     *gatewayClient
	chatCalls  int32
	lastPrompt string
}

type chatStubConfig struct {
	chunks    []string
	delay     time.Duration
	status    int
	nonStream bool
	truncate  bool
}

func newChatStub(t *testing.T, cfg chatStubConfig) *chatStub {
	t.Helper()
	stub := &chatStub{}
	stub.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/embeddings":
			var body struct {
				Input []string `json:"input"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			vector := []float32{0, 0, 1, 0}
			for _, text := range body.Input {
				if strings.Contains(text, "考核") {
					vector = []float32{1, 0, 0, 0}
					break
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"embedding": vector}}})
		case "/chat/completions":
			atomic.AddInt32(&stub.chatCalls, 1)
			var body struct {
				Messages []chatMessage `json:"messages"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			var builder strings.Builder
			for _, message := range body.Messages {
				builder.WriteString(message.Role + ":" + message.Content + "\n")
			}
			stub.lastPrompt = builder.String()
			if cfg.status != 0 {
				w.WriteHeader(cfg.status)
				return
			}
			if cfg.nonStream {
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"content": "整段回答"}}}})
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, ok := w.(http.Flusher)
			if !ok {
				t.Errorf("替身需要支持 Flush")
				return
			}
			for _, chunk := range cfg.chunks {
				if cfg.delay > 0 {
					time.Sleep(cfg.delay)
				}
				sseDelta(w, chunk)
				flusher.Flush()
			}
			if !cfg.truncate {
				fmt.Fprint(w, "data: [DONE]\n\n")
				flusher.Flush()
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(stub.server.Close)
	stub.client = &gatewayClient{baseURL: stub.server.URL, embedModel: "embed-model", chatModel: "chat-model", dimensions: 4, timeout: 3 * time.Second}
	return stub
}

type sseEvent struct {
	Name string
	Data map[string]any
	Raw  string
}

// readSSE 解析事件流，直到响应结束。
func readSSE(t *testing.T, body io.Reader) []sseEvent {
	t.Helper()
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	events := []sseEvent{}
	name := ""
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event:"):
			name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			raw := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			payload := map[string]any{}
			if err := json.Unmarshal([]byte(raw), &payload); err != nil {
				t.Fatalf("事件数据不是 JSON：%s", raw)
			}
			events = append(events, sseEvent{Name: name, Data: payload, Raw: raw})
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("读取事件流失败: %v", err)
	}
	return events
}

func (h *authHarness) chatRequest(t *testing.T, method, path, body string, headers map[string]string) (*http.Response, []sseEvent) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, h.server.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := h.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp, readSSE(t, resp.Body)
}

func (h *authHarness) seedChunks(t *testing.T) {
	t.Helper()
	publishChineseChunks(t, h.app, h.ctx, h.f)
}

func (h *authHarness) createSession(t *testing.T, headers map[string]string) string {
	t.Helper()
	resp, body := h.do(t, http.MethodPost, "/api/classes/"+h.f.classID+"/sessions", `{}`, headers)
	if resp.StatusCode != 201 {
		t.Fatalf("新建会话失败：%d %s", resp.StatusCode, body)
	}
	var payload struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatal(err)
	}
	return payload.ID
}

func ownerHeaders(t *testing.T, h *authHarness) map[string]string {
	t.Helper()
	return map[string]string{"Authorization": "Bearer " + h.issueToken(t)}
}

// 任务 4.1 / 4.3：会话流程、事件顺序与响应头。
func TestChatSessionFlowAndEvents(t *testing.T) {
	h := newAuthHarness(t)
	h.seedChunks(t)
	stub := newChatStub(t, chatStubConfig{chunks: []string{"考核由平时作业", "与期末项目组成。[1]"}})
	h.app.gateway = stub.client
	headers := ownerHeaders(t, h)
	sessionID := h.createSession(t, headers)
	base := "/api/classes/" + h.f.classID + "/sessions/" + sessionID

	resp, events := h.chatRequest(t, http.MethodPost, base+"/messages", `{"content":"考核方式是什么"}`, headers)
	if resp.StatusCode != 200 {
		t.Fatalf("发送消息应 200，实际 %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("响应应为事件流，实际 %q", ct)
	}
	if resp.Header.Get("X-Accel-Buffering") != "no" || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("事件流必须禁止缓冲与缓存：%q %q", resp.Header.Get("X-Accel-Buffering"), resp.Header.Get("Cache-Control"))
	}
	if len(events) < 3 {
		t.Fatalf("事件过少：%+v", events)
	}
	order := []string{}
	deltas := 0
	for _, event := range events {
		order = append(order, event.Name)
		if event.Name == "delta" {
			deltas++
		}
	}
	if deltas < 2 {
		t.Fatalf("应有多个增量事件，实际 %d（%v）", deltas, order)
	}
	if order[len(order)-2] != "citations" || order[len(order)-1] != "done" {
		t.Fatalf("事件顺序应为 ... citations, done，实际 %v", order)
	}
	done := events[len(events)-1]
	if done.Data["status"] != "complete" {
		t.Fatalf("结束状态应为 complete：%+v", done.Data)
	}
	if stub.chatCalls != 1 {
		t.Fatalf("有命中时应调用一次对话模块，实际 %d", stub.chatCalls)
	}
	if !strings.Contains(stub.lastPrompt, "考核方式由平时作业") {
		t.Fatalf("提示应包含命中材料：%s", stub.lastPrompt)
	}
	if !strings.Contains(stub.lastPrompt, guidanceSkillName) {
		t.Fatalf("提示应包含启用中的技能：%s", stub.lastPrompt)
	}

	// 消息落库：用户消息在前、助手消息在后且为 complete。
	resp, body := h.do(t, http.MethodGet, base, "", headers)
	if resp.StatusCode != 200 {
		t.Fatalf("读取会话失败：%d %s", resp.StatusCode, body)
	}
	var payload struct {
		Session  chatSessionView   `json:"session"`
		Messages []chatMessageView `json:"messages"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Messages) != 2 {
		t.Fatalf("应有两条消息，实际 %d：%s", len(payload.Messages), body)
	}
	if payload.Messages[0].Role != "user" || payload.Messages[0].Seq != 1 || payload.Messages[0].Content != "考核方式是什么" {
		t.Fatalf("第一条应是用户消息：%+v", payload.Messages[0])
	}
	assistant := payload.Messages[1]
	if assistant.Role != "assistant" || assistant.Seq != 2 || assistant.Status != "complete" {
		t.Fatalf("第二条应是完成状态的助手消息：%+v", assistant)
	}
	if assistant.Content != "考核由平时作业与期末项目组成。[1]" {
		t.Fatalf("助手回答应与增量拼接一致：%q", assistant.Content)
	}
	if len(assistant.Citations) != 1 {
		t.Fatalf("出处应落库：%+v", assistant.Citations)
	}
	if payload.Session.Title == "" || payload.Session.MessageCount != 2 {
		t.Fatalf("会话标题与消息数应更新：%+v", payload.Session)
	}

	// 空白与超长消息被拒绝。
	if resp, body := h.do(t, http.MethodPost, base+"/messages", `{"content":"   "}`, headers); resp.StatusCode != 400 {
		t.Fatalf("空白消息应 400，实际 %d %s", resp.StatusCode, body)
	}
	long := strings.Repeat("字", maxChatMessageRunes+1)
	if resp, body := h.do(t, http.MethodPost, base+"/messages", `{"content":"`+long+`"}`, headers); resp.StatusCode != 400 {
		t.Fatalf("超长消息应 400，实际 %d %s", resp.StatusCode, body)
	}
}

// 任务 4.4：无命中时不调用对话模块，只返回固定文案与空出处。
func TestChatNoMatchesSkipsChatGateway(t *testing.T) {
	h := newAuthHarness(t)
	h.seedChunks(t)
	stub := newChatStub(t, chatStubConfig{chunks: []string{"不应该被调用"}})
	h.app.gateway = stub.client
	headers := ownerHeaders(t, h)
	sessionID := h.createSession(t, headers)

	resp, events := h.chatRequest(t, http.MethodPost, "/api/classes/"+h.f.classID+"/sessions/"+sessionID+"/messages", `{"content":"南极帝企鹅的迁徙路线"}`, headers)
	if resp.StatusCode != 200 {
		t.Fatalf("应返回 200 事件流，实际 %d", resp.StatusCode)
	}
	if stub.chatCalls != 0 {
		t.Fatalf("无命中时绝不能调用对话模块，实际 %d 次", stub.chatCalls)
	}
	text := ""
	for _, event := range events {
		if event.Name == "delta" {
			text += event.Data["text"].(string)
		}
	}
	if text != noAnswerText {
		t.Fatalf("无命中应只返回固定文案，实际 %q", text)
	}
	last := events[len(events)-1]
	if last.Name != "done" || last.Data["status"] != "no_matches" {
		t.Fatalf("结束事件应为 no_matches：%+v", last)
	}
	// 助手消息同样落库，出处为空。
	resp, body := h.do(t, http.MethodGet, "/api/classes/"+h.f.classID+"/sessions/"+sessionID, "", headers)
	if resp.StatusCode != 200 {
		t.Fatalf("读取会话失败：%d %s", resp.StatusCode, body)
	}
	var payload struct {
		Messages []chatMessageView `json:"messages"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Messages) != 2 || payload.Messages[1].Content != noAnswerText || len(payload.Messages[1].Citations) != 0 {
		t.Fatalf("无命中也应记录固定文案且出处为空：%s", body)
	}
}

// 任务 4.4：客户端中途断开时，已生成部分按 interrupted 记录，绝不记成完整回答。
func TestChatInterruptionIsRecorded(t *testing.T) {
	h := newAuthHarness(t)
	h.seedChunks(t)
	stub := newChatStub(t, chatStubConfig{chunks: []string{"第一段", "第二段", "第三段", "第四段"}, delay: 150 * time.Millisecond})
	h.app.gateway = stub.client
	headers := ownerHeaders(t, h)
	sessionID := h.createSession(t, headers)

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.server.URL+"/api/classes/"+h.f.classID+"/sessions/"+sessionID+"/messages", strings.NewReader(`{"content":"考核方式是什么"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := h.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)
	// 收到第一个增量事件后立即取消，模拟关闭页面。
	if _, err = reader.ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	if _, err = reader.ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	cancel()
	time.Sleep(1200 * time.Millisecond)

	var status, content string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		err = h.app.db.QueryRowContext(h.ctx, `SELECT status,content FROM chat_messages WHERE session_id=? AND role='assistant'`, sessionID).Scan(&status, &content)
		if err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("中断后仍应保存助手消息：%v", err)
	}
	if status != "interrupted" {
		t.Fatalf("中断的回答应记录为 interrupted，实际 %q（内容 %q）", status, content)
	}
	if content == "" {
		t.Fatal("应保留已收到的增量内容")
	}
	var complete int
	if err = h.app.db.QueryRowContext(h.ctx, `SELECT COUNT(*) FROM chat_messages WHERE session_id=? AND role='assistant' AND status='complete'`, sessionID).Scan(&complete); err != nil {
		t.Fatal(err)
	}
	if complete != 0 {
		t.Fatal("中断的回答不得记成 complete")
	}
}

// 任务 4.5：越界编号不呈现为引用，出处与来源接口一致。
func TestChatCitationsAndOutOfRangeMarkers(t *testing.T) {
	h := newAuthHarness(t)
	h.seedChunks(t)
	stub := newChatStub(t, chatStubConfig{chunks: []string{"见 [1] 与 [7] 的说明。[2]"}})
	h.app.gateway = stub.client
	headers := ownerHeaders(t, h)
	sessionID := h.createSession(t, headers)

	resp, events := h.chatRequest(t, http.MethodPost, "/api/classes/"+h.f.classID+"/sessions/"+sessionID+"/messages", `{"content":"考核方式是什么"}`, headers)
	if resp.StatusCode != 200 {
		t.Fatalf("发送消息失败：%d", resp.StatusCode)
	}
	var citations []any
	for _, event := range events {
		if event.Name == "citations" {
			if raw, ok := event.Data["citations"].([]any); ok {
				citations = raw
			}
		}
	}
	if len(citations) != 1 {
		t.Fatalf("命中一条切片时应返回一条出处，实际 %+v", citations)
	}
	first, _ := citations[0].(map[string]any)
	if first["index"].(float64) != 1 {
		t.Fatalf("出处编号应为 1：%+v", first)
	}
	chunkID, _ := first["chunk_id"].(string)

	_, body := h.do(t, http.MethodGet, "/api/classes/"+h.f.classID+"/sessions/"+sessionID, "", headers)
	var payload struct {
		Messages []chatMessageView `json:"messages"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatal(err)
	}
	answer := payload.Messages[len(payload.Messages)-1].Content
	if !strings.Contains(answer, "[1]") {
		t.Fatalf("范围内的编号应保留：%q", answer)
	}
	if strings.Contains(answer, "[7]") || !strings.Contains(answer, "7") {
		t.Fatalf("越界编号应去掉方括号但保留数字：%q", answer)
	}
	if strings.Contains(answer, "[2]") {
		t.Fatalf("超出命中范围的 [2] 不应保留：%q", answer)
	}
	// 出处可由来源接口逐字核对。
	resp, source := h.do(t, http.MethodGet, "/api/classes/"+h.f.classID+"/knowledge/chunks/"+chunkID+"/source", "", headers)
	if resp.StatusCode != 200 {
		t.Fatalf("来源接口失败：%d %s", resp.StatusCode, source)
	}
	if !strings.Contains(source, "考核方式由平时作业") {
		t.Fatalf("来源应返回同一切片正文：%s", source)
	}
}

// 任务 4.6：跨班、非成员、归档与移除成员后的隔离。
func TestChatIsolation(t *testing.T) {
	h := newAuthHarness(t)
	h.seedChunks(t)
	stub := newChatStub(t, chatStubConfig{chunks: []string{"回答 [1]"}})
	h.app.gateway = stub.client
	headers := ownerHeaders(t, h)
	sessionID := h.createSession(t, headers)
	base := "/api/classes/" + h.f.classID + "/sessions/" + sessionID

	student := h.newUser(t, "student", true)
	stranger := h.newUser(t, "teacher", false)

	// 非成员：读、写会话都被拒绝。
	for _, path := range []string{"/api/classes/" + h.f.classID + "/sessions", base} {
		if resp, _ := stranger.request(t, http.MethodGet, path, ""); resp.StatusCode != 403 {
			t.Fatalf("非成员读取 %s 应 403，实际 %d", path, resp.StatusCode)
		}
	}
	if resp, body := stranger.request(t, http.MethodPost, base+"/messages", `{"content":"越权提问"}`); resp.StatusCode != 403 {
		t.Fatalf("非成员发送消息应 403，实际 %d %s", resp.StatusCode, body)
	}

	// 跨班：用另一个班级的路径访问同一会话，表现应与不存在一致。
	otherClass := randomID(16)
	if _, err := h.app.db.ExecContext(h.ctx, `INSERT INTO classrooms(id,name,description,owner_id,invite_code) VALUES(?,?,?,?,?)`,
		otherClass, "另一个班", "", h.user.ID, strings.ToUpper(otherClass[:8])); err != nil {
		t.Fatal(err)
	}
	if resp, body := h.do(t, http.MethodGet, "/api/classes/"+otherClass+"/sessions/"+sessionID, "", headers); resp.StatusCode != 404 {
		t.Fatalf("跨班访问会话应 404，实际 %d %s", resp.StatusCode, body)
	}
	if resp, body := h.do(t, http.MethodPost, "/api/classes/"+otherClass+"/sessions/"+sessionID+"/messages", `{"content":"跨班"}`, headers); resp.StatusCode != 404 {
		t.Fatalf("跨班发送消息应 404，实际 %d %s", resp.StatusCode, body)
	}

	// 学生成员可以读，也可以在会话里提问（本班会话对成员可见）。
	if resp, body := student.request(t, http.MethodGet, base, ""); resp.StatusCode != 200 {
		t.Fatalf("在班学生应能读取会话：%d %s", resp.StatusCode, body)
	}

	// 归档后不能新建会话或发送消息，但可以读取。
	if _, err := h.app.db.ExecContext(h.ctx, `UPDATE classrooms SET archived=1 WHERE id=?`, h.f.classID); err != nil {
		t.Fatal(err)
	}
	if resp, body := h.do(t, http.MethodPost, "/api/classes/"+h.f.classID+"/sessions", `{}`, headers); resp.StatusCode != 403 {
		t.Fatalf("归档后新建会话应 403，实际 %d %s", resp.StatusCode, body)
	}
	if resp, body := h.do(t, http.MethodPost, base+"/messages", `{"content":"归档后提问"}`, headers); resp.StatusCode != 403 {
		t.Fatalf("归档后发送消息应 403，实际 %d %s", resp.StatusCode, body)
	}
	if resp, body := h.do(t, http.MethodGet, base, "", headers); resp.StatusCode != 200 {
		t.Fatalf("归档后仍应可读：%d %s", resp.StatusCode, body)
	}
	if _, err := h.app.db.ExecContext(h.ctx, `UPDATE classrooms SET archived=0 WHERE id=?`, h.f.classID); err != nil {
		t.Fatal(err)
	}

	// 移除成员后立即失去访问。
	if _, err := h.app.db.ExecContext(h.ctx, `DELETE FROM memberships WHERE class_id=? AND user_id=?`, h.f.classID, student.user.ID); err != nil {
		t.Fatal(err)
	}
	if resp, _ := student.request(t, http.MethodGet, base, ""); resp.StatusCode != 403 {
		t.Fatalf("被移除成员应立即失去访问，实际 %d", resp.StatusCode)
	}
}

// 会话列表按最近活动排序，并标注是否本人创建。
func TestChatSessionList(t *testing.T) {
	h := newAuthHarness(t)
	h.seedChunks(t)
	stub := newChatStub(t, chatStubConfig{chunks: []string{"回答 [1]"}})
	h.app.gateway = stub.client
	headers := ownerHeaders(t, h)
	first := h.createSession(t, headers)
	if resp, body := h.do(t, http.MethodPost, "/api/classes/"+h.f.classID+"/sessions", `{"title":"我的第二个会话"}`, headers); resp.StatusCode != 201 {
		t.Fatalf("新建会话失败：%d %s", resp.StatusCode, body)
	}
	resp, body := h.do(t, http.MethodGet, "/api/classes/"+h.f.classID+"/sessions", "", headers)
	if resp.StatusCode != 200 {
		t.Fatalf("会话列表失败：%d %s", resp.StatusCode, body)
	}
	var payload struct {
		Sessions []chatSessionView `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Sessions) != 2 {
		t.Fatalf("应有两个会话：%s", body)
	}
	found := false
	for _, session := range payload.Sessions {
		if session.ID == first {
			found = true
		}
		if !session.Mine {
			t.Fatalf("本人创建的会话应标记 mine：%+v", session)
		}
	}
	if !found {
		t.Fatalf("列表应包含刚创建的会话：%s", body)
	}
	if utf8.RuneCountInString(payload.Sessions[0].Title) > 60 {
		t.Fatalf("标题过长：%q", payload.Sessions[0].Title)
	}
}
