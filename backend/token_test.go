package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// authHarness 用真实 handler 起一个测试服务器，走完整的 route → currentUser 路径。
type authHarness struct {
	app    *App
	ctx    context.Context
	server *httptest.Server
	f      fixture
	user   User
	pass   string
}

func newAuthHarness(t *testing.T) *authHarness {
	t.Helper()
	a, ctx := testApp(t)
	f := seedFixture(t, a, ctx)
	h := &authHarness{app: a, ctx: ctx, f: f, pass: "verify-pass-123"}
	request := httptest.NewRequest(http.MethodPost, "/api/auth/register", nil)
	u, err := a.addUser(request, credentials{Username: "apit" + f.userID[:6], Name: "接口测试教师", Password: h.pass}, "teacher")
	if err != nil {
		t.Fatal(err)
	}
	h.user = u
	// 让 fixture 班级归这位可登录的教师所有，令牌才会被认作所属教师。
	if _, err = a.db.ExecContext(ctx, `UPDATE classrooms SET owner_id=? WHERE id=?`, u.ID, f.classID); err != nil {
		t.Fatal(err)
	}
	h.server = httptest.NewServer(a.handler())
	t.Cleanup(h.server.Close)
	return h
}

func (h *authHarness) do(t *testing.T, method, path, body string, headers map[string]string) (*http.Response, string) {
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
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(payload)
}

func (h *authHarness) credentials(username, password string) string {
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	return string(body)
}

