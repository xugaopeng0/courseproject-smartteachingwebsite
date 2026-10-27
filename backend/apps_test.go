package main

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// doMultipart 用同源 handler 直接发一次 multipart 请求（无需真实服务器）。
func (h *authHarness) doMultipart(t *testing.T, path, filename, content string, headers map[string]string) (int, string) {
	t.Helper()
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, path, &buffer)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	h.app.handler().ServeHTTP(recorder, request)
	return recorder.Code, recorder.Body.String()
}

// 应用清单可读；未知应用 400；quiz 的任务段进入 system 提示词，qa 不带。
func TestAppsListAndQuizTemplate(t *testing.T) {
	h := newAuthHarness(t)
	owner := promptClient{h: h, headers: map[string]string{"Authorization": "Bearer " + h.issueToken(t)}, user: h.user}
	base := "/api/classes/" + h.f.classID

	resp, body := owner.request(t, http.MethodGet, base+"/apps", "")
	if resp.StatusCode != 200 || !strings.Contains(body, "\"qa\"") || !strings.Contains(body, "\"quiz\"") {
		t.Fatalf("应用清单应包含 qa 与 quiz：%d %s", resp.StatusCode, body)
	}

	h.seedChunks(t)
	stub := newChatStub(t, chatStubConfig{chunks: []string{"第一题：[1]"}, nonStream: true})
	h.app.gateway = stub.client

	if resp, body = owner.request(t, http.MethodPost, base+"/knowledge/ask", `{"query":"考核方式","app":"nope"}`); resp.StatusCode != 400 {
		t.Fatalf("未知应用应 400，实际 %d %s", resp.StatusCode, body)
	}
	if resp, body = owner.request(t, http.MethodPost, base+"/knowledge/ask", `{"query":"考核方式","app":"quiz"}`); resp.StatusCode != 200 {
		t.Fatalf("quiz 应 200，实际 %d %s", resp.StatusCode, body)
	}
	if !strings.Contains(stub.lastPrompt, "出 3 道由易到难的练习题") {
		t.Fatalf("quiz 任务段应进入提示词：%s", stub.lastPrompt)
	}
	if !strings.Contains(stub.lastPrompt, "考核方式") {
		t.Fatalf("模板里的 {{question}} 应被替换：%s", stub.lastPrompt)
	}
	if resp, body = owner.request(t, http.MethodPost, base+"/knowledge/ask", `{"query":"考核方式"}`); resp.StatusCode != 200 {
		t.Fatalf("qa 应 200，实际 %d %s", resp.StatusCode, body)
	}
	if strings.Contains(stub.lastPrompt, "出 3 道由易到难的练习题") {
		t.Fatalf("默认应用不应带出题任务：%s", stub.lastPrompt)
	}
}

// 提示词变量渲染：认识的替换、不认识的保留、无变量原样返回。
func TestRenderTemplateVariables(t *testing.T) {
	vars := map[string]string{"question": "考核占比", "class_name": "示例班", "skill_names": "解题引导", "date": "2026-10-27"}
	out := renderTemplate("班：{{class_name}}；技能：{{skill_names}}；问：{{question}}；日期：{{date}}；未知：{{nope}}", vars)
	for _, want := range []string{"示例班", "解题引导", "考核占比", "2026-10-27", "{{nope}}"} {
		if !strings.Contains(out, want) {
			t.Fatalf("渲染结果缺少 %q：%s", want, out)
		}
	}
	if renderTemplate("无变量", vars) != "无变量" {
		t.Fatal("没有变量时应原样返回")
	}
}

// 技能文件上传：.md（front-matter）与 .json 成功，异常类型/未闭合/工具未连接被拒。
func TestSkillImport(t *testing.T) {
	th := newToolHarness(t, true)
	path := th.classPath("/skills/import")

	markdown := "---\nname: 上传技能甲\nenabled: true\ntools: formula_hint@campus-tools\n---\n先给结论，再给出处。\n"
	status, body := th.server.doMultipart(t, path, "skill.md", markdown, th.owner.headers)
	if status != 201 || !strings.Contains(body, "上传技能甲") || !strings.Contains(body, "formula_hint@campus-tools") {
		t.Fatalf("md 上传应 201 并解析名称与工具：%d %s", status, body)
	}
	jsonSkill := `{"name":"上传技能乙","body":"用要点作答。","enabled":false}`
	if status, body = th.server.doMultipart(t, path, "skill.json", jsonSkill, th.owner.headers); status != 201 || !strings.Contains(body, "上传技能乙") {
		t.Fatalf("json 上传应 201：%d %s", status, body)
	}
	if status, _ = th.server.doMultipart(t, path, "skill.exe", "x", th.owner.headers); status != 400 {
		t.Fatalf("不支持的类型应 400，实际 %d", status)
	}
	if status, _ = th.server.doMultipart(t, path, "skill.md", "---\nname: 坏技能\n", th.owner.headers); status != 400 {
		t.Fatalf("front-matter 未闭合应 400，实际 %d", status)
	}
	if _, err := th.app.db.ExecContext(th.server.ctx, `UPDATE tool_servers SET connected=0 WHERE id=?`, th.row.ID); err != nil {
		t.Fatal(err)
	}
	disconnected := "---\nname: 上传技能丙\ntools: formula_hint@campus-tools\n---\n正文。\n"
	if status, body = th.server.doMultipart(t, path, "skill3.md", disconnected, th.owner.headers); status != 400 {
		t.Fatalf("工具未连接时应 400：%d %s", status, body)
	}
}
