package main

import (
	"context"
	"net/http"
	"time"
)

// routes 是全部对外操作的唯一清单：路由注册、`docs/openapi.json` 的一致性检查与浏览器试调
// 都以此为准。新增接口时先加在这里，再补文档；`openapi_test.go` 会双向比对两者是否一致。
func (a *App) routes() []routeSpec {
	return []routeSpec{
		{"GET", "/api/health", false, func(w http.ResponseWriter, r *http.Request, _ *User) error {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			if err := a.db.PingContext(ctx); err != nil {
				return fail(503, "数据库未就绪")
			}
			writeJSON(w, 200, map[string]string{"status": "ok"})
			return nil
		}},
		{"GET", "/api/openapi.json", false, a.openapiDocument},
		{"GET", "/api/docs", false, a.apiDocsPage},
		{"GET", "/api/docs/{asset}", false, a.apiDocsAsset},
		{"POST", "/api/auth/register", false, a.register},
		{"POST", "/api/auth/login", false, a.login},
		{"POST", "/api/auth/token", false, a.token},
		{"POST", "/api/auth/logout", false, func(w http.ResponseWriter, r *http.Request, _ *User) error {
			// 撤销请求实际使用的凭证：Cookie 会话或 Bearer 令牌都是 sessions 表的一行。
			if value, _ := requestToken(r); value != "" {
				if _, err := a.db.ExecContext(r.Context(), `DELETE FROM sessions WHERE token_hash=?`, tokenHash(value)); err != nil {
					return err
				}
			}
			http.SetCookie(w, &http.Cookie{Name: "teaching_session", Value: "", Path: "/", HttpOnly: true, Secure: a.secure, SameSite: http.SameSiteLaxMode, MaxAge: -1})
			writeJSON(w, 200, map[string]bool{"ok": true})
			return nil
		}},
		{"GET", "/api/auth/me", true, func(w http.ResponseWriter, r *http.Request, u *User) error { writeJSON(w, 200, u); return nil }},
		{"GET", "/api/admin/teachers", true, a.teachers},
		{"POST", "/api/admin/teachers", true, a.createTeacher},
		{"GET", "/api/admin/prompts", true, a.adminPrompts},
		{"PUT", "/api/admin/prompts", true, a.updateAdminPrompt},
		{"PUT", "/api/admin/skills/{name}", true, a.updateAdminSkill},
		{"DELETE", "/api/admin/skills/{name}", true, a.deleteAdminSkill},
		{"GET", "/api/classes", true, a.classes},
		{"POST", "/api/classes", true, a.createClass},
		{"POST", "/api/classes/join", true, a.joinClass},
		{"GET", "/api/classes/{id}", true, a.classDetail},
		{"PATCH", "/api/classes/{id}", true, a.updateClass},
		{"POST", "/api/classes/{id}/archive", true, a.archiveClass},
		{"DELETE", "/api/classes/{id}/members/{userID}", true, a.removeMember},
		{"POST", "/api/classes/{id}/handouts", true, a.upload},
		{"GET", "/api/handouts/{id}/file", true, a.download},
		{"DELETE", "/api/handouts/{id}", true, a.deleteHandout},
		{"GET", "/api/classes/{id}/knowledge/documents", true, a.knowledgeDocuments},
		{"POST", "/api/classes/{id}/knowledge/documents/{handoutID}/index", true, a.indexKnowledgeDocument},
		{"POST", "/api/classes/{id}/knowledge/search", true, a.searchKnowledge},
		{"POST", "/api/classes/{id}/knowledge/ask", true, a.askKnowledge},
		{"GET", "/api/classes/{id}/knowledge/chunks/{chunkID}/source", true, a.knowledgeSource},
		{"GET", "/api/classes/{id}/prompts", true, a.classPrompts},
		{"PUT", "/api/classes/{id}/prompts", true, a.updateClassPrompt},
		{"DELETE", "/api/classes/{id}/prompts", true, a.deleteClassPrompt},
		{"PUT", "/api/classes/{id}/skills/{name}", true, a.updateClassSkill},
		{"DELETE", "/api/classes/{id}/skills/{name}", true, a.deleteClassSkill},
		{"POST", "/api/classes/{id}/sessions", true, a.createChatSession},
		{"GET", "/api/classes/{id}/sessions", true, a.listChatSessions},
		{"GET", "/api/classes/{id}/sessions/{sid}", true, a.chatSessionMessages},
		{"POST", "/api/classes/{id}/sessions/{sid}/messages", true, a.sendChatMessage},
		{"GET", "/api/tool-servers", true, a.listToolServersHandler},
		{"POST", "/api/tool-servers", true, a.createToolServer},
		{"PATCH", "/api/tool-servers/{id}", true, a.updateToolServer},
		{"DELETE", "/api/tool-servers/{id}", true, a.deleteToolServer},
		{"POST", "/api/tool-servers/{id}/connect", true, a.connectToolServer},
		{"POST", "/api/tool-servers/{id}/disconnect", true, a.disconnectToolServer},
		{"GET", "/api/tool-calls", true, a.listToolCalls},
	}
}