func (h *authHarness) issueToken(t *testing.T) string {
	t.Helper()
	resp, body := h.do(t, http.MethodPost, "/api/auth/token", h.credentials(h.user.Username, h.pass), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("签发令牌失败: %d %s", resp.StatusCode, body)
	}
	var payload struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expires_at"`
		User      User   `json:"user"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Token) < 32 || payload.ExpiresAt == "" || payload.User.ID != h.user.ID {
		t.Fatalf("令牌响应字段不完整: %s", body)
	}
	if _, err := time.Parse(time.RFC3339, payload.ExpiresAt); err != nil {
		t.Fatalf("expires_at 不是 RFC3339: %v", err)
	}
	if resp.Header.Get("Set-Cookie") != "" {
		t.Fatalf("签发令牌不得写 Cookie，实际 %q", resp.Header.Get("Set-Cookie"))
	}
	return payload.Token
}

// 任务 2.1：签发、只存哈希、错误凭证与未知账号表现一致。
func TestTokenIssuance(t *testing.T) {
	h := newAuthHarness(t)
	value := h.issueToken(t)

	var rows int
	if err := h.app.db.QueryRowContext(h.ctx, `SELECT COUNT(*) FROM sessions WHERE token_hash=?`, tokenHash(value)).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("令牌应写入 sessions，实际 %d 行", rows)
	}
	if err := h.app.db.QueryRowContext(h.ctx, `SELECT COUNT(*) FROM sessions WHERE token_hash=?`, value).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatal("数据库不得保存令牌原文")
	}

	_, wrong := h.do(t, http.MethodPost, "/api/auth/token", h.credentials(h.user.Username, "wrong-password"), nil)
	respUnknown, unknown := h.do(t, http.MethodPost, "/api/auth/token", h.credentials("nosuchuser", "wrong-password"), nil)
	if respUnknown.StatusCode != 401 || !strings.Contains(unknown, "账号或密码错误") || !strings.Contains(wrong, "账号或密码错误") {
		t.Fatalf("错误凭证应统一返回 401 且不区分账号：unknown=%d %s wrong=%s", respUnknown.StatusCode, unknown, wrong)
	}
}

// 任务 2.2：Bearer 鉴权、登出撤销、过期失效、失去成员身份后立即失效。
func TestBearerTokenLifecycle(t *testing.T) {
	h := newAuthHarness(t)
	value := h.issueToken(t)
	bearer := map[string]string{"Authorization": "Bearer " + value}

	resp, body := h.do(t, http.MethodGet, "/api/auth/me", "", bearer)
	if resp.StatusCode != 200 || !strings.Contains(body, h.user.Username) {
		t.Fatalf("带令牌应能读取当前用户：%d %s", resp.StatusCode, body)
	}
	if resp, body = h.do(t, http.MethodGet, "/api/auth/me", "", map[string]string{"Authorization": "Bearer not-a-real-token"}); resp.StatusCode != 401 {
		t.Fatalf("伪造令牌应 401，实际 %d %s", resp.StatusCode, body)
	}

	// 过期令牌：直接把过期时间改到过去。
	if _, err := h.app.db.ExecContext(h.ctx, `UPDATE sessions SET expires_at=? WHERE token_hash=?`, time.Now().UTC().Add(-time.Minute), tokenHash(value)); err != nil {
		t.Fatal(err)
	}
	if resp, body = h.do(t, http.MethodGet, "/api/auth/me", "", bearer); resp.StatusCode != 401 || !strings.Contains(body, "令牌无效或已过期") {
		t.Fatalf("过期令牌应 401，实际 %d %s", resp.StatusCode, body)
	}

	// 重新签发后用登出撤销。
	value = h.issueToken(t)
	bearer = map[string]string{"Authorization": "Bearer " + value}
	if resp, body = h.do(t, http.MethodPost, "/api/auth/logout", "", bearer); resp.StatusCode != 200 {
		t.Fatalf("登出应成功：%d %s", resp.StatusCode, body)
	}
	if resp, body = h.do(t, http.MethodGet, "/api/auth/me", "", bearer); resp.StatusCode != 401 {
		t.Fatalf("登出后令牌应失效，实际 %d %s", resp.StatusCode, body)
	}

	// 学生在班时可用令牌读取班级，被移除后立即 403。
	student := User{}
	request := httptest.NewRequest(http.MethodPost, "/api/auth/register", nil)
	student, err := h.app.addUser(request, credentials{Username: "stud" + h.f.userID[:6], Name: "接口测试学生", Password: "student-pass-123"}, "student")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.app.db.ExecContext(h.ctx, `INSERT INTO memberships(class_id,user_id) VALUES(?,?)`, h.f.classID, student.ID); err != nil {
		t.Fatal(err)
	}
	resp, body = h.do(t, http.MethodPost, "/api/auth/token", h.credentials(student.Username, "student-pass-123"), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("学生换取令牌失败：%d %s", resp.StatusCode, body)
	}
	var payload struct {
		Token string `json:"token"`
	}
	if err = json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatal(err)
	}
	studentBearer := map[string]string{"Authorization": "Bearer " + payload.Token}
	if resp, body = h.do(t, http.MethodGet, "/api/classes/"+h.f.classID, "", studentBearer); resp.StatusCode != 200 {
		t.Fatalf("在班学生应能读取班级：%d %s", resp.StatusCode, body)
	}
	if _, err = h.app.db.ExecContext(h.ctx, `DELETE FROM memberships WHERE class_id=? AND user_id=?`, h.f.classID, student.ID); err != nil {
		t.Fatal(err)
	}
	if resp, body = h.do(t, http.MethodGet, "/api/classes/"+h.f.classID, "", studentBearer); resp.StatusCode != 403 {
		t.Fatalf("被移除成员应立即失去访问：%d %s", resp.StatusCode, body)
	}
}

// 任务 2.3：同源校验只作用于 Cookie 身份。
func TestSameOriginBoundaryForTokens(t *testing.T) {
	h := newAuthHarness(t)
	value := h.issueToken(t)

	// 仅令牌：放行（令牌不会被浏览器自动携带）。
	bearerOnlyHeaders := map[string]string{"Origin": "http://evil.example", "Authorization": "Bearer " + value}
	if resp, body := h.do(t, http.MethodPost, "/api/auth/logout", "", bearerOnlyHeaders); resp.StatusCode != 200 {
		t.Fatalf("仅令牌的跨站写请求应放行：%d %s", resp.StatusCode, body)
	}

	// 仅 Cookie：拒绝。
	loginResp, body := h.do(t, http.MethodPost, "/api/auth/login", h.credentials(h.user.Username, h.pass), nil)
	if loginResp.StatusCode != 200 {
		t.Fatalf("登录失败：%d %s", loginResp.StatusCode, body)
	}
	cookie := ""
	for _, item := range loginResp.Cookies() {
		if item.Name == "teaching_session" {
			cookie = item.Name + "=" + item.Value
		}
	}
	if cookie == "" {
		t.Fatal("登录应下发会话 Cookie")
	}
	cookieHeaders := map[string]string{"Origin": "http://evil.example", "Cookie": cookie}
	if resp, body := h.do(t, http.MethodPost, "/api/auth/logout", "", cookieHeaders); resp.StatusCode != 403 || !strings.Contains(body, "不允许跨站请求") {
		t.Fatalf("仅 Cookie 的跨站写请求应被拒绝：%d %s", resp.StatusCode, body)
	}

	// 令牌 + Cookie：按 Cookie 规则拒绝。
	bothHeaders := map[string]string{"Origin": "http://evil.example", "Cookie": cookie, "Authorization": "Bearer " + h.issueToken(t)}
	if resp, body := h.do(t, http.MethodPost, "/api/auth/logout", "", bothHeaders); resp.StatusCode != 403 {
		t.Fatalf("同时带 Cookie 时应按 Cookie 规则拒绝：%d %s", resp.StatusCode, body)
	}

	// 同源写请求不受影响。
	sameOrigin := map[string]string{"Origin": h.server.URL, "Authorization": "Bearer " + h.issueToken(t)}
	if resp, body := h.do(t, http.MethodPost, "/api/auth/logout", "", sameOrigin); resp.StatusCode != 200 {
		t.Fatalf("同源令牌请求应放行：%d %s", resp.StatusCode, body)
	}
}
